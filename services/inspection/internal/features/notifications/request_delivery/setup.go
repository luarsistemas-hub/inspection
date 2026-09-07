package request_delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

type Recipient struct {
	ID, Channel, Destination     string
	Internal, Verified, Selected bool
}
type Input struct {
	TenantID, InspectionID identity.ID
	Previous, Current      string
	Recipients             []Recipient
	CorrelationID          string
}

func Request(ctx context.Context, tx *gorm.DB, in Input) error {
	if tx == nil || in.TenantID == (identity.ID{}) || in.InspectionID == (identity.ID{}) {
		return fmt.Errorf("notification delivery: missing tenant or inspection")
	}
	allowed := make([]core.Recipient, 0, len(in.Recipients))
	for _, recipient := range in.Recipients {
		allowed = append(allowed, core.Recipient{ID: recipient.ID, Channel: recipient.Channel, Destination: recipient.Destination, Internal: recipient.Internal, Verified: recipient.Verified, Selected: recipient.Selected})
	}
	recipients := core.FirstCritical(in.Previous, in.Current, allowed)
	if len(recipients) == 0 {
		return nil
	}
	// The intent is deterministic for one inspection/classification transition;
	// replaying the event with a new transport correlation must not create a
	// second alert aggregate.
	intentSeed := "critical:" + in.TenantID.String() + ":" + in.InspectionID.String() + ":" + in.Current
	intentID := identity.ID(uuid.NewSHA1(uuid.Nil, []byte(intentSeed)))
	now := time.Now().UTC()
	var existing database.Delivery
	if err := tx.WithContext(ctx).Where("tenant_id=? AND intent_id=?", in.TenantID, intentID).First(&existing).Error; err == nil {
		return nil
	} else if err != gorm.ErrRecordNotFound {
		return err
	}
	delivery := database.Delivery{ID: identity.NewID(), TenantID: in.TenantID, IntentID: intentID, InspectionID: &in.InspectionID, Status: "PENDING", CreatedAt: now, UpdatedAt: now}
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&delivery).Error; err != nil {
		return err
	}
	for _, recipient := range recipients {
		attempt := database.ChannelAttempt{ID: identity.NewID(), TenantID: in.TenantID, DeliveryID: delivery.ID, Channel: recipient.Channel, Destination: recipient.Destination, Status: "PENDING", CreatedAt: now, UpdatedAt: now}
		if err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&attempt).Error; err != nil {
			return err
		}
	}
	if in.CorrelationID == "" {
		in.CorrelationID = "notification-critical-" + in.InspectionID.String()
	}
	payload, err := json.Marshal(map[string]any{"deliveryId": delivery.ID, "inspectionId": in.InspectionID})
	if err != nil {
		return err
	}
	return messaging.AddOutbox(tx, events.Envelope[json.RawMessage]{ID: identity.NewID(), Type: "notification.delivery_requested.v1", SchemaVersion: 1, OccurredAt: now, TenantID: in.TenantID, AggregateID: in.InspectionID, CorrelationID: in.CorrelationID, Payload: payload})
}
