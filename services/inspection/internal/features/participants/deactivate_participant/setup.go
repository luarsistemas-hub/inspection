package deactivate_participant

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
	TenantID, ParticipantID identity.ID
	ExpectedVersion         int64
}
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice participants/deactivate_participant: missing dependency")
	}
	s := core.Service{DB: d.DB, Authorizer: d.Authorizer}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		c := raw.(Command)
		return nil, s.Deactivate(ctx, c.TenantID, c.ParticipantID, c.ExpectedVersion)
	})
}
