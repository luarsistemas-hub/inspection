package activate_version

import (
	"context"
	"fmt"

	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command contains the operation-specific activation input.
type Command struct{ Input core.ActivateInput }

// Setup registers baseline activation.
func Setup(bus *mediator.Bus, service core.Service) error {
	if bus == nil {
		return fmt.Errorf("activate version: mediator bus is required")
	}
	if service.DB == nil && service.Within == nil {
		return fmt.Errorf("activate version: database dependency is required")
	}
	return bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		return service.Activate(ctx, raw.(Command).Input)
	})
}
