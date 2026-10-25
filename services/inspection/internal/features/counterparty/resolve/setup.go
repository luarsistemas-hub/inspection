// Package resolve lets internal users decline or accept an open contestation.
package resolve

import (
	"context"
	"errors"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/counterparty/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command resolves one contested invitation.
type Command struct {
	TenantID, InvitationID, ActorID identity.ID
	Outcome, Reason                 string
}

// Dependencies are the collaborators required by this operation.
type Dependencies struct {
	Bus        *mediator.Bus
	Service    core.Service
	Authorizer auth.Authorizer
}

// Setup validates dependencies and registers contestation resolution.
func Setup(deps Dependencies) error {
	if deps.Bus == nil || deps.Service.DB == nil || deps.Authorizer.Store == nil {
		return errors.New("counterparty resolve: missing dependency")
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		if err := deps.Service.AuthorizeInvitationAsset(ctx, deps.Authorizer, command.TenantID, command.InvitationID); err != nil {
			return nil, err
		}
		return deps.Service.Resolve(ctx, command.TenantID, command.InvitationID, command.ActorID, command.Outcome, command.Reason)
	})
}
