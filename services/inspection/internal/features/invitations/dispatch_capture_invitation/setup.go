package dispatch_capture_invitation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/notifications"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Dependencies struct {
	Notifications *notifications.Registry
	CallbackURL   string
}

func Setup(d Dependencies) (func(context.Context, *gorm.DB, events.RawEnvelope) error, error) {
	if d.Notifications == nil {
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
		var existing int64
		if err := tx.Model(&database.Invitation{}).Where("tenant_id=? AND responsibility_id=? AND status='ACTIVE'", envelope.TenantID, payload.ResponsibilityID).Count(&existing).Error; err != nil || existing > 0 {
			return err
		}
		if payload.InspectionID == (identity.ID{}) || payload.ParticipantID == (identity.ID{}) {
			return nil // Origin invitations are created and delivered synchronously by their slice.
		}
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", envelope.TenantID, payload.InspectionID).First(&inspection).Error; err != nil {
			return err
		}
		delivery, err := loadDelivery(tx, envelope.TenantID, payload.ParticipantID)
		if err != nil {
			return err
		}
		if len(delivery) == 0 {
			return messaging.ErrPermanent
		}
		token, err := security.NewScopedToken(envelope.TenantID)
		if err != nil {
			return err
		}
		hash := security.HashToken(token)
		encoded, _ := json.Marshal(delivery)
		invitation := database.Invitation{ID: identity.NewID(), TenantID: envelope.TenantID, ResponsibilityID: payload.ResponsibilityID, TokenHash: hash[:], DeliveryIntents: encoded, Status: "ACTIVE", ExpiresAt: inspection.DeadlineAt, CreatedAt: time.Now().UTC()}
		if err := tx.Create(&invitation).Error; err != nil {
			return err
		}
		deliveryRow := database.Delivery{ID: identity.NewID(), TenantID: envelope.TenantID, IntentID: envelope.ID, InspectionID: &payload.InspectionID, Status: "FAILED", CreatedAt: invitation.CreatedAt, UpdatedAt: invitation.CreatedAt}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&deliveryRow).Error; err != nil {
			return err
		}
		succeeded := 0
		for _, destination := range delivery {
			sender, senderErr := d.Notifications.Sender(notifications.Channel(destination.Channel))
			attempt := database.ChannelAttempt{ID: identity.NewID(), TenantID: envelope.TenantID, DeliveryID: deliveryRow.ID, Channel: destination.Channel, Destination: destination.Destination, Status: "FAILED", Attempts: 1, CreatedAt: invitation.CreatedAt, UpdatedAt: invitation.CreatedAt}
			if senderErr == nil {
				receipt, sendErr := sender.Send(ctx, notifications.Intent{ID: invitation.ID.String(), Destination: destination.Destination, Template: "inspection-capture-link", Parameters: map[string]string{"body": token, "tenantId": envelope.TenantID.String(), "callbackUrl": d.CallbackURL}})
				if sendErr == nil {
					attempt.Status, attempt.Provider, attempt.ReceiptID = "SENT", receipt.Provider, receipt.ID
					succeeded++
				} else {
					attempt.LastError = "delivery unavailable"
				}
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&attempt).Error; err != nil {
				return err
			}
		}
		if succeeded == 0 {
			return errors.New("capture invitation delivery unavailable")
		}
		if err := tx.Model(&deliveryRow).Update("status", "DELIVERED").Error; err != nil {
			return err
		}
		return tx.Model(&inspection).Where("status='PLANNED'").Updates(map[string]any{"status": "INVITED", "version": gorm.Expr("version + 1"), "updated_at": invitation.CreatedAt}).Error
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
