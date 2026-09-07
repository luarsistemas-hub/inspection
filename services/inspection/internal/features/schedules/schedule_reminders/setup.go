package schedule_reminders

import (
	"context"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/schedules/core"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/gorm"
)

type Command struct {
	TenantID identity.ID
	Now      time.Time
	Limit    int
}
type Dependencies struct {
	DB  *gorm.DB
	Bus *mediator.Bus
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice schedules/schedule_reminders: missing dependency")
	}
	s := core.Service{DB: d.DB, Bus: d.Bus}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		in := raw.(Command)
		return s.DispatchDueReminders(ctx, in.TenantID, in.Now, in.Limit)
	})
}
