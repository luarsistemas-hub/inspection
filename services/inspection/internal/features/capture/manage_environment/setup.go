// Package manage_environment registers participant changes to added video environments.
package manage_environment

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command renames an environment added during a video capture or removes it.
type Command struct {
	TenantID, ResponsibilityID identity.ID
	RequirementKey, Name       string
	Remove                     bool
}

// Dependencies are the external collaborators used by this slice.
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

// Setup validates dependencies and registers the environment management command.
func Setup(d Dependencies) error {
	if d.Bus == nil || d.Service.DB == nil {
		return fmt.Errorf("slice capture/manage_environment: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		cmd := raw.(Command)
		return d.Service.ManageEnvironment(ctx, core.ManageEnvironmentInput{TenantID: cmd.TenantID, ResponsibilityID: cmd.ResponsibilityID, RequirementKey: cmd.RequirementKey, Name: cmd.Name, Remove: cmd.Remove})
	})
}
