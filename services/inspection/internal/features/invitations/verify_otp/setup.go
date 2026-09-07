package verify_otp

import (
	"context"
	"errors"

	"inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/platform/mediator"
)

type Command struct{ LinkToken, Code, ClientMutationID string }
type Result = core.Session
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

func Setup(deps Dependencies) error {
	if deps.Bus == nil || deps.Service.DB == nil || deps.Service.Limits == nil || len(deps.Service.Pepper) < 32 {
		return errors.New("verify invitation otp: missing dependency")
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		return deps.Service.VerifyOTP(ctx, command.LinkToken, command.Code)
	})
}
