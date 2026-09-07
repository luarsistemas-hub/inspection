package get

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/inspections/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/gorm"
)

type Query struct{ TenantID, InspectionID identity.ID }
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice inspections/get: missing dependency")
	}
	s := core.Service{DB: d.DB, Bus: d.Bus, Authorizer: d.Authorizer}
	return d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		q := raw.(Query)
		return s.Get(ctx, q.TenantID, q.InspectionID)
	})
}
