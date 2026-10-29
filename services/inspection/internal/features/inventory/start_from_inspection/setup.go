package start_from_inspection

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command starts an inspection-only draft from any completed inspection.
type Command struct{ TenantID, InspectionID, ActorID identity.ID }

// Setup registers draft creation from an inspection.
func Setup(bus *mediator.Bus, service core.Service) error {
	if bus == nil {
		return fmt.Errorf("start from inspection: mediator bus is required")
	}
	if service.DB == nil && service.Within == nil {
		return fmt.Errorf("start from inspection: database dependency is required")
	}
	return bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		c := raw.(Command)
		return service.StartFromInspection(ctx, c.TenantID, c.InspectionID, c.ActorID)
	})
}
