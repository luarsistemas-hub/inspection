package decide_items

import (
	"context"
	"errors"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/gorm"
)

func TestSetupValidatesDependencies(t *testing.T) {
	if Setup(nil, core.Service{DB: &gorm.DB{}}) == nil {
		t.Fatal("Setup accepted a nil bus")
	}
	if Setup(mediator.New(), core.Service{}) == nil {
		t.Fatal("Setup accepted missing database dependencies")
	}
	if err := Setup(mediator.New(), core.Service{DB: &gorm.DB{}}); err != nil {
		t.Fatalf("Setup rejected valid dependencies: %v", err)
	}
}

func TestSetupDispatchesAndPropagatesValidationAndDependencyErrors(t *testing.T) {
	dependencyErr := errors.New("inventory persistence unavailable")
	called := 0
	service := core.Service{Within: func(context.Context, identity.ID, func(*gorm.DB) error) error {
		called++
		return dependencyErr
	}}
	bus := mediator.New()
	if err := Setup(bus, service); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Send(context.Background(), Command{Input: core.DecideInput{}}); err == nil {
		t.Fatal("empty decision input was accepted")
	}
	if called != 0 {
		t.Fatal("invalid input reached persistence")
	}
	input := core.DecideInput{TenantID: identity.NewID(), OriginVersionID: identity.NewID(), Decisions: []core.Decision{{ItemID: identity.NewID(), Action: "ACCEPT", ExpectedVersion: 1}}}
	if _, err := bus.Send(context.Background(), Command{Input: input}); !errors.Is(err, dependencyErr) {
		t.Fatalf("dependency error = %v, want %v", err, dependencyErr)
	}
	if called != 1 {
		t.Fatalf("handler dispatch count=%d, want 1", called)
	}
	happyBus := mediator.New()
	if err := Setup(happyBus, core.Service{Within: func(context.Context, identity.ID, func(*gorm.DB) error) error { return nil }}); err != nil {
		t.Fatalf("Setup rejected valid Within dependency: %v", err)
	}
	if _, err := happyBus.Send(context.Background(), Command{Input: input}); err != nil {
		t.Fatalf("registered command did not complete its happy path: %v", err)
	}
}
