package decide_items

import (
	"context"
	"fmt"

	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command contains the operation-specific decision input.
type Command struct{ Input core.DecideInput }

// Setup registers the inventory item decision command.
func Setup(bus *mediator.Bus, service core.Service) error {
	if bus == nil {
		return fmt.Errorf("decide items: mediator bus is required")
	}
	if service.DB == nil && service.Within == nil {
		return fmt.Errorf("decide items: database dependency is required")
	}
	return bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		return service.Decide(ctx, raw.(Command).Input)
	})
}
