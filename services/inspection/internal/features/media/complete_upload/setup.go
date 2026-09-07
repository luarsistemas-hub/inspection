package complete_upload

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/media/core"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
)

type Command struct {
	TenantID, ResponsibilityID, MediaID identity.ID
	Parts                               []objectstore.Part
}
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

func Setup(d Dependencies) error {
	if d.Bus == nil || d.Service.DB == nil || d.Service.Store.Client == nil {
		return fmt.Errorf("slice media/complete_upload: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		return d.Service.Complete(ctx, command.TenantID, command.ResponsibilityID, command.MediaID, command.Parts)
	})
}
