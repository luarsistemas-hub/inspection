package messaging

import (
	"encoding/json"
	"errors"
	"fmt"

	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AddOutboxOnce queues the envelope like AddOutbox but treats an existing row
// with the same event ID as already queued. Duplicate scheduler replicas that
// name the same deterministic event therefore never fail the tenant
// transaction on the primary key. It reports whether a new row was inserted.
func AddOutboxOnce[T any](tx *gorm.DB, envelope events.Envelope[T]) (bool, error) {
	if tx == nil {
		return false, errors.New("outbox: missing transaction")
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return false, err
	}
	if _, err := events.DefaultRegistry().Validate(data); err != nil {
		return false, fmt.Errorf("outbox: invalid event contract: %w", err)
	}
	result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).Create(&database.OutboxIntent{ID: envelope.ID, TenantID: envelope.TenantID, Type: envelope.Type, SchemaVersion: envelope.SchemaVersion, Payload: data, CorrelationID: envelope.CorrelationID, CausationID: envelope.CausationID, Status: "PENDING", NextAttemptAt: envelope.OccurredAt, CreatedAt: envelope.OccurredAt})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}
