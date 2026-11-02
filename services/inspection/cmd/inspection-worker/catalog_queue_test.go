package main

import (
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/messaging"
)

func TestUnit_CatalogCoordinationQueuesAreRegisteredWithoutCollisions(t *testing.T) {
	contracts, err := messaging.CatalogQueueContracts()
	if err != nil {
		t.Fatal(err)
	}
	taken := map[string]bool{}
	routing := map[string]bool{}
	for _, definition := range workerQueueDefinitions {
		taken[definition[0]], routing[definition[1]] = true, true
	}
	note, revision := identity.NewID(), identity.NewID()
	payloads := map[string]any{
		"transcription.completed.v1": events.TranscriptionCompletedPayload{NoteID: note, RevisionID: &revision, Generation: 1},
		"transcription.failed.v1":    events.TranscriptionFailedPayload{NoteID: note, Generation: 1, SafeCode: "PROVIDER_UNAVAILABLE"},
		"inventory.job_requested.v1": events.InventoryJobRequestedPayload{JobID: identity.NewID(), InspectionID: identity.NewID()},
	}
	for _, contract := range contracts {
		if taken[contract.Name] || routing[contract.RoutingKey] {
			t.Fatalf("catalog queue %s (%s) collides with an existing worker queue", contract.Name, contract.RoutingKey)
		}
		raw, _ := json.Marshal(events.Envelope[any]{ID: identity.NewID(), Type: contract.RoutingKey, SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: identity.NewID(), AggregateID: note, CorrelationID: note.String(), Payload: payloads[contract.RoutingKey]})
		if _, err := events.DefaultRegistry().Validate(raw); err != nil {
			t.Fatalf("%s is not a registered contract: %v", contract.RoutingKey, err)
		}
		if contract.DLQName == "" || contract.RetryNames[0] == "" {
			t.Fatalf("catalog queue %s has no retry/dead-letter topology", contract.Name)
		}
	}
	if len(ordinaryQueueContracts(contracts)) != len(contracts) {
		t.Fatal("catalog queues must be consumed by the ordinary worker")
	}
}
