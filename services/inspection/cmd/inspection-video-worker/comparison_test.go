package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

type testVideoPermit struct{ held chan struct{} }

func (p testVideoPermit) WithPermit(ctx context.Context, run func(context.Context) (bool, error)) (bool, error) {
	select {
	case p.held <- struct{}{}:
		defer func() { <-p.held }()
		return run(ctx)
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

type testVideoDelivery struct{ called *bool }

func (d testVideoDelivery) Process(context.Context, []byte, int) (bool, error) {
	*d.called = true
	return true, nil
}

func TestVideoConditionDeliveryWaitsForPreparationPermitUT077(t *testing.T) {
	permit := testVideoPermit{held: make(chan struct{}, 1)}
	permit.held <- struct{}{}
	called := false
	callback := permittedDelivery{permit: permit, inner: testVideoDelivery{called: &called}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	processed, err := callback.Process(ctx, []byte("event"), 0)
	if processed || called || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("condition delivery ran under occupied permit: processed=%t called=%t err=%v", processed, called, err)
	}
	<-permit.held
	processed, err = callback.Process(context.Background(), []byte("event"), 0)
	if !processed || !called || err != nil {
		t.Fatalf("condition delivery did not resume after release: processed=%t called=%t err=%v", processed, called, err)
	}
}
