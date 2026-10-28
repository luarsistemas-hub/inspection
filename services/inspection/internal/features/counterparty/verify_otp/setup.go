// Package verify_otp exchanges a counterparty OTP for a COUNTERPARTY external session.
package verify_otp

import (
	"context"
	"errors"

	"inspection/services/inspection/internal/features/counterparty/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command carries the link token and the code typed by the counterparty.
type Command struct{ Token, Code string }

// Dependencies are the collaborators required by this operation.
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

// Setup validates dependencies and registers counterparty OTP verification.
func Setup(deps Dependencies) error {
	if deps.Bus == nil || deps.Service.DB == nil || len(deps.Service.Pepper) < 32 || deps.Service.Limits == nil {
		return errors.New("counterparty verify otp: missing dependency")
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		return deps.Service.VerifyOTP(ctx, command.Token, command.Code)
	})
}
