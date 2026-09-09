package list_events

import (
	"context"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

type Query struct {
	TenantID                identity.ID
	ActorID                 *identity.ID
	Action, TargetID, After string
	From, To                *time.Time
	First                   int
}
type Result struct {
	Events      []database.AuditEvent
	EndCursor   string
	HasNextPage bool
}
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
}

func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil {
		return fmt.Errorf("slice audit/list_events: missing dependency")
	}
	return deps.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) { return handle(ctx, deps, raw.(Query)) })
}
func handle(ctx context.Context, deps Dependencies, q Query) (Result, error) {
	if q.From != nil && q.To != nil && q.From.After(*q.To) {
		return Result{}, apperror.New(apperror.InvalidInput, "dateRange", "invalid date range")
	}
	if _, err := deps.Authorizer.Authorize(ctx, q.TenantID, []string{auth.TenantAdmin, auth.Auditor}, nil, false); err != nil {
		return Result{}, err
	}
	limit, err := graph.PageSize(q.First)
	if err != nil {
		return Result{}, apperror.New(apperror.InvalidInput, "first", "invalid page size")
	}
	var result Result
	err = (tenanttx.Runner{DB: deps.DB}).Within(ctx, q.TenantID, func(tx *gorm.DB) error {
		db := tx.Where("tenant_id = ?", q.TenantID)
		if q.ActorID != nil {
			db = db.Where("actor_id = ?", *q.ActorID)
		}
		if q.Action != "" {
			db = db.Where("action = ?", q.Action)
		}
		if q.TargetID != "" {
			db = db.Where("target_id = ?", q.TargetID)
		}
		if q.From != nil {
			db = db.Where("occurred_at >= ?", *q.From)
		}
		if q.To != nil {
			db = db.Where("occurred_at <= ?", *q.To)
		}
		if q.After != "" {
			c, e := graph.DecodeCursor(q.After)
			if e != nil {
				return e
			}
			at, e := time.Parse(time.RFC3339Nano, c.Time)
			if e != nil {
				return apperror.New(apperror.InvalidInput, "after", "invalid cursor")
			}
			db = db.Where("(occurred_at, id) < (?, ?)", at, c.ID)
		}
		var events []database.AuditEvent
		if err := db.Order("occurred_at DESC, id DESC").Limit(limit + 1).Find(&events).Error; err != nil {
			return apperror.Wrap(apperror.Internal, err)
		}
		result = Result{Events: events}
		if len(events) > limit {
			result.HasNextPage = true
			result.Events = events[:limit]
		}
		if len(result.Events) > 0 {
			last := result.Events[len(result.Events)-1]
			result.EndCursor = graph.EncodeCursor(graph.Cursor{Time: last.OccurredAt.Format(time.RFC3339Nano), ID: last.ID.String()})
		}
		return nil
	})
	return result, err
}
