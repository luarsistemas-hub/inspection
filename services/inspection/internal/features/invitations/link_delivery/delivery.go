package link_delivery

import (
	"context"
	"errors"
	"time"

	"inspection/libs/identity"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/notifications"
	"inspection/services/inspection/internal/platform/security"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func Deliver(ctx context.Context, db *gorm.DB, registry *notifications.Registry, tenantID, invitationID identity.ID, token string, delivery []invitationcore.DeliveryIntent, template string) error {
	within := func(ctx context.Context, tenant identity.ID, fn func(*gorm.DB) error) error {
		return (tenanttx.Runner{DB: db}).Within(ctx, tenant, fn)
	}
	return deliver(ctx, registry, tenantID, invitationID, token, delivery, template, time.Now().UTC(), within)
}

type withinFunc func(context.Context, identity.ID, func(*gorm.DB) error) error

func deliver(ctx context.Context, registry *notifications.Registry, tenantID, invitationID identity.ID, token string, delivery []invitationcore.DeliveryIntent, template string, now time.Time, within withinFunc) error {
	if registry == nil || within == nil || tenantID == (identity.ID{}) || invitationID == (identity.ID{}) || token == "" || len(delivery) == 0 {
		return apperror.New(apperror.InvalidInput, "delivery", "valid invitation delivery is required")
	}
	deliverySeed := database.Delivery{ID: identity.NewID(), TenantID: tenantID, IntentID: invitationID, Status: "QUEUED", CreatedAt: now, UpdatedAt: now}
	var deliveryRow database.Delivery
	prior := map[string]database.ChannelAttempt{}
	if err := within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND intent_id=?", tenantID, invitationID).Attrs(deliverySeed).FirstOrCreate(&deliveryRow).Error; err != nil {
			return err
		}
		var rows []database.ChannelAttempt
		if err := tx.Where("tenant_id=? AND delivery_id=?", tenantID, deliveryRow.ID).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			prior[row.Channel+"\x00"+row.Destination] = row
		}
		return nil
	}); err != nil {
		return err
	}

	succeeded := 0
	attempts := make([]database.ChannelAttempt, 0, len(delivery))
	for _, destination := range delivery {
		key := destination.Channel + "\x00" + destination.Destination
		if existing, ok := prior[key]; ok && (existing.Status == "SENT" || existing.Status == "DELIVERED") {
			succeeded++
			continue
		}
		sender, err := registry.Sender(notifications.Channel(destination.Channel))
		attempt := database.ChannelAttempt{ID: identity.NewID(), TenantID: tenantID, Channel: destination.Channel, Destination: destination.Destination, Status: "FAILED", Attempts: 1, CreatedAt: now, UpdatedAt: now}
		if err == nil {
			receipt, sendErr := sender.Send(ctx, notifications.Intent{ID: invitationID.String(), Destination: destination.Destination, Template: template, Parameters: map[string]string{"body": token, "tenantId": tenantID.String()}})
			if sendErr == nil {
				attempt.Status, attempt.Provider, attempt.ReceiptID = "SENT", receipt.Provider, receipt.ID
				succeeded++
			} else {
				attempt.LastError = "delivery unavailable"
			}
		} else {
			attempt.LastError = "delivery unavailable"
		}
		attempts = append(attempts, attempt)
	}
	if succeeded > 0 {
		deliveryRow.Status = "DELIVERED"
	} else {
		deliveryRow.Status = "FAILED"
	}
	err := within(ctx, tenantID, func(tx *gorm.DB) error {
		for _, attempt := range attempts {
			attempt.DeliveryID = deliveryRow.ID
			var prior database.ChannelAttempt
			result := tx.Where("tenant_id=? AND delivery_id=? AND channel=? AND destination=?", tenantID, deliveryRow.ID, attempt.Channel, attempt.Destination).Attrs(attempt).FirstOrCreate(&prior)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 && prior.Status != "SENT" {
				if err := tx.Model(&prior).Updates(map[string]any{"status": attempt.Status, "provider": attempt.Provider, "receipt_id": attempt.ReceiptID, "attempts": gorm.Expr("attempts + 1"), "last_error": attempt.LastError, "updated_at": now}).Error; err != nil {
					return err
				}
			}
		}
		return tx.Model(&deliveryRow).Updates(map[string]any{"status": deliveryRow.Status, "updated_at": now}).Error
	})
	if err != nil {
		return err
	}
	if succeeded == 0 {
		return apperror.Wrap(apperror.DependencyUnavailable, errors.New("invitation delivery unavailable"))
	}
	return nil
}

func RetryToken(ctx context.Context, db *gorm.DB, tenantID, invitationID identity.ID) (string, error) {
	var status string
	var failed int64
	err := (tenanttx.Runner{DB: db}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Delivery{}).Select("status").Where("tenant_id=? AND intent_id=?", tenantID, invitationID).Scan(&status).Error; err != nil {
			return err
		}
		return tx.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND delivery_id=(SELECT id FROM notifications.deliveries WHERE tenant_id=? AND intent_id=?) AND status='FAILED'", tenantID, tenantID, invitationID).Count(&failed).Error
	})
	if err != nil || (status != "FAILED" && failed == 0) {
		return "", err
	}
	token, err := security.NewScopedToken(tenantID)
	if err != nil {
		return "", err
	}
	hash := security.HashToken(token)
	err = (tenanttx.Runner{DB: db}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Model(&database.Invitation{}).Where("tenant_id=? AND id=? AND status='ACTIVE' AND expires_at>?", tenantID, invitationID, time.Now().UTC()).Updates(map[string]any{"previous_token_hash": gorm.Expr("token_hash"), "token_hash": hash[:]})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return apperror.New(apperror.InvalidState, "invitation", "invitation is unavailable")
		}
		return nil
	})
	return token, err
}
