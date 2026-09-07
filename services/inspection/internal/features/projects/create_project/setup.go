package create_project

import (
	"context"
	"fmt"

	"inspection/services/inspection/internal/features/projects/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/gorm"
)

type Command = core.CreateInput
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice projects/create_project: missing dependency")
	}
	s := core.Service{DB: d.DB, Bus: d.Bus, Authorizer: d.Authorizer}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) { return s.Create(ctx, raw.(Command)) })
}
