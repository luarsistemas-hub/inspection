package create_upload

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/media/core"
	"inspection/services/inspection/internal/platform/mediator"
)

type Command struct {
	TenantID, ResponsibilityID          identity.ID
	ContentType, SHA256, IdempotencyKey string
	SizeBytes                           int64
}
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

func Setup(d Dependencies) error {
	if d.Bus == nil || d.Service.DB == nil || d.Service.Store.Client == nil {
		return fmt.Errorf("slice media/create_upload: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		return d.Service.CreateWithKey(ctx, command.TenantID, command.ResponsibilityID, command.ContentType, command.SHA256, command.SizeBytes, command.IdempotencyKey)
	})
}
