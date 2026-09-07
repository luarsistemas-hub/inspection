//go:build integration

package harness

import (
	"context"
	"testing"
)

// TestTask06HarnessSmoke is the first gate for the external IT-241..IT-586
// suite. It intentionally performs no business mutation: its job is to prove
// the configured database, broker, and provider stubs are reachable before
// the fixture-heavy suites start publishing events.
func TestTask06HarnessSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), ConfigFromEnv().RequestTimeout)
	defer cancel()
	h, err := NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	if err := h.ProbeProviders(ctx); err != nil {
		t.Fatal(err)
	}
}
