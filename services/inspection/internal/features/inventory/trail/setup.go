package trail

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Query identifies the version trail requested by an authorized reader.
type Query struct{ TenantID, VersionID identity.ID }

// Setup registers the inventory decision trail query.
func Setup(bus *mediator.Bus, service core.Service) error {
	if bus == nil {
		return fmt.Errorf("inventory trail: mediator bus is required")
	}
	if service.DB == nil && service.Within == nil {
		return fmt.Errorf("inventory trail: database dependency is required")
	}
	return bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		q := raw.(Query)
		return service.InventoryTrail(ctx, q.TenantID, q.VersionID)
	})
}
