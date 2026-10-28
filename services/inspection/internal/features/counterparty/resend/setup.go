// Package resend rotates a counterparty link after a failed delivery and sends it again.
package resend

import (
	"context"
	"errors"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/counterparty/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command resends one pending invitation.
type Command struct{ TenantID, InvitationID identity.ID }

// Dependencies are the collaborators required by this operation.
type Dependencies struct {
	Bus        *mediator.Bus
	Service    core.Service
	Authorizer auth.Authorizer
}

// Setup validates dependencies and registers counterparty invitation resend.
func Setup(deps Dependencies) error {
	if deps.Bus == nil || deps.Service.DB == nil || deps.Service.Notifications == nil || deps.Service.CaptureBaseURL == "" || deps.Authorizer.Store == nil {
		return errors.New("counterparty resend: missing dependency")
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		if err := deps.Service.AuthorizeInvitationAsset(ctx, deps.Authorizer, command.TenantID, command.InvitationID); err != nil {
			return nil, err
		}
		return deps.Service.Resend(ctx, command.TenantID, command.InvitationID)
	})
}
