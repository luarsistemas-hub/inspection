// Package acknowledge records a counterparty acknowledgment of a baseline version.
package acknowledge

import (
	"context"
	"errors"

	"inspection/services/inspection/internal/features/counterparty/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command acknowledges the baseline bound to the session.
type Command struct {
	Owner      core.SessionOwner
	MutationID string
}

// Dependencies are the collaborators required by this operation.
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

// Setup validates dependencies and registers baseline acknowledgment.
func Setup(deps Dependencies) error {
	if deps.Bus == nil || deps.Service.DB == nil {
		return errors.New("counterparty acknowledge: missing dependency")
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		return deps.Service.Respond(ctx, command.Owner, "ACKNOWLEDGED", "", command.MutationID)
	})
}
