// Package dispatch_capture_invitation creates central capture-link requests.
package dispatch_capture_invitation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/features/notifications/core"
	notificationrequest "inspection/services/inspection/internal/features/notifications/request"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
)

type Dependencies struct {
	Notifications  core.NotificationService
	CaptureBaseURL string
}

// Setup registers the inbox handler that creates capture invitations and
// durable v2 requests. Provider adapters are intentionally not dependencies.
func Setup(d Dependencies) (func(context.Context, *gorm.DB, events.RawEnvelope) error, error) {
	if d.Notifications == nil || strings.TrimSpace(d.CaptureBaseURL) == "" {
		return nil, fmt.Errorf("slice invitations/dispatch_capture_invitation: missing dependency")
	}
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload struct {
			InspectionID     identity.ID `json:"inspectionId"`
			ResponsibilityID identity.ID `json:"responsibilityId"`
			ParticipantID    identity.ID `json:"participantId"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.ResponsibilityID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		if payload.InspectionID == (identity.ID{}) || payload.ParticipantID == (identity.ID{}) {
			return nil
		}
		var existing int64
		if err := tx.Model(&database.Invitation{}).Where("tenant_id=? AND responsibility_id=? AND status='ACTIVE'", envelope.TenantID, payload.ResponsibilityID).Count(&existing).Error; err != nil || existing > 0 {
			return err
		}
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", envelope.TenantID, payload.InspectionID).First(&inspection).Error; err != nil {
			return err
		}
		delivery, err := loadDelivery(tx, envelope.TenantID, payload.ParticipantID)
		if err != nil || len(delivery) == 0 {
			if err != nil {
				return err
			}
			return messaging.ErrPermanent
		}
		token, err := security.NewScopedToken(envelope.TenantID)
		if err != nil {
			return err
		}
		hash := security.HashToken(token)
		now := time.Now().UTC()
		encoded, _ := json.Marshal(delivery)
		invitation := database.Invitation{ID: identity.NewID(), TenantID: envelope.TenantID, ResponsibilityID: payload.ResponsibilityID, TokenHash: hash[:], DeliveryIntents: encoded, Status: "ACTIVE", ExpiresAt: inspection.DeadlineAt, CreatedAt: now}
		if err := tx.Create(&invitation).Error; err != nil {
			return err
		}
		for _, target := range delivery {
			_, err := d.Notifications.Send(notificationrequest.InTransaction(ctx, tx), core.Notification{
				TenantID: envelope.TenantID, Recipient: core.Recipient{Destination: target.Destination}, Channel: core.Channel(target.Channel),
				Template: core.TemplateRef{Name: "capture-link", Version: "v1"}, Variables: map[string]string{"recipientName": "participante"},
				CorrelationID: envelope.CorrelationID, IdempotencyKey: invitation.ID.String() + ":" + target.Channel + ":" + target.Destination,
				Execution: &core.ExecutionPayload{InvitationID: invitation.ID, Token: token, URLVariable: "captureUrl", BaseURL: d.CaptureBaseURL, ExpiresAt: invitation.ExpiresAt.Unix()},
			})
			if err != nil {
				return err
			}
		}
		return tx.Model(&inspection).Where("status='PLANNED'").Updates(map[string]any{"status": "INVITED", "version": gorm.Expr("version + 1"), "updated_at": now}).Error
	}, nil
}

func loadDelivery(tx *gorm.DB, tenantID, participantID identity.ID) ([]invitationcore.DeliveryIntent, error) {
	var contacts []database.ParticipantContact
	err := tx.Table("participants.contacts AS c").Select("c.*").Joins("JOIN participants.channel_selections s ON s.contact_id=c.id AND s.tenant_id=c.tenant_id").Joins("JOIN participants.contact_verifications v ON v.contact_id=c.id AND v.tenant_id=c.tenant_id AND v.status='VERIFIED'").Where("c.tenant_id=? AND c.participant_id=? AND c.active", tenantID, participantID).Find(&contacts).Error
	if err != nil {
		return nil, err
	}
	result := make([]invitationcore.DeliveryIntent, 0, len(contacts))
	for _, contact := range contacts {
		result = append(result, invitationcore.DeliveryIntent{Channel: contact.Channel, Destination: contact.Value})
	}
	return result, nil
}
