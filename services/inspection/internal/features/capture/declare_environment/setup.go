// Package declare_environment registers the external capture environment declaration command.
package declare_environment

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/mediator"
)

type Command struct {
	TenantID, ResponsibilityID identity.ID
	TypeCode, Name             string
	ClientMutationID           string
}
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

func Setup(d Dependencies) error {
	if d.Bus == nil || d.Service.DB == nil {
		return fmt.Errorf("slice capture/declare_environment: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		cmd := raw.(Command)
		return d.Service.DeclareEnvironment(ctx, core.DeclareEnvironmentInput{TenantID: cmd.TenantID, ResponsibilityID: cmd.ResponsibilityID, TypeCode: cmd.TypeCode, Name: cmd.Name, ClientMutationID: cmd.ClientMutationID})
	})
}
