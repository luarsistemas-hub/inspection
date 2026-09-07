package resolve_template

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/templates/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/gorm"
)

type Query struct{ TenantID, TemplateID identity.ID }
type Result struct {
	Template database.Template
	Version  database.TemplateVersion
}
type Dependencies struct {
	DB  *gorm.DB
	Bus *mediator.Bus
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice templates/resolve_template: missing dependency")
	}
	s := core.Service{DB: d.DB, Bus: d.Bus}
	return d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		q := raw.(Query)
		view, err := s.ResolveActive(ctx, q.TenantID, q.TemplateID)
		return Result{Template: view.Template, Version: view.Version}, err
	})
}
