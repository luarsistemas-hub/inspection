package expire_request

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/recapture/core"
	"inspection/services/inspection/internal/platform/mediator"
)

type Command struct{ TenantID, RequestID identity.ID }
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

func Setup(d Dependencies) error {
	if d.Bus == nil || d.Service.DB == nil {
		return fmt.Errorf("slice recapture/expire_request: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		return d.Service.Finalize(ctx, command.TenantID, command.RequestID, false)
	})
}
