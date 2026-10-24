package remove_environment

import (
	"context"
	"fmt"

	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command contains the operation-specific environment removal input.
type Command struct{ Input core.RemoveEnvironmentInput }

// Setup registers the remove environment command.
func Setup(bus *mediator.Bus, service core.Service) error {
	if bus == nil {
		return fmt.Errorf("remove environment: mediator bus is required")
	}
	if service.DB == nil && service.Within == nil {
		return fmt.Errorf("remove environment: database dependency is required")
	}
	return bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		return nil, service.RemoveEnvironment(ctx, raw.(Command).Input)
	})
}
