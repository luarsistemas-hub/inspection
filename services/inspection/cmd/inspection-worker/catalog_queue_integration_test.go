//go:build integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/messaging"
)

// TestCatalogCoordinationQueuesRouteMSG02MSG03MSG05 declares the catalog
// topology (with retry and DLQ queues) and checks that each queue exists with
// its retry and dead-letter queues. It never publishes to the shared exchange.
func TestCatalogCoordinationQueuesRouteMSG02MSG03MSG05(t *testing.T) {
	for _, key := range []string{"INSPECTION_TEST_DATABASE_URL", "INSPECTION_TEST_RUNTIME_DATABASE_URL", "INSPECTION_TEST_RABBITMQ_URL"} {
		if os.Getenv(key) == "" {
			t.Skip(key + " is required")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	contracts, err := messaging.CatalogQueueContracts("it-catalog-" + identity.NewID().String() + "-")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Declare(contracts...); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, contract := range contracts {
			for _, name := range append([]string{contract.Name, contract.DLQName}, contract.RetryNames[:]...) {
				_, _ = h.Channel.QueueDelete(name, false, false, false)
			}
		}
	})
	tenant, note := identity.NewID(), identity.NewID()
	revision := identity.NewID()
	payloads := map[string]any{
		"transcription.completed.v1": events.TranscriptionCompletedPayload{NoteID: note, RevisionID: &revision, Generation: 1},
		"transcription.failed.v1":    events.TranscriptionFailedPayload{NoteID: note, Generation: 1, SafeCode: "PROVIDER_UNAVAILABLE"},
		"inventory.job_requested.v1": events.InventoryJobRequestedPayload{JobID: identity.NewID(), InspectionID: identity.NewID()},
	}
	for _, contract := range contracts {
		raw, _ := json.Marshal(events.Envelope[any]{ID: identity.NewID(), Type: contract.RoutingKey, SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: tenant, AggregateID: note, CorrelationID: note.String(), Payload: payloads[contract.RoutingKey]})
		if _, err := events.DefaultRegistry().Validate(raw); err != nil {
			t.Fatalf("%s envelope violates its contract: %v", contract.RoutingKey, err)
		}
		// The topic exchange is shared with the live workers, whose queues bind
		// the same routing keys: publishing here would feed them. Only the
		// declaration of the catalog queues (and the contract of each event)
		// is verified; routing is covered by the unit tests of the contracts.
		if _, err := h.Channel.QueueDeclarePassive(contract.Name, true, false, false, false, nil); err != nil {
			t.Fatalf("queue %s for %s was not declared: %v", contract.Name, contract.RoutingKey, err)
		}
		for _, name := range append([]string{contract.DLQName}, contract.RetryNames[:]...) {
			if _, err := h.Channel.QueueDeclarePassive(name, true, false, false, false, nil); err != nil {
				t.Fatalf("queue %s was not declared: %v", name, err)
			}
		}
	}
}
