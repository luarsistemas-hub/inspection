package upsert_environment

import (
	"context"
	"fmt"

	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/mediator"
)

type Command struct{ Input core.UpsertEnvironmentInput }

// Setup registers the upsert environment command.
func Setup(bus *mediator.Bus, service core.Service) error {
	if bus == nil {
		return fmt.Errorf("upsert environment: mediator bus is required")
	}
	if service.DB == nil && service.Within == nil {
		return fmt.Errorf("upsert environment: database dependency is required")
	}
	return bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		return service.UpsertEnvironment(ctx, raw.(Command).Input)
	})
}
