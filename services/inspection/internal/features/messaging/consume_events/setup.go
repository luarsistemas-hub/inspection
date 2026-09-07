package consume_events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Dependencies struct {
	DB         *gorm.DB
	Connection *amqp.Connection
	Contracts  []messaging.QueueContract
	Registry   *events.Registry
	Handlers   map[string]func(context.Context, *gorm.DB, events.RawEnvelope) error
}

func Setup(deps Dependencies) (func(context.Context) error, error) {
	if deps.DB == nil || deps.Connection == nil || deps.Registry == nil || len(deps.Contracts) == 0 {
		return nil, errors.New("consume events: missing dependency")
	}
	return func(ctx context.Context) error {
		errCh := make(chan error, len(deps.Contracts))
		channels := make([]*amqp.Channel, 0, len(deps.Contracts))
		defer func() {
			for _, channel := range channels {
				_ = channel.Close()
			}
		}()
		for _, contract := range deps.Contracts {
			channel, err := deps.Connection.Channel()
			if err != nil {
				return fmt.Errorf("consumer channel: %w", err)
			}
			channels = append(channels, channel)
			handle := HandleLifecycleEvent
			if registered := deps.Handlers[contract.RoutingKey]; registered != nil {
				handle = registered
			}
			consumer := messaging.Consumer{DB: deps.DB, Registry: deps.Registry, Name: contract.Name, Handle: handle}
			rabbit := messaging.RabbitConsumer{Channel: channel, Contract: contract, Handler: consumer, ConsumerName: contract.Name}
			go func() { errCh <- rabbit.Run(ctx) }()
		}
		select {
		case <-ctx.Done():
			return nil
		case err := <-errCh:
			return err
		}
	}, nil
}

// HandleLifecycleEvent applies integration outcomes without coupling lifecycle
// commands to invitation or scheduling persistence.
func HandleLifecycleEvent(_ context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
	switch envelope.Type {
	case "inspection.created.v1":
		var payload struct {
			InspectionID     identity.ID `json:"inspectionId"`
			ResponsibilityID identity.ID `json:"responsibilityId"`
			ParticipantID    identity.ID `json:"participantId"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.InspectionID == (identity.ID{}) || payload.ResponsibilityID == (identity.ID{}) || payload.ParticipantID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		eventID := uuid.NewSHA1(envelope.ID, []byte("origin.invitation_requested.v1"))
		body, _ := json.Marshal(events.Envelope[map[string]any]{
			ID: eventID, Type: "origin.invitation_requested.v1", SchemaVersion: 1,
			OccurredAt: time.Now().UTC(), TenantID: envelope.TenantID, AggregateID: payload.InspectionID,
			CorrelationID: envelope.CorrelationID, CausationID: envelope.ID.String(),
			Payload: map[string]any{"inspectionId": payload.InspectionID, "responsibilityId": payload.ResponsibilityID, "participantId": payload.ParticipantID},
		})
		now := time.Now().UTC()
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&database.OutboxIntent{ID: eventID, TenantID: envelope.TenantID, Type: "origin.invitation_requested.v1", SchemaVersion: 1, Payload: body, CorrelationID: envelope.CorrelationID, CausationID: envelope.ID.String(), Status: "PENDING", NextAttemptAt: now, CreatedAt: now}).Error
	case "inspection.state_changed.v1":
		var payload struct {
			InspectionID     identity.ID `json:"inspectionId"`
			ResponsibilityID identity.ID `json:"responsibilityId"`
			To               string      `json:"to"`
			RevokeSessions   bool        `json:"revokeSessions"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.InspectionID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		if payload.To != "CANCELED" && payload.To != "INVALIDATED" && payload.To != "SUBMITTED" && payload.To != "COMPLETED" {
			return nil
		}
		if err := tx.Model(&database.ReminderPlan{}).Where("tenant_id=? AND inspection_id=? AND status='PLANNED'", envelope.TenantID, payload.InspectionID).Update("status", "CANCELED").Error; err != nil {
			return err
		}
		if payload.RevokeSessions && payload.ResponsibilityID != (identity.ID{}) {
			now := time.Now().UTC()
			if err := tx.Model(&database.ExternalSession{}).Where("tenant_id=? AND responsibility_id=? AND revoked_at IS NULL", envelope.TenantID, payload.ResponsibilityID).Update("revoked_at", now).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
