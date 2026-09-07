package accept_processing

import (
	"context"
	"errors"

	"inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/platform/mediator"
)

type Command struct {
	SessionToken, CSRFToken string
	Consent                 core.ProcessingConsent
}

type Result struct{ Status string }

type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

func Setup(deps Dependencies) error {
	if deps.Bus == nil || deps.Service.DB == nil || len(deps.Service.Pepper) < 32 {
		return errors.New("slice invitations/accept_processing: missing dependency")
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		if err := deps.Service.AcceptProcessing(ctx, command.SessionToken, command.CSRFToken, command.Consent); err != nil {
			return nil, err
		}
		return Result{Status: "ACCEPTED"}, nil
	})
}
