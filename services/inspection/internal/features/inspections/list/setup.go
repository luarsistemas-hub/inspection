package list

import (
	"context"
	"fmt"

	"inspection/services/inspection/internal/features/inspections/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/gorm"
)

type Query = core.ListInput
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice inspections/list: missing dependency")
	}
	s := core.Service{DB: d.DB, Bus: d.Bus, Authorizer: d.Authorizer}
	return d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) { return s.List(ctx, raw.(Query)) })
}
