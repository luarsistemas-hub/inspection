// Package contest records a counterparty contestation statement for a baseline version.
package contest

import (
	"context"
	"errors"

	"inspection/services/inspection/internal/features/counterparty/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command contests the baseline bound to the session with a text statement.
type Command struct {
	Owner      core.SessionOwner
	Statement  string
	MutationID string
}

// Dependencies are the collaborators required by this operation.
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

// Setup validates dependencies and registers baseline contestation.
func Setup(deps Dependencies) error {
	if deps.Bus == nil || deps.Service.DB == nil {
		return errors.New("counterparty contest: missing dependency")
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		return deps.Service.Respond(ctx, command.Owner, "CONTESTED", command.Statement, command.MutationID)
	})
}
