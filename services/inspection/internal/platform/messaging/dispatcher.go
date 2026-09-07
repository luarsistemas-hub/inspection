package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrUnroutable = errors.New("message unroutable")

type Publication struct {
	RoutingKey    string
	Body          []byte
	Mandatory     bool
	CorrelationID string
	CausationID   string
}

type Publisher interface {
	PublishConfirmed(context.Context, Publication) error
}

type Dispatcher struct {
	DB        *gorm.DB
	Publisher Publisher
	BatchSize int
	Clock     func() time.Time
}

func (d Dispatcher) Dispatch(ctx context.Context) (int, error) {
	if d.DB == nil || d.Publisher == nil {
		return 0, errors.New("messaging dispatcher: missing dependency")
	}
	batch := d.BatchSize
	if batch <= 0 {
		batch = 50
	}
	now := time.Now
	if d.Clock != nil {
		now = d.Clock
	}
	var rows []database.OutboxIntent
	claimedAt := now().UTC()
	err := d.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("(status = ? AND next_attempt_at <= ?) OR (status = ? AND claimed_at <= ?)", "PENDING", claimedAt, "CLAIMED", claimedAt.Add(-time.Minute)).
			Order("created_at").Limit(batch).Find(&rows).Error; err != nil {
			return err
		}
		for i := range rows {
			result := tx.Model(&database.OutboxIntent{}).
				Where("id = ? AND status IN ?", rows[i].ID, []string{"PENDING", "CLAIMED"}).
				Updates(map[string]any{"status": "CLAIMED", "claimed_at": claimedAt})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("outbox claim lost")
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("claim outbox: %w", err)
	}
	published := 0
	for _, row := range rows {
		if err := d.Publisher.PublishConfirmed(ctx, Publication{RoutingKey: row.Type, Body: row.Payload, Mandatory: true, CorrelationID: row.CorrelationID, CausationID: row.CausationID}); err != nil {
			d.DB.WithContext(ctx).Model(&database.OutboxIntent{}).Where("id = ? AND status = ?", row.ID, "CLAIMED").Updates(map[string]any{"status": "PENDING", "claimed_at": nil, "attempts": gorm.Expr("attempts + 1"), "next_attempt_at": claimedAt, "last_error": safeReason(err)})
			continue
		}
		result := d.DB.WithContext(ctx).Model(&database.OutboxIntent{}).Where("id = ? AND status = ?", row.ID, "CLAIMED").Updates(map[string]any{"status": "PUBLISHED", "claimed_at": nil, "published_at": now(), "last_error": ""})
		if result.Error != nil {
			return published, result.Error
		}
		published += int(result.RowsAffected)
	}
	return published, nil
}

func safeReason(err error) string {
	if errors.Is(err, ErrUnroutable) {
		return "unroutable"
	}
	return "dependency failure"
}

func AddOutbox[T any](tx *gorm.DB, envelope events.Envelope[T]) error {
	if tx == nil {
		return errors.New("outbox: missing transaction")
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return tx.Create(&database.OutboxIntent{ID: envelope.ID, TenantID: envelope.TenantID, Type: envelope.Type, SchemaVersion: envelope.SchemaVersion, Payload: data, CorrelationID: envelope.CorrelationID, CausationID: envelope.CausationID, Status: "PENDING", NextAttemptAt: envelope.OccurredAt, CreatedAt: envelope.OccurredAt}).Error
}

type Consumer struct {
	DB       *gorm.DB
	Registry *events.Registry
	Name     string
	Handle   func(context.Context, *gorm.DB, events.RawEnvelope) error
	Clock    func() time.Time
}

func (c Consumer) Process(ctx context.Context, body []byte, generation int) (bool, error) {
	if c.DB == nil || c.Registry == nil || c.Name == "" || c.Handle == nil || generation < 0 {
		return false, errors.New("consumer: missing dependency")
	}
	envelope, err := c.Registry.Validate(body)
	if err != nil {
		return false, fmt.Errorf("%w: contract", ErrPermanent)
	}
	now := time.Now
	if c.Clock != nil {
		now = c.Clock
	}
	processed := false
	err = c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SELECT set_config('app.tenant_id', ?, true)", envelope.TenantID.String()).Error; err != nil {
				return err
			}
		}
		row := database.InboxReceipt{ID: identity.NewID(), TenantID: envelope.TenantID, Consumer: c.Name, EventID: envelope.ID, Generation: generation, ProcessedAt: now()}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if err := c.Handle(ctx, tx, envelope); err != nil {
			return err
		}
		processed = true
		return nil
	})
	return processed, err
}
