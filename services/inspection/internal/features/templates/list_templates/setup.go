package list_templates

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/templates/core"
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
	Templates   []core.View
	EndCursor   string
	HasNextPage bool
}
type VersionQuery struct{ TenantID, VersionID identity.ID }
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice templates/list_templates: missing dependency")
	}
	s := core.Service{DB: d.DB, Bus: d.Bus, Authorizer: d.Authorizer}
	if err := d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		q := raw.(Query)
		v, e, h, err := s.List(ctx, q.TenantID, q.Search, q.First, q.After)
		return Result{Templates: v, EndCursor: e, HasNextPage: h}, err
	}); err != nil {
		return err
	}
	return d.Bus.RegisterQuery(VersionQuery{}, func(ctx context.Context, raw any) (any, error) {
		q := raw.(VersionQuery)
		return s.GetVersion(ctx, q.TenantID, q.VersionID)
	})
}
