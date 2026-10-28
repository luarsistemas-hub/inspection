package discard_draft

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command identifies the draft and reviewer performing the discard.
type Command struct{ TenantID, VersionID, ActorID identity.ID }

// Setup registers draft discard.
func Setup(bus *mediator.Bus, service core.Service) error {
	if bus == nil {
		return fmt.Errorf("discard draft: mediator bus is required")
	}
	if service.DB == nil && service.Within == nil {
		return fmt.Errorf("discard draft: database dependency is required")
	}
	return bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		c := raw.(Command)
		return service.Discard(ctx, c.TenantID, c.VersionID, c.ActorID)
	})
}
