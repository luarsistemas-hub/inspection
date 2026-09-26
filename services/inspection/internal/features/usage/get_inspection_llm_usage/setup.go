// Package get_inspection_llm_usage exposes the durable per-call LLM ledger.
package get_inspection_llm_usage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// Query identifies one inspection and one execution mode in the durable LLM
// ledger. The cursor is ordered by started_at DESC, call_id DESC.
type Query struct {
	TenantID, InspectionID identity.ID
	Mode                   string
	First                  int
	After                  string
}

// Result is the inspection-level ledger projection returned to GraphQL.
type Result struct {
	InspectionID      identity.ID
	Mode              string
	AttemptedCalls    int
	DeliveredCalls    int
	IncompleteCalls   int
	InputTokens       int64
	OutputTokens      int64
	KnownReportedCost float64
	UnknownCostCalls  int
	CachedInputTokens int64
	CacheHitCalls     int
	KnownCacheCalls   int
	UnknownCacheCalls int
	CostComplete      bool
	CoverageStartedAt *time.Time
	CoverageComplete  bool
	Calls             []Call
	EndCursor         string
	HasNextPage       bool
}

// Call is a safe, non-content projection of one LLM call.
type Call struct {
	CallID             identity.ID
	JobID              identity.ID
	EventID            identity.ID
	ExecutionID        identity.ID
	CorrelationID      string
	Attempt            int
	ReplayGeneration   int
	Mode               string
	ComparisonMode     string
	ModelAlias         string
	Provider           string
	Model              string
	GatewayRequestID   string
	State              string
	TechnicalOutcome   string
	TransportDelivered *bool
	HTTPStatus         *int
	InputTokens        *int64
	OutputTokens       *int64
	CachedInputTokens  *int64
	ImageCount         *int
	RequestBodyBytes   *int64
	ReportedCost       *float64
	DurationMS         *int64
	StartedAt          time.Time
	FinishedAt         *time.Time
}

// Dependencies are the dependencies of the usage query slice.
type Dependencies struct {
	DB  *gorm.DB
	Bus *mediator.Bus
}

// Setup registers the inspection LLM usage query in the mediator.
func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil {
		return fmt.Errorf("slice usage/get_inspection_llm_usage: missing dependency")
	}
	return deps.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		return handle(ctx, deps, raw.(Query))
	})
}

func handle(ctx context.Context, deps Dependencies, q Query) (Result, error) {
	if q.TenantID == (identity.ID{}) || q.InspectionID == (identity.ID{}) {
		return Result{}, apperror.New(apperror.InvalidInput, "inspectionId", "invalid inspection")
	}
	mode := strings.ToLower(q.Mode)
	if mode == "" {
		mode = "live"
	}
	if mode != "live" && mode != "mock" {
		return Result{}, apperror.New(apperror.InvalidInput, "mode", "invalid execution mode")
	}
	limit, err := graph.PageSize(q.First)
	if err != nil {
		return Result{}, apperror.New(apperror.InvalidInput, "first", "invalid page size")
	}

	var result Result
	err = withTenant(ctx, deps.DB, q.TenantID, func(tx *gorm.DB) error {
		var inspection database.Inspection
		if err := tx.Select("id", "created_at").Where("tenant_id = ? AND id = ?", q.TenantID, q.InspectionID).First(&inspection).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return apperror.New(apperror.NotFound, "inspectionId", "inspection not found")
			}
			return apperror.Wrap(apperror.Internal, err)
		}

		var aggregate struct {
			AttemptedCalls    int64   `gorm:"column:attempted_calls"`
			DeliveredCalls    int64   `gorm:"column:delivered_calls"`
			IncompleteCalls   int64   `gorm:"column:incomplete_calls"`
			InputTokens       int64   `gorm:"column:input_tokens"`
			OutputTokens      int64   `gorm:"column:output_tokens"`
			KnownReportedCost float64 `gorm:"column:known_reported_cost"`
			UnknownCostCalls  int64   `gorm:"column:unknown_cost_calls"`
			CachedInputTokens int64   `gorm:"column:cached_input_tokens"`
			CacheHitCalls     int64   `gorm:"column:cache_hit_calls"`
			KnownCacheCalls   int64   `gorm:"column:known_cache_calls"`
			UnknownCacheCalls int64   `gorm:"column:unknown_cache_calls"`
		}
		aggregateQuery := tx.Model(&database.LLMCallRecord{}).
			Select(`
				COUNT(*) AS attempted_calls,
				COALESCE(SUM(CASE WHEN transport_delivered = TRUE THEN 1 ELSE 0 END), 0) AS delivered_calls,
				COALESCE(SUM(CASE WHEN state = 'STARTED' THEN 1 ELSE 0 END), 0) AS incomplete_calls,
				COALESCE(SUM(CASE WHEN transport_delivered = TRUE THEN COALESCE(input_tokens, 0) ELSE 0 END), 0) AS input_tokens,
				COALESCE(SUM(CASE WHEN transport_delivered = TRUE THEN COALESCE(output_tokens, 0) ELSE 0 END), 0) AS output_tokens,
				COALESCE(SUM(CASE WHEN transport_delivered = TRUE THEN COALESCE(reported_cost, 0) ELSE 0 END), 0) AS known_reported_cost,
				COALESCE(SUM(CASE WHEN transport_delivered = TRUE AND reported_cost IS NULL THEN 1 ELSE 0 END), 0) AS unknown_cost_calls,
				COALESCE(SUM(CASE WHEN transport_delivered = TRUE THEN COALESCE(cached_input_tokens, 0) ELSE 0 END), 0) AS cached_input_tokens,
				COALESCE(SUM(CASE WHEN transport_delivered = TRUE AND cached_input_tokens > 0 THEN 1 ELSE 0 END), 0) AS cache_hit_calls,
				COALESCE(SUM(CASE WHEN transport_delivered = TRUE AND cached_input_tokens IS NOT NULL THEN 1 ELSE 0 END), 0) AS known_cache_calls,
				COALESCE(SUM(CASE WHEN transport_delivered = TRUE AND cached_input_tokens IS NULL THEN 1 ELSE 0 END), 0) AS unknown_cache_calls`).
			Where("tenant_id = ? AND inspection_id = ? AND mode = ?", q.TenantID, q.InspectionID, mode)
		if err := aggregateQuery.Scan(&aggregate).Error; err != nil {
			return apperror.Wrap(apperror.Internal, err)
		}

		coverageStartedAt, err := migration40StartedAt(tx)
		if err != nil {
			return apperror.Wrap(apperror.Internal, err)
		}
		coverageComplete := coverageStartedAt != nil && !inspection.CreatedAt.Before(*coverageStartedAt)
		result = Result{
			InspectionID:      q.InspectionID,
			Mode:              mode,
			AttemptedCalls:    int(aggregate.AttemptedCalls),
			DeliveredCalls:    int(aggregate.DeliveredCalls),
			IncompleteCalls:   int(aggregate.IncompleteCalls),
			InputTokens:       aggregate.InputTokens,
			OutputTokens:      aggregate.OutputTokens,
			KnownReportedCost: aggregate.KnownReportedCost,
			UnknownCostCalls:  int(aggregate.UnknownCostCalls),
			CachedInputTokens: aggregate.CachedInputTokens, CacheHitCalls: int(aggregate.CacheHitCalls),
			KnownCacheCalls: int(aggregate.KnownCacheCalls), UnknownCacheCalls: int(aggregate.UnknownCacheCalls),
			CoverageStartedAt: coverageStartedAt,
			CoverageComplete:  coverageComplete,
		}
		result.CostComplete = coverageComplete && result.IncompleteCalls == 0 && result.UnknownCostCalls == 0

		callsQuery := tx.Model(&database.LLMCallRecord{}).
			Where("tenant_id = ? AND inspection_id = ? AND mode = ?", q.TenantID, q.InspectionID, mode)
		if q.After != "" {
			cursor, err := graph.DecodeCursor(q.After)
			if err != nil {
				return err
			}
			startedAt, err := time.Parse(time.RFC3339Nano, cursor.Time)
			if err != nil {
				return apperror.New(apperror.InvalidInput, "after", "invalid cursor")
			}
			callID, err := identity.ParseID(cursor.ID)
			if err != nil {
				return apperror.New(apperror.InvalidInput, "after", "invalid cursor")
			}
			callsQuery = callsQuery.Where("(started_at, call_id) < (?, ?)", startedAt, callID)
		}
		var rows []database.LLMCallRecord
		if err := callsQuery.Order("started_at DESC, call_id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
			return apperror.Wrap(apperror.Internal, err)
		}
		if len(rows) > limit {
			result.HasNextPage = true
			rows = rows[:limit]
		}
		result.Calls = make([]Call, 0, len(rows))
		for _, row := range rows {
			result.Calls = append(result.Calls, Call{
				CallID: row.CallID, JobID: row.JobID, EventID: row.EventID, ExecutionID: row.ExecutionID,
				CorrelationID: row.CorrelationID, Attempt: row.Attempt, ReplayGeneration: row.ReplayGeneration,
				Mode: row.Mode, ComparisonMode: row.ComparisonMode, ModelAlias: row.ModelAlias,
				Provider: row.Provider, Model: row.Model, GatewayRequestID: row.GatewayRequestID,
				State: row.State, TechnicalOutcome: row.TechnicalOutcome, TransportDelivered: row.TransportDelivered,
				HTTPStatus: row.HTTPStatus, InputTokens: row.InputTokens, OutputTokens: row.OutputTokens,
				CachedInputTokens: row.CachedInputTokens, ImageCount: row.ImageCount, RequestBodyBytes: row.RequestBodyBytes,
				ReportedCost: row.ReportedCost, DurationMS: row.DurationMS, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt,
			})
		}
		if len(result.Calls) > 0 {
			last := result.Calls[len(result.Calls)-1]
			result.EndCursor = graph.EncodeCursor(graph.Cursor{Time: last.StartedAt.UTC().Format(time.RFC3339Nano), ID: last.CallID.String()})
		}
		return nil
	})
	return result, err
}

func withTenant(ctx context.Context, db *gorm.DB, tenantID identity.ID, fn func(*gorm.DB) error) error {
	if db.Dialector.Name() == "postgres" {
		return (tenanttx.Runner{DB: db}).Within(ctx, tenantID, fn)
	}
	return fn(db.WithContext(ctx))
}

func migration40StartedAt(db *gorm.DB) (*time.Time, error) {
	var row struct {
		AppliedAt time.Time `gorm:"column:applied_at"`
	}
	query := db.Table("platform.schema_migrations").Select("applied_at").Where("version = ?", 40).Scan(&row)
	if query.Error != nil {
		// SQLite unit tests do not have the PostgreSQL migration catalog. A nil
		// marker accurately reports unknown historical coverage in that case.
		if db.Dialector.Name() != "postgres" {
			return nil, nil
		}
		return nil, query.Error
	}
	if row.AppliedAt.IsZero() {
		return nil, nil
	}
	appliedAt := row.AppliedAt.UTC()
	return &appliedAt, nil
}
