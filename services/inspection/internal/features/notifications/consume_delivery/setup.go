// Package consume_delivery validates v2 delivery references at the inbox boundary.
package consume_delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

// Setup returns a handler that is executed in messaging.Consumer's transaction,
// so durable work and its inbox receipt commit before RabbitMQ is acknowledged.
func Setup() func(context.Context, *gorm.DB, events.RawEnvelope) error {
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		if envelope.Type != "notification.delivery_requested.v2" || envelope.SchemaVersion != 2 {
			return messaging.ErrPermanent
		}
		var payload struct {
			NotificationID identity.ID `json:"notificationId"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.NotificationID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		var delivery database.Delivery
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, payload.NotificationID).First(&delivery).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: notification reference missing", messaging.ErrPermanent)
			}
			return err
		}
		return nil
	}
}
