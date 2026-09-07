package list_participants

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/participants/core"
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
	Participants []core.ParticipantView
	EndCursor    string
	HasNextPage  bool
}
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice participants/list_participants: missing dependency")
	}
	s := core.Service{DB: d.DB, Authorizer: d.Authorizer}
	return d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		q := raw.(Query)
		v, e, h, err := s.List(ctx, q.TenantID, q.Search, q.First, q.After)
		return Result{Participants: v, EndCursor: e, HasNextPage: h}, err
	})
}
