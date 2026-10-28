package discard_draft

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

func TestSetupDispatchesHappyPathAndPropagatesInputAndDependencyFailures(t *testing.T) {
	within := func(context.Context, identity.ID, func(*gorm.DB) error) error { return nil }
	bus := mediator.New()
	if err := Setup(bus, core.Service{Within: within}); err != nil {
		t.Fatal(err)
	}
	input := Command{TenantID: identity.NewID(), VersionID: identity.NewID(), ActorID: identity.NewID()}
	if _, err := bus.Send(context.Background(), input); err != nil {
		t.Fatalf("discard command did not dispatch: %v", err)
	}
	if _, err := bus.Send(context.Background(), Command{}); err == nil {
		t.Fatal("empty tenant input was accepted")
	} else if code, field, _ := apperror.Public(err); code != apperror.InvalidInput || field != "originVersionId" {
		t.Fatalf("invalid input error=(%s,%s), want (InvalidInput,originVersionId)", code, field)
	}
	dependencyErr := errors.New("inventory database unavailable")
	failingBus := mediator.New()
	if err := Setup(failingBus, core.Service{Within: func(context.Context, identity.ID, func(*gorm.DB) error) error { return dependencyErr }}); err != nil {
		t.Fatal(err)
	}
	if _, err := failingBus.Send(context.Background(), input); !errors.Is(err, dependencyErr) {
		t.Fatalf("dependency error=%v, want %v", err, dependencyErr)
	}
}
