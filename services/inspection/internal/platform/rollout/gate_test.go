package rollout

import (
	"context"
	"errors"
	"testing"
)

func TestIT050CompatibilityRolloutRequiresConsumersAndDrain(t *testing.T) {
	gate := NewGate(2)
	if err := gate.CanStartV2(Snapshot{CompatibleConsumers: 1}); !errors.Is(err, ErrConsumersNotReady) {
		t.Fatalf("expected consumer gate, got %v", err)
	}
	if got := gate.Phase(Snapshot{CompatibleConsumers: 2}); got != "enable-v2-producers" {
		t.Fatalf("phase=%q", got)
	}
	if err := gate.CanRemoveLegacy(Snapshot{CompatibleConsumers: 2, V2Producers: 1, V1QueueDepth: 1}); !errors.Is(err, ErrLegacyWorkRemaining) {
		t.Fatalf("expected queue drain gate, got %v", err)
	}
	if got := gate.Phase(Snapshot{CompatibleConsumers: 2, V2Producers: 1, V1DurableWork: 1}); got != "drain-v1-work" {
		t.Fatalf("phase=%q", got)
	}
	if got := gate.Phase(Snapshot{CompatibleConsumers: 2, V2Producers: 1}); got != "remove-legacy-handling" {
		t.Fatalf("phase=%q", got)
	}
}

func TestGateReadStatusUsesLiveSnapshot(t *testing.T) {
	status, err := NewGate(1).ReadStatus(context.Background(), SnapshotReaderFunc(func(context.Context) (Snapshot, error) {
		return Snapshot{CompatibleConsumers: 1, V2Producers: 1, V1QueueDepth: 2}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "drain-v1-work" || status.LegacyRemovalAllowed {
		t.Fatalf("unexpected live rollout status: %+v", status)
	}
}
