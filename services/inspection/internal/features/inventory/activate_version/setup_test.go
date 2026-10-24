package activate_version

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

func TestSetupDispatchesHappyPathAndReturnsValidationAndDependencyErrors(t *testing.T) {
	input := core.ActivateInput{TenantID: identity.NewID(), OriginVersionID: identity.NewID(), Purpose: "MOVE_IN", ExpectedOriginVersion: 1}
	happyBus := mediator.New()
	if err := Setup(happyBus, core.Service{Within: func(context.Context, identity.ID, func(*gorm.DB) error) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	if _, err := happyBus.Send(context.Background(), Command{Input: input}); err != nil {
		t.Fatalf("activation command did not dispatch: %v", err)
	}
	if _, err := happyBus.Send(context.Background(), Command{Input: core.ActivateInput{}}); err == nil {
		t.Fatal("missing expected origin version was accepted")
	} else if code, field, _ := apperror.Public(err); code != apperror.InvalidInput || field != "expectedOriginVersion" {
		t.Fatalf("invalid activation error=(%s,%s), want (InvalidInput,expectedOriginVersion)", code, field)
	}
	dependencyErr := errors.New("inventory database unavailable")
	failingBus := mediator.New()
	if err := Setup(failingBus, core.Service{Within: func(context.Context, identity.ID, func(*gorm.DB) error) error { return dependencyErr }}); err != nil {
		t.Fatal(err)
	}
	if _, err := failingBus.Send(context.Background(), Command{Input: input}); !errors.Is(err, dependencyErr) {
		t.Fatalf("dependency error=%v, want %v", err, dependencyErr)
	}
}
