package cancel_schedule

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/schedules/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/gorm"
)

type Command struct {
	TenantID, ScheduleID identity.ID
	ExpectedVersion      int64
}
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice schedules/cancel_schedule: missing dependency")
	}
	s := core.Service{DB: d.DB, Bus: d.Bus, Authorizer: d.Authorizer}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		in := raw.(Command)
		return s.Cancel(ctx, in.TenantID, in.ScheduleID, in.ExpectedVersion)
	})
}
