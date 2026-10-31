// Package get_inspection_llm_usage exposes the durable per-call LLM ledger.
package get_inspection_llm_usage

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/usage/core"
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
	IncludeTestData        bool
	First                  int
	After                  string
}

// Result is the inspection-level ledger projection returned to GraphQL.
type Result struct {
	InspectionID             identity.ID
	Mode                     string
	AttemptedCalls           int
	DeliveredCalls           int
	IncompleteCalls          int
	InputTokens              int64
	OutputTokens             int64
	KnownReportedCost        float64
	KnownReportedCostDecimal string
	UnknownCostCalls         int
	UncertainCostCalls       int
	ErrorWithoutCostCalls    int
	UnverifiedCostCalls      int
	CachedInputTokens        int64
	CacheHitCalls            int
	KnownCacheCalls          int
	UnknownCacheCalls        int
	CostComplete             bool
	CoverageStartedAt        *time.Time
	CoverageComplete         bool
	Calls                    []Call
	EndCursor                string
	HasNextPage              bool
}

// Call is a safe, non-content projection of one LLM call.
type Call struct {
	CallID              identity.ID
	JobID               identity.ID
	EventID             identity.ID
	ExecutionID         identity.ID
	CorrelationID       string
	Attempt             int
	ReplayGeneration    int
	Mode                string
	ComparisonMode      string
	ModelAlias          string
	Provider            string
	Model               string
	GatewayRequestID    string
	GatewayCallID       string
	ReportedCostDecimal *string
	CostSource          string
	CostState           string
	State               string
	TechnicalOutcome    string
	TransportDelivered  *bool
	HTTPStatus          *int
	InputTokens         *int64
	OutputTokens        *int64
	CachedInputTokens   *int64
	ImageCount          *int
	RequestBodyBytes    *int64
	ReportedCost        *float64
	DurationMS          *int64
	StartedAt           time.Time
	FinishedAt          *time.Time
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
		if err := tx.Select("id").Where("tenant_id = ? AND id = ?", q.TenantID, q.InspectionID).First(&inspection).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return apperror.New(apperror.NotFound, "inspectionId", "inspection not found")
			}
			return apperror.Wrap(apperror.Internal, err)
		}

		var aggregate struct {
			AttemptedCalls           int64  `gorm:"column:attempted_calls"`
			DeliveredCalls           int64  `gorm:"column:delivered_calls"`
			IncompleteCalls          int64  `gorm:"column:incomplete_calls"`
			InputTokens              int64  `gorm:"column:input_tokens"`
			OutputTokens             int64  `gorm:"column:output_tokens"`
			KnownReportedCostDecimal string `gorm:"column:known_reported_cost_decimal"`
			UnknownCostCalls         int64  `gorm:"column:unknown_cost_calls"`
			UncertainCostCalls       int64  `gorm:"column:uncertain_cost_calls"`
			ErrorWithoutCostCalls    int64  `gorm:"column:error_without_cost_calls"`
			UnverifiedCostCalls      int64  `gorm:"column:unverified_cost_calls"`
			CachedInputTokens        int64  `gorm:"column:cached_input_tokens"`
			CacheHitCalls            int64  `gorm:"column:cache_hit_calls"`
			KnownCacheCalls          int64  `gorm:"column:known_cache_calls"`
			UnknownCacheCalls        int64  `gorm:"column:unknown_cache_calls"`
			CallsBeforeCutoff        int64  `gorm:"column:calls_before_cutoff"`
		}
		costStateSQL := llmCostStateSQL(tx)
		aggregateQuery := tx.Model(&database.LLMCallRecord{}).
			Select(fmt.Sprintf(`
				COUNT(*) AS attempted_calls,
				COALESCE(SUM(CASE WHEN transport_delivered = TRUE THEN 1 ELSE 0 END), 0) AS delivered_calls,
				COALESCE(SUM(CASE WHEN state = 'STARTED' THEN 1 ELSE 0 END), 0) AS incomplete_calls,
				COALESCE(SUM(CASE WHEN transport_delivered = TRUE THEN COALESCE(input_tokens, 0) ELSE 0 END), 0) AS input_tokens,
				COALESCE(SUM(CASE WHEN transport_delivered = TRUE THEN COALESCE(output_tokens, 0) ELSE 0 END), 0) AS output_tokens,
				COALESCE(SUM(CASE WHEN %s='unknown' THEN 1 ELSE 0 END),0) AS unknown_cost_calls,
				CAST(COALESCE(SUM(CASE WHEN %s='confirmed' THEN reported_cost ELSE 0 END),0) AS TEXT) AS known_reported_cost_decimal,
			COALESCE(SUM(CASE WHEN %s='uncertain' THEN 1 ELSE 0 END),0) AS uncertain_cost_calls,
			COALESCE(SUM(CASE WHEN %s='error_without_cost' THEN 1 ELSE 0 END),0) AS error_without_cost_calls,
			COALESCE(SUM(CASE WHEN %s='unverified' THEN 1 ELSE 0 END),0) AS unverified_cost_calls,
				COALESCE(SUM(CASE WHEN transport_delivered = TRUE THEN COALESCE(cached_input_tokens, 0) ELSE 0 END), 0) AS cached_input_tokens,
				COALESCE(SUM(CASE WHEN transport_delivered = TRUE AND cached_input_tokens > 0 THEN 1 ELSE 0 END), 0) AS cache_hit_calls,
				COALESCE(SUM(CASE WHEN transport_delivered = TRUE AND cached_input_tokens IS NOT NULL THEN 1 ELSE 0 END), 0) AS known_cache_calls,
				COALESCE(SUM(CASE WHEN transport_delivered = TRUE AND cached_input_tokens IS NULL THEN 1 ELSE 0 END), 0) AS unknown_cache_calls`, costStateSQL, costStateSQL, costStateSQL, costStateSQL, costStateSQL)).
			Where("tenant_id = ? AND inspection_id = ? AND mode = ?", q.TenantID, q.InspectionID, mode)
		if !q.IncludeTestData {
			aggregateQuery = aggregateQuery.Where("provider IS DISTINCT FROM ?", "qa")
		}
		if err := aggregateQuery.Scan(&aggregate).Error; err != nil {
			return apperror.Wrap(apperror.Internal, err)
		}

		coverageStartedAt, err := migrationCostCoverageStartedAt(tx)
		if err != nil {
			return apperror.Wrap(apperror.Internal, err)
		}
		if coverageStartedAt != nil {
			beforeCutoff := tx.Model(&database.LLMCallRecord{}).
				Where("tenant_id = ? AND inspection_id = ? AND mode = ? AND started_at < ?", q.TenantID, q.InspectionID, mode, *coverageStartedAt)
			if !q.IncludeTestData {
				beforeCutoff = beforeCutoff.Where("provider IS DISTINCT FROM ?", "qa")
			}
			if err := beforeCutoff.Count(&aggregate.CallsBeforeCutoff).Error; err != nil {
				return apperror.Wrap(apperror.Internal, err)
			}
		}
		coverageComplete := core.InspectionCoverageComplete(coverageStartedAt, aggregate.CallsBeforeCutoff, aggregate.UnverifiedCostCalls)
		result = Result{
			InspectionID:             q.InspectionID,
			Mode:                     mode,
			AttemptedCalls:           int(aggregate.AttemptedCalls),
			DeliveredCalls:           int(aggregate.DeliveredCalls),
			IncompleteCalls:          int(aggregate.IncompleteCalls),
			InputTokens:              aggregate.InputTokens,
			OutputTokens:             aggregate.OutputTokens,
			KnownReportedCost:        parseDecimalCost(aggregate.KnownReportedCostDecimal),
			UnknownCostCalls:         int(aggregate.UnknownCostCalls),
			KnownReportedCostDecimal: aggregate.KnownReportedCostDecimal,
			UncertainCostCalls:       int(aggregate.UncertainCostCalls), ErrorWithoutCostCalls: int(aggregate.ErrorWithoutCostCalls), UnverifiedCostCalls: int(aggregate.UnverifiedCostCalls),
			CachedInputTokens: aggregate.CachedInputTokens, CacheHitCalls: int(aggregate.CacheHitCalls),
			KnownCacheCalls: int(aggregate.KnownCacheCalls), UnknownCacheCalls: int(aggregate.UnknownCacheCalls),
			CoverageStartedAt: coverageStartedAt,
			CoverageComplete:  coverageComplete,
		}
		result.CostComplete = core.CostComplete(int64(result.UnknownCostCalls), int64(result.UncertainCostCalls), int64(result.UnverifiedCostCalls))

		callsQuery := tx.Model(&database.LLMCallRecord{}).
			Where("tenant_id = ? AND inspection_id = ? AND mode = ?", q.TenantID, q.InspectionID, mode)
		if !q.IncludeTestData {
			callsQuery = callsQuery.Where("provider IS DISTINCT FROM ?", "qa")
		}
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
		type callWithDecimal struct {
			database.LLMCallRecord
			ReportedCostDecimal *string `gorm:"column:reported_cost_decimal"`
			CostState           string  `gorm:"column:cost_state"`
		}
		var rows []callWithDecimal
		if err := callsQuery.Select("*, CAST(reported_cost AS TEXT) AS reported_cost_decimal, " + costStateSQL + " AS cost_state").Order("started_at DESC, call_id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
			return apperror.Wrap(apperror.Internal, err)
		}
		if len(rows) > limit {
			result.HasNextPage = true
			rows = rows[:limit]
		}
		result.Calls = make([]Call, 0, len(rows))
		for _, row := range rows {
			costSource := ""
			if row.CostSource != nil {
				costSource = *row.CostSource
			}
			result.Calls = append(result.Calls, Call{
				CallID: row.CallID, JobID: row.JobID, EventID: row.EventID, ExecutionID: row.ExecutionID,
				CorrelationID: row.CorrelationID, Attempt: row.Attempt, ReplayGeneration: row.ReplayGeneration,
				Mode: row.Mode, ComparisonMode: row.ComparisonMode, ModelAlias: row.ModelAlias,
				Provider: row.Provider, Model: row.Model, GatewayRequestID: row.GatewayRequestID, GatewayCallID: row.GatewayCallID,
				ReportedCostDecimal: row.ReportedCostDecimal, CostSource: costSource, CostState: row.CostState,
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

func parseDecimalCost(value string) float64 {
	cost, _ := strconv.ParseFloat(value, 64)
	return cost
}

func llmCostStateSQL(db *gorm.DB) string {
	if db.Dialector.Name() != "sqlite" {
		return "usage.llm_cost_state(state,transport_delivered,http_status,technical_outcome,cost_source)"
	}
	return "CASE WHEN state='STARTED' THEN 'uncertain' WHEN state='FINISHED' AND COALESCE(transport_delivered,FALSE)=FALSE THEN CASE WHEN technical_outcome IN ('timeout','cancelled','transport') THEN 'uncertain' ELSE 'not_sent' END WHEN cost_source='header' THEN 'confirmed' WHEN cost_source='unknown' AND transport_delivered=TRUE AND http_status BETWEEN 200 AND 299 THEN 'unknown' WHEN cost_source='error_missing' THEN 'error_without_cost' ELSE 'unverified' END"
}

func withTenant(ctx context.Context, db *gorm.DB, tenantID identity.ID, fn func(*gorm.DB) error) error {
	if db.Dialector.Name() == "postgres" {
		return (tenanttx.Runner{DB: db}).Within(ctx, tenantID, fn)
	}
	return fn(db.WithContext(ctx))
}

func migrationCostCoverageStartedAt(db *gorm.DB) (*time.Time, error) {
	var row struct {
		AppliedAt time.Time `gorm:"column:applied_at"`
	}
	query := db.Table("platform.schema_migrations").Select("applied_at").Where("version = ?", 96).Scan(&row)
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
