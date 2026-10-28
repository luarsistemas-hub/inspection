package declare_impossibility

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/mediator"
)

type Command struct {
	TenantID, ResponsibilityID identity.ID
	RequirementKey, Reason     string
	ReasonCode, ReasonText     string
	ExpectedVersion            int64
}
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

func Setup(d Dependencies) error {
	if d.Bus == nil || d.Service.DB == nil {
		return fmt.Errorf("slice capture/declare_impossibility: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		if command.ReasonCode != "" {
			return true, d.Service.DeclareImpossibilityWithCode(ctx, command.TenantID, command.ResponsibilityID, command.RequirementKey, command.ReasonCode, command.ReasonText, command.ExpectedVersion)
		}
		return true, d.Service.DeclareImpossibility(ctx, command.TenantID, command.ResponsibilityID, command.RequirementKey, command.Reason, command.ExpectedVersion)
	})
}
