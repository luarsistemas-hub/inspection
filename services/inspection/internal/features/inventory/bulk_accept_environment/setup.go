package bulk_accept_environment

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command contains the environment and confidence threshold for bulk acceptance.
type Command struct {
	TenantID, VersionID, EnvironmentID, ActorID identity.ID
	Threshold                                   int
}

// Setup registers the bulk accept command.
func Setup(bus *mediator.Bus, service core.Service) error {
	if bus == nil {
		return fmt.Errorf("bulk accept environment: mediator bus is required")
	}
	if service.DB == nil && service.Within == nil {
		return fmt.Errorf("bulk accept environment: database dependency is required")
	}
	return bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		c := raw.(Command)
		return service.BulkAccept(ctx, c.TenantID, c.VersionID, c.EnvironmentID, c.ActorID, c.Threshold)
	})
}
