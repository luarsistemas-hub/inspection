// Package retry_draft registers retry of failed inventory catalog drafts.
package retry_draft

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command reopens a failed inventory draft and resets its failed jobs.
type Command struct{ TenantID, VersionID, ActorID identity.ID }

// Setup registers the retry command.
func Setup(bus *mediator.Bus, service core.Service) error {
	if bus == nil {
		return fmt.Errorf("retry inventory draft: mediator bus is required")
	}
	if service.DB == nil && service.Within == nil {
		return fmt.Errorf("retry inventory draft: database dependency is required")
	}
	return bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		return service.RetryDraft(ctx, command.TenantID, command.VersionID, command.ActorID)
	})
}
