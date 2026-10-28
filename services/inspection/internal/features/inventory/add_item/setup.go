package add_item

import (
	"context"
	"fmt"

	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/mediator"
)

type Command struct{ Input core.AddItemInput }

// Setup registers the add item command.
func Setup(bus *mediator.Bus, service core.Service) error {
	if bus == nil {
		return fmt.Errorf("add item: mediator bus is required")
	}
	if service.DB == nil && service.Within == nil {
		return fmt.Errorf("add item: database dependency is required")
	}
	return bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		return service.AddItem(ctx, raw.(Command).Input)
	})
}
