package baseline

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Query identifies the requested asset projection.
type Query struct {
	TenantID, AssetID identity.ID
	VersionID         *identity.ID
	Customer          bool
}

// Setup registers the baseline inventory query.
func Setup(bus *mediator.Bus, service core.Service) error {
	if bus == nil {
		return fmt.Errorf("baseline inventory: mediator bus is required")
	}
	if service.DB == nil && service.Within == nil {
		return fmt.Errorf("baseline inventory: database dependency is required")
	}
	return bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		q := raw.(Query)
		return service.BaselineInventory(ctx, q.TenantID, q.AssetID, q.VersionID, q.Customer)
	})
}
