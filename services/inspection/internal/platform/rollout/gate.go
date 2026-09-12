// Package rollout contains the compatibility-window checks used by deployment
// tooling. It intentionally has no database or broker client so the same
// decision can be tested locally and evaluated by an operator.
package rollout

import (
	"context"
	"errors"
	"fmt"
)

var ErrLegacyWorkRemaining = errors.New("rollout: legacy v1 work remains")
var ErrConsumersNotReady = errors.New("rollout: compatible consumers are not ready")

// Snapshot is the redacted state needed to decide whether a rollout step is
// safe. Queue and durable-work counts are v1-only counts.
type Snapshot struct {
	CompatibleConsumers int
	V2Producers         int
	V1QueueDepth        int
	V1DurableWork       int
}

// Gate evaluates ordering and drain requirements for the compatibility window.
type Gate struct {
	RequiredConsumers int
}

// SnapshotReader obtains rollout state from the live deployment.
type SnapshotReader interface {
	ReadSnapshot(context.Context) (Snapshot, error)
}

// SnapshotReaderFunc adapts a function into a SnapshotReader.
type SnapshotReaderFunc func(context.Context) (Snapshot, error)

// ReadSnapshot implements SnapshotReader.
func (f SnapshotReaderFunc) ReadSnapshot(ctx context.Context) (Snapshot, error) {
	return f(ctx)
}

// Status is the redacted rollout decision exposed to operational tooling.
type Status struct {
	Snapshot             Snapshot
	Phase                string
	LegacyRemovalAllowed bool
}

// ReadStatus reads live state and evaluates the rollout gate.
func (g Gate) ReadStatus(ctx context.Context, reader SnapshotReader) (Status, error) {
	if reader == nil {
		return Status{}, errors.New("rollout: missing snapshot reader")
	}
	snapshot, err := reader.ReadSnapshot(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("read rollout snapshot: %w", err)
	}
	return Status{
		Snapshot:             snapshot,
		Phase:                g.Phase(snapshot),
		LegacyRemovalAllowed: g.CanRemoveLegacy(snapshot) == nil,
	}, nil
}

// NewGate returns a gate that requires at least one compatible consumer unless
// a larger deployment-specific count is provided.
func NewGate(requiredConsumers int) Gate {
	if requiredConsumers < 1 {
		requiredConsumers = 1
	}
	return Gate{RequiredConsumers: requiredConsumers}
}

// CanStartV2 reports whether compatible consumers are deployed before v2
// producers are enabled.
func (g Gate) CanStartV2(snapshot Snapshot) error {
	if snapshot.CompatibleConsumers < g.RequiredConsumers {
		return fmt.Errorf("%w: have %d, need %d", ErrConsumersNotReady, snapshot.CompatibleConsumers, g.RequiredConsumers)
	}
	return nil
}

// CanRemoveLegacy reports whether all v1 queues and durable work are drained.
func (g Gate) CanRemoveLegacy(snapshot Snapshot) error {
	if snapshot.V1QueueDepth > 0 || snapshot.V1DurableWork > 0 {
		return fmt.Errorf("%w: queue=%d durable=%d", ErrLegacyWorkRemaining, snapshot.V1QueueDepth, snapshot.V1DurableWork)
	}
	return nil
}

// Phase returns the next safe deployment phase.
func (g Gate) Phase(snapshot Snapshot) string {
	if g.CanStartV2(snapshot) != nil {
		return "deploy-compatible-consumers"
	}
	if snapshot.V2Producers == 0 {
		return "enable-v2-producers"
	}
	if g.CanRemoveLegacy(snapshot) != nil {
		return "drain-v1-work"
	}
	return "remove-legacy-handling"
}
