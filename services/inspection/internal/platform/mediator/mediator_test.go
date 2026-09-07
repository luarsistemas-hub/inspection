package mediator

import (
	"context"
	"errors"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/requestctx"
	"testing"
	"time"
)

type testCommand struct{}

func TestMediatorContractsUT003UT004(t *testing.T) {
	b := New()
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	tenant := identity.NewID()
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: tenant, CorrelationID: "corr"})
	called := false
	if err := b.RegisterCommand(testCommand{}, func(got context.Context, _ any) (any, error) {
		called = true
		m, ok := requestctx.FromContext(got)
		if !ok || m.TenantID != tenant || m.CorrelationID != "corr" {
			t.Fatal("context metadata lost")
		}
		if d, ok := got.Deadline(); !ok || !d.Equal(deadline) {
			t.Fatal("deadline lost")
		}
		return "ok", nil
	}); err != nil {
		t.Fatal(err)
	}
	v, err := b.Send(ctx, testCommand{})
	if err != nil || v != "ok" || !called {
		t.Fatalf("dispatch=%v %v", v, err)
	}
	if err := b.RegisterCommand(testCommand{}, func(context.Context, any) (any, error) { return nil, nil }); err == nil {
		t.Fatal("expected duplicate")
	}
	if _, err := b.Ask(ctx, testCommand{}); err == nil {
		t.Fatal("expected missing query")
	}
	canceled, cancelNow := context.WithCancel(ctx)
	cancelNow()
	if _, err := b.Send(canceled, testCommand{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
}
