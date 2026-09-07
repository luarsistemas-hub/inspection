package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/messaging"
)

// Publish sends a validated event directly to the worker exchange. The target
// queue must have been declared with the matching routing key first.
func (h *Harness) Publish(ctx context.Context, envelope events.RawEnvelope) error {
	if h == nil || h.Publisher == nil {
		return errors.New("integration harness: publisher unavailable")
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	if _, err := events.DefaultRegistry().Validate(body); err != nil {
		return fmt.Errorf("validate event: %w", err)
	}
	return h.Publisher.PublishConfirmed(ctx, messaging.Publication{
		RoutingKey:    envelope.Type,
		Body:          body,
		Mandatory:     true,
		CorrelationID: envelope.CorrelationID,
		CausationID:   envelope.CausationID,
	})
}

// PublishPayload wraps a payload in the canonical v1 event envelope.
func (h *Harness) PublishPayload(ctx context.Context, tenantID, aggregateID identity.ID, eventType, correlationID string, payload any) (identity.ID, error) {
	if tenantID == (identity.ID{}) || eventType == "" || correlationID == "" {
		return identity.ID{}, errors.New("integration harness: incomplete event context")
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return identity.ID{}, fmt.Errorf("marshal event payload: %w", err)
	}
	eventID := identity.NewID()
	envelope := events.RawEnvelope{ID: eventID, Type: eventType, SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: tenantID, AggregateID: aggregateID, CorrelationID: correlationID, Payload: payloadJSON}
	if err := h.Publish(ctx, envelope); err != nil {
		return identity.ID{}, err
	}
	return eventID, nil
}
