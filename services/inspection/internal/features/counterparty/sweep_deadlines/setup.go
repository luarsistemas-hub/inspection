// Package sweep_deadlines marks pending counterparty invitations past their deadline as NO_RESPONSE.
package sweep_deadlines

import (
	"context"
	"errors"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/counterparty/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command sweeps one tenant at the scheduler tick time.
type Command struct {
	TenantID identity.ID
	Now      time.Time
	Limit    int
}

// Dependencies are the collaborators required by this operation.
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

// Setup validates dependencies and registers the scheduler-owned deadline sweep.
func Setup(deps Dependencies) error {
	if deps.Bus == nil || deps.Service.DB == nil {
		return errors.New("counterparty sweep deadlines: missing dependency")
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		return deps.Service.Sweep(ctx, command.TenantID, command.Now, command.Limit)
	})
}
