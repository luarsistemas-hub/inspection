package draft

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Query identifies a draft to load for internal review.
type Query struct{ TenantID, VersionID identity.ID }

// Setup registers the internal inventory draft query.
func Setup(bus *mediator.Bus, service core.Service) error {
	if bus == nil {
		return fmt.Errorf("inventory draft: mediator bus is required")
	}
	if service.DB == nil && service.Within == nil {
		return fmt.Errorf("inventory draft: database dependency is required")
	}
	return bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		q := raw.(Query)
		return service.InventoryDraft(ctx, q.TenantID, q.VersionID)
	})
}
