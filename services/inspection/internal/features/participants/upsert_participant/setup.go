package upsert_participant

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
	TenantID, BusinessUnitID          identity.ID
	ParticipantID                     *identity.ID
	ExpectedVersion                   int64
	Name, SegmentRole, IdempotencyKey string
	Contacts                          []core.ContactInput
}
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice participants/upsert_participant: missing dependency")
	}
	s := core.Service{DB: d.DB, Authorizer: d.Authorizer}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		c := raw.(Command)
		return s.Upsert(ctx, c.TenantID, c.BusinessUnitID, c.ParticipantID, c.ExpectedVersion, c.Name, c.SegmentRole, c.IdempotencyKey, c.Contacts)
	})
}
