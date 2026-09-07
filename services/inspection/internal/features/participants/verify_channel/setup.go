package verify_channel

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/participants/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/mediator"
)

type Command struct {
	TenantID, ContactID identity.ID
	Success             bool
	IdempotencyKey      string
}
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice participants/verify_channel: missing dependency")
	}
	s := core.Service{DB: d.DB, Authorizer: d.Authorizer}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		c := raw.(Command)
		return s.Verify(ctx, c.TenantID, c.ContactID, c.Success, c.IdempotencyKey)
	})
}
