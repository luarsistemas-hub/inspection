package revoke_invitation

import (
	"context"
	"errors"

	"inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/platform/mediator"
)

type Command struct{ LinkToken, ClientMutationID string }
type Result struct{ Status string }
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

func Setup(deps Dependencies) error {
	if deps.Bus == nil || deps.Service.DB == nil {
		return errors.New("revoke invitation: missing dependency")
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		if err := deps.Service.Revoke(ctx, command.LinkToken); err != nil {
			return nil, err
		}
		return Result{Status: "REVOKED"}, nil
	})
}
