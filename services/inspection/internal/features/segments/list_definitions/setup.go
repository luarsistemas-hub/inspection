package list_definitions

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/segments/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/mediator"
)

type Query struct {
	TenantID identity.ID
	Search   string
	First    int
	After    string
}
type Result struct {
	Definitions []core.View
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
		return fmt.Errorf("slice segments/list_definitions: missing dependency")
	}
	s := core.Service{DB: d.DB, Authorizer: d.Authorizer}
	return d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		q := raw.(Query)
		v, e, h, err := s.List(ctx, q.TenantID, q.Search, q.First, q.After)
		return Result{Definitions: v, EndCursor: e, HasNextPage: h}, err
	})
}
