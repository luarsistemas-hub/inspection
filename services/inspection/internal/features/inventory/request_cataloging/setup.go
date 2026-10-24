package request_cataloging

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command starts legacy-origin cataloging for an asset.
type Command struct{ TenantID, AssetID, ActorID identity.ID }

// Setup registers legacy-origin cataloging.
func Setup(bus *mediator.Bus, service core.Service) error {
	if bus == nil {
		return fmt.Errorf("request cataloging: mediator bus is required")
	}
	if service.DB == nil && service.Within == nil {
		return fmt.Errorf("request cataloging: database dependency is required")
	}
	return bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		c := raw.(Command)
		return service.RequestOriginCataloging(ctx, c.TenantID, c.AssetID, c.ActorID)
	})
}
