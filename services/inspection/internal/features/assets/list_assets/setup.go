package list_assets

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/assets/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/mediator"
)

type Query struct {
	TenantID       identity.ID
	BusinessUnitID *identity.ID
	Search         string
	First          int
	After          string
}
type Result struct {
	Assets      []core.View
	EndCursor   string
	HasNextPage bool
}
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice assets/list_assets: missing dependency")
	}
	s := core.Service{DB: d.DB, Bus: d.Bus, Authorizer: d.Authorizer}
	return d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		q := raw.(Query)
		v, e, h, err := s.List(ctx, q.TenantID, q.BusinessUnitID, q.Search, q.First, q.After)
		return Result{Assets: v, EndCursor: e, HasNextPage: h}, err
	})
}
