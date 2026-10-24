package merge_items

import (
	"context"
	"fmt"

	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/mediator"
)

type Command struct{ Input core.MergeItemsInput }

// Setup registers the merge items command.
func Setup(bus *mediator.Bus, service core.Service) error {
	if bus == nil {
		return fmt.Errorf("merge items: mediator bus is required")
	}
	if service.DB == nil && service.Within == nil {
		return fmt.Errorf("merge items: database dependency is required")
	}
	return bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		return service.MergeItems(ctx, raw.(Command).Input)
	})
}
