package submit

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/mediator"
)

type Command struct {
	TenantID, ResponsibilityID identity.ID
	ConfirmIncomplete          bool
}
type Dependencies struct {
	Bus              *mediator.Bus
	Service          core.Service
	AnalysisDisabled bool
}

func Setup(d Dependencies) error {
	if d.Bus == nil || d.Service.DB == nil || d.Service.Finalizer == nil {
		return fmt.Errorf("slice capture/submit: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		if d.AnalysisDisabled {
			return nil, apperror.New(apperror.IntegrationDisabled, "", "Analysis is temporarily unavailable")
		}
		command := raw.(Command)
		return d.Service.Submit(ctx, command.TenantID, command.ResponsibilityID, command.ConfirmIncomplete)
	})
}
