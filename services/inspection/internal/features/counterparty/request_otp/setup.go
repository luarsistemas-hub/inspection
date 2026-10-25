// Package request_otp sends a one-time code to a counterparty link owner.
package request_otp

import (
	"context"
	"errors"

	"inspection/services/inspection/internal/features/counterparty/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command carries the secret link token from the external request.
type Command struct{ Token string }

// Result reports that the code was sent.
type Result struct{ Status string }

// Dependencies are the collaborators required by this operation.
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

// Setup validates dependencies and registers the counterparty OTP request.
func Setup(deps Dependencies) error {
	if deps.Bus == nil || deps.Service.DB == nil || len(deps.Service.Pepper) < 32 || deps.Service.Limits == nil || deps.Service.Notifier == nil {
		return errors.New("counterparty request otp: missing dependency")
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		if err := deps.Service.RequestOTP(ctx, raw.(Command).Token); err != nil {
			return nil, err
		}
		return Result{Status: "SENT"}, nil
	})
}
