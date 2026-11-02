// Package regenerate_suggestions registers the explicit regeneration of the
// undecided suggestions of one room of an inventory draft.
package regenerate_suggestions

import (
	"context"
	"fmt"

	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command carries the regeneration request of one room.
type Command struct{ Input core.RegenerateInput }

// Setup registers the regeneration command.
func Setup(bus *mediator.Bus, service core.Service) error {
	if bus == nil {
		return fmt.Errorf("regenerate suggestions: mediator bus is required")
	}
	if service.DB == nil && service.Within == nil {
		return fmt.Errorf("regenerate suggestions: database dependency is required")
	}
	return bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		return service.RegenerateSuggestions(ctx, raw.(Command).Input)
	})
}
