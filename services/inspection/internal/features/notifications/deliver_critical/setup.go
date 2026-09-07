// Package deliver_critical delivers one first-critical alert aggregate while
// keeping channel attempts independently observable and retryable.
package deliver_critical

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/notifications"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Dependencies struct {
	Registry    *notifications.Registry
	CallbackURL string
	Now         func() time.Time
}

// Setup returns an inbox-compatible handler for notification.delivery_requested.v1.
func Setup(deps Dependencies) (func(context.Context, *gorm.DB, events.RawEnvelope) error, error) {
	if deps.Registry == nil {
		return nil, fmt.Errorf("notifications/deliver_critical: missing registry")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload struct {
			DeliveryID   identity.ID `json:"deliveryId"`
			InspectionID identity.ID `json:"inspectionId"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.DeliveryID == (identity.ID{}) || payload.InspectionID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		var delivery database.Delivery
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, payload.DeliveryID).First(&delivery).Error; err != nil {
			return err
		}
		var attempts []database.ChannelAttempt
		if err := tx.WithContext(ctx).Where("tenant_id=? AND delivery_id=?", envelope.TenantID, delivery.ID).Order("channel ASC, destination ASC").Find(&attempts).Error; err != nil {
			return err
		}
		now := deps.Now().UTC()
		succeeded := false
		pending := false
		maxAttempt := 0
		for _, attempt := range attempts {
			if attempt.Status == "SENT" || attempt.Status == "DELIVERED" {
				succeeded = true
				continue
			}
			if attempt.Attempts >= messaging.MaxDeliveryAttempts {
				continue
			}
			sender, err := deps.Registry.Sender(notifications.Channel(attempt.Channel))
			status, provider, receiptID, lastError := "FAILED", "", "", "delivery unavailable"
			attemptCount := attempt.Attempts + 1
			if attemptCount > maxAttempt {
				maxAttempt = attemptCount
			}
			if err == nil {
				receipt, sendErr := sender.Send(ctx, notifications.Intent{ID: delivery.IntentID.String(), Destination: attempt.Destination, Template: "inspection-critical-alert", Parameters: map[string]string{"body": "A critical inspection finding requires internal review.", "tenantId": envelope.TenantID.String(), "inspectionId": payload.InspectionID.String(), "callbackUrl": deps.CallbackURL}})
				if sendErr == nil {
					status, provider, receiptID, lastError, succeeded = "SENT", receipt.Provider, receipt.ID, "", true
				}
			}
			if err := tx.WithContext(ctx).Model(&database.ChannelAttempt{}).Where("tenant_id=? AND id=?", envelope.TenantID, attempt.ID).Updates(map[string]any{"status": status, "provider": provider, "receipt_id": receiptID, "attempts": attemptCount, "last_error": lastError, "updated_at": now}).Error; err != nil {
				return err
			}
			if status == "FAILED" && attemptCount < messaging.MaxDeliveryAttempts {
				pending = true
			}
		}
		if !succeeded && maxAttempt == 0 {
			for _, attempt := range attempts {
				if attempt.Status == "PENDING" || (attempt.Status == "FAILED" && attempt.Attempts < messaging.MaxDeliveryAttempts) {
					pending = true
				}
			}
		}
		status := "FAILED"
		if succeeded {
			status = "DELIVERED"
		} else if pending {
			status = "PENDING"
		}
		if err := tx.WithContext(ctx).Model(&database.Delivery{}).Where("tenant_id=? AND id=?", envelope.TenantID, delivery.ID).Updates(map[string]any{"status": status, "updated_at": now}).Error; err != nil {
			return err
		}
		if !pending || maxAttempt <= 0 || maxAttempt >= len(messaging.RetryDelays) {
			return nil
		}
		payloadBytes, err := json.Marshal(map[string]any{"deliveryId": delivery.ID, "inspectionId": payload.InspectionID})
		if err != nil {
			return err
		}
		retryID := identity.ID(uuid.NewSHA1(delivery.ID, []byte(fmt.Sprintf("critical-retry:%d", maxAttempt))))
		retryAt := now.Add(messaging.RetryDelays[maxAttempt])
		body, err := json.Marshal(events.Envelope[json.RawMessage]{ID: retryID, Type: "notification.delivery_requested.v1", SchemaVersion: 1, OccurredAt: retryAt, TenantID: envelope.TenantID, AggregateID: payload.InspectionID, CorrelationID: envelope.CorrelationID, CausationID: envelope.ID.String(), Payload: payloadBytes})
		if err != nil {
			return err
		}
		return tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&database.OutboxIntent{ID: retryID, TenantID: envelope.TenantID, Type: "notification.delivery_requested.v1", SchemaVersion: 1, Payload: body, CorrelationID: envelope.CorrelationID, CausationID: envelope.ID.String(), Status: "PENDING", NextAttemptAt: retryAt, CreatedAt: now}).Error
	}, nil
}
