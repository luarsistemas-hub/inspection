package session

import (
	"context"
	"errors"

	"inspection/services/inspection/internal/platform/mediator"
)

type RequestOTPCommand struct{ Name, Email, IP string }
type VerifyOTPCommand struct{ Locator, Code string }
type LoadQuery struct{ Locator string }
type CheckpointCommand struct {
	Locator, CSRF, Step string
	ExpectedVersion     int64
	Payload             map[string]any
}

type Dependencies struct {
	Bus     *mediator.Bus
	Service Service
}

func Setup(deps Dependencies) error {
	if deps.Bus == nil {
		return errors.New("onboarding session: missing bus")
	}
	if err := deps.Service.validate(); err != nil {
		return err
	}
	if err := deps.Bus.RegisterCommand(RequestOTPCommand{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(RequestOTPCommand)
		return deps.Service.RequestOTP(ctx, command.Name, command.Email, command.IP)
	}); err != nil {
		return err
	}
	if err := deps.Bus.RegisterCommand(VerifyOTPCommand{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(VerifyOTPCommand)
		return deps.Service.VerifyOTP(ctx, command.Locator, command.Code)
	}); err != nil {
		return err
	}
	if err := deps.Bus.RegisterQuery(LoadQuery{}, func(ctx context.Context, raw any) (any, error) {
		return deps.Service.Load(ctx, raw.(LoadQuery).Locator)
	}); err != nil {
		return err
	}
	return deps.Bus.RegisterCommand(CheckpointCommand{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(CheckpointCommand)
		return deps.Service.Checkpoint(ctx, command.Locator, command.CSRF, command.Step, command.ExpectedVersion, command.Payload)
	})
}
