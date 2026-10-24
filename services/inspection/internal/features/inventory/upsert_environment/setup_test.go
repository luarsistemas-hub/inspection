package upsert_environment

import (
	"context"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/mediator"
	"testing"

	"gorm.io/gorm"
)

func TestSetupValidationAndDispatch(t *testing.T) {
	if Setup(nil, core.Service{}) == nil || Setup(mediator.New(), core.Service{}) == nil {
		t.Fatal("Setup accepted missing dependencies")
	}
	bus := mediator.New()
	service := core.Service{Within: func(context.Context, identity.ID, func(*gorm.DB) error) error { return nil }}
	if err := Setup(bus, service); err != nil {
		t.Fatal(err)
	}
	_, err := bus.Send(context.Background(), Command{Input: core.UpsertEnvironmentInput{TypeCode: "OTHER", Name: "Room"}})
	if err != nil {
		t.Fatalf("command dispatch failed: %v", err)
	}
}
