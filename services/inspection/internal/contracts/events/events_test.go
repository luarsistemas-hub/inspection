package events

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"inspection/libs/identity"
)

func TestUT040EnvelopeRoundTrip(t *testing.T) {
	e := Envelope[map[string]string]{ID: identity.NewID(), Type: "participant.channel_verified.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: identity.NewID(), AggregateID: identity.NewID(), CorrelationID: "corr", Payload: map[string]string{"participantId": identity.NewID().String()}}
	raw, _ := json.Marshal(e)
	got, err := DefaultRegistry().Validate(raw)
	if err != nil || got.ID != e.ID {
		t.Fatalf("round trip failed: %v", err)
	}
}
func TestUT041RegistryRejectsUnsafeContracts(t *testing.T) {
	base := Envelope[map[string]string]{ID: identity.NewID(), Type: "participant.channel_verified.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: identity.NewID(), CorrelationID: "corr", Payload: map[string]string{"participantId": "p"}}
	cases := []func(*Envelope[map[string]string]){func(e *Envelope[map[string]string]) { e.SchemaVersion = 2 }, func(e *Envelope[map[string]string]) { e.CorrelationID = "" }, func(e *Envelope[map[string]string]) { e.Payload = map[string]string{"otp": "123456"} }}
	for _, mutate := range cases {
		e := base
		mutate(&e)
		raw, _ := json.Marshal(e)
		if _, err := DefaultRegistry().Validate(raw); err == nil {
			t.Fatal("expected rejection")
		}
	}
	if _, err := DefaultRegistry().Validate(make([]byte, MaxPayloadBytes+1)); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("expected size error: %v", err)
	}
}

func TestLifecycleEventsIT551ToIT554IT581ToIT582(t *testing.T) {
	registry := DefaultRegistry()
	for _, eventType := range []string{"inspection.created.v1", "inspection.state_changed.v1", "project.stage_changed.v1"} {
		envelope := Envelope[map[string]any]{ID: identity.NewID(), Type: eventType, SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: identity.NewID(), AggregateID: identity.NewID(), CorrelationID: "lifecycle-contract", Payload: map[string]any{"state": "ACTIVE"}}
		raw, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		validated, err := registry.Validate(raw)
		if err != nil || validated.Type != eventType {
			t.Fatalf("%s rejected: %v", eventType, err)
		}
		envelope.SchemaVersion = 2
		invalid, _ := json.Marshal(envelope)
		if _, err := registry.Validate(invalid); !errors.Is(err, ErrUnknownContract) {
			t.Fatalf("%s invalid major accepted: %v", eventType, err)
		}
	}
}
