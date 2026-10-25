package request_cataloging

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command starts legacy-origin cataloging for an asset.
type Command struct{ TenantID, AssetID, ActorID identity.ID }

// Result is the opened draft and whether the legacy video source has expired,
// in which case cataloging uses the retained frames only.
type Result struct {
	Version        database.OriginVersion
	SourcesExpired bool
}

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
		version, err := service.RequestOriginCataloging(ctx, c.TenantID, c.AssetID, c.ActorID)
		if err != nil {
			return nil, err
		}
		expired, err := service.LegacySourcesExpired(ctx, c.TenantID, version.ID)
		if err != nil {
			return nil, err
		}
		return Result{Version: version, SourcesExpired: expired}, nil
	})
}
