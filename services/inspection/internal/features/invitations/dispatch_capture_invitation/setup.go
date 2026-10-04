// Package dispatch_capture_invitation creates central capture-link requests.
package dispatch_capture_invitation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"inspection/services/inspection/internal/platform/apperror"
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
	"gorm.io/gorm/clause"
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
		tx = tx.WithContext(ctx)
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
		var responsibility database.Responsibility
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=? AND inspection_id=? AND participant_id=?", envelope.TenantID, payload.ResponsibilityID, payload.InspectionID, payload.ParticipantID).First(&responsibility).Error; err != nil {
			return err
		}
		if responsibility.Status != "PENDING" {
			return nil
		}
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", envelope.TenantID, payload.InspectionID).First(&inspection).Error; err != nil {
			return err
		}
		var existing int64
		if err := tx.Model(&database.Invitation{}).Where("tenant_id=? AND responsibility_id=? AND status='ACTIVE'", envelope.TenantID, payload.ResponsibilityID).Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return markInvited(tx, &inspection, time.Now().UTC())
		}
		var participant database.Participant
		if err := tx.Where("tenant_id=? AND id=?", envelope.TenantID, payload.ParticipantID).First(&participant).Error; err != nil {
			return err
		}
		var asset database.Asset
		if err := tx.Where("tenant_id=? AND id=?", envelope.TenantID, inspection.AssetID).First(&asset).Error; err != nil {
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
		invitation := database.Invitation{ID: identity.NewID(), TenantID: envelope.TenantID, ResponsibilityID: payload.ResponsibilityID, TokenHash: hash[:], DeliveryIntents: encoded, IdempotencyKey: "inspection-created:" + envelope.ID.String(), Status: "ACTIVE", ExpiresAt: inspection.DeadlineAt, CreatedAt: now}
		if err := tx.Create(&invitation).Error; err != nil {
			return err
		}
		for _, target := range delivery {
			template, variables := core.CaptureLinkNotification(core.Channel(target.Channel), participant.Name, asset.Name, asset.Address, invitation.ExpiresAt)
			if target.Channel == "EMAIL" && !target.Verified {
				template = core.TemplateRef{Name: "capture-link", Version: "v1"}
				variables = map[string]string{"recipientName": participant.Name}
			}
			_, err := d.Notifications.Send(notificationrequest.InTransaction(ctx, tx), core.Notification{
				TenantID: envelope.TenantID, InspectionID: &payload.InspectionID, InvitationID: &invitation.ID, Recipient: core.Recipient{Destination: target.Destination}, Channel: core.Channel(target.Channel),
				Template: template, Variables: variables,
				CorrelationID: envelope.CorrelationID, IdempotencyKey: deliveryIdempotencyKey(invitation.ID, target),
				Execution: &core.ExecutionPayload{InvitationID: invitation.ID, Token: token, URLVariable: "captureUrl", BaseURL: d.CaptureBaseURL, ExpiresAt: invitation.ExpiresAt.Unix()},
			})
			if apperror.Is(err, apperror.IntegrationDisabled) {
				// Disabled channels are skipped so enabled channels still deliver.
				continue
			}
			if err != nil {
				return err
			}
		}
		return markInvited(tx, &inspection, now)
	}, nil
}

func markInvited(tx *gorm.DB, inspection *database.Inspection, now time.Time) error {
	return tx.Model(inspection).Where("status='PLANNED'").Updates(map[string]any{"status": "INVITED", "version": gorm.Expr("version + 1"), "updated_at": now}).Error
}

func deliveryIdempotencyKey(invitationID identity.ID, target invitationcore.DeliveryIntent) string {
	digest := sha256.Sum256([]byte(target.Channel + "\x00" + target.Destination))
	return invitationID.String() + ":" + target.Channel + ":" + hex.EncodeToString(digest[:])
}

func loadDelivery(tx *gorm.DB, tenantID, participantID identity.ID) ([]invitationcore.DeliveryIntent, error) {
	var contacts []database.ParticipantContact
	err := tx.Table("participants.contacts AS c").Select("c.*").Joins("JOIN participants.channel_selections s ON s.contact_id=c.id AND s.tenant_id=c.tenant_id").Where("c.tenant_id=? AND c.participant_id=? AND c.active", tenantID, participantID).Find(&contacts).Error
	if err != nil {
		return nil, err
	}
	result := make([]invitationcore.DeliveryIntent, 0, len(contacts))
	for _, contact := range contacts {
		var verified int64
		if err := tx.Model(&database.ContactVerification{}).Where("tenant_id=? AND contact_id=? AND status='VERIFIED'", tenantID, contact.ID).Count(&verified).Error; err != nil {
			return nil, err
		}
		// Only unverified EMAIL may receive the reduced template; other channels require verification.
		if verified == 0 && contact.Channel != "EMAIL" {
			continue
		}
		result = append(result, invitationcore.DeliveryIntent{Channel: contact.Channel, Destination: contact.Value, Verified: verified > 0})
	}
	return result, nil
}
