package presign_parts

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/media/core"
	"inspection/services/inspection/internal/platform/mediator"
)

type Command struct {
	TenantID, ResponsibilityID, MediaID identity.ID
	Parts                               []int
}
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

func Setup(d Dependencies) error {
	if d.Bus == nil || d.Service.DB == nil || d.Service.Store.Client == nil {
		return fmt.Errorf("slice media/presign_parts: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		return d.Service.Presign(ctx, command.TenantID, command.ResponsibilityID, command.MediaID, command.Parts)
	})
}
