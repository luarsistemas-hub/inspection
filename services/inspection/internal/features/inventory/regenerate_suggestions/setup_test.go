package regenerate_suggestions

import (
	"context"
	"errors"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/apperror"
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

func TestSetupRejectsInvalidInputBeforePersistenceAndPropagatesDependencyErrors(t *testing.T) {
	dependencyErr := errors.New("inventory persistence unavailable")
	called := 0
	bus := mediator.New()
	if err := Setup(bus, core.Service{Within: func(context.Context, identity.ID, func(*gorm.DB) error) error {
		called++
		return dependencyErr
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Send(context.Background(), Command{Input: core.RegenerateInput{}}); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("empty input error = %v, want INVALID_INPUT", err)
	}
	if called != 0 {
		t.Fatal("invalid input reached persistence")
	}
	input := core.RegenerateInput{TenantID: identity.NewID(), OriginVersionID: identity.NewID(), EnvironmentID: identity.NewID(), ExpectedVersion: 1, RevisionIDs: []identity.ID{identity.NewID()}, ClientMutationID: "m1"}
	if _, err := bus.Send(context.Background(), Command{Input: input}); !errors.Is(err, dependencyErr) {
		t.Fatalf("dependency error = %v, want %v", err, dependencyErr)
	}
	if called != 1 {
		t.Fatalf("handler dispatch count=%d, want 1", called)
	}
}
