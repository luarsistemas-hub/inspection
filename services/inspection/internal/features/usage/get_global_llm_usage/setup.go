// Package get_global_llm_usage exposes the super-admin LLM ledger projection.
package get_global_llm_usage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/gorm"
)

const (
	defaultWindow = 30 * 24 * time.Hour
	maxWindow     = 90 * 24 * time.Hour
)

// Query describes the global LLM ledger filters. From is inclusive and To is
// exclusive. An empty window defaults to the latest 30 days.
type Query struct {
	From, To         time.Time
	TenantID         *identity.ID
	InspectionID     *identity.ID
	Provider         string
	Model            string
	ModelAlias       string
	Mode             string
	TechnicalOutcome string
	State            string
	CostState        string
	First            int
	After            string
}

// Dependencies are the dependencies of the global usage slice.
type Dependencies struct {
	DB  *gorm.DB
	Bus *mediator.Bus
}

// Result is the aggregate and page returned by the global ledger query.
type Result struct {
	From, To          time.Time
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
	CoverageStartedAt *time.Time
	CoverageComplete  bool
	CostComplete      bool
	Calls             []Call
	EndCursor         string
	HasNextPage       bool
}

// Call is a safe, non-content projection of one global LLM call.
type Call struct {
	TenantID, InspectionID, JobID, EventID, ExecutionID          identity.ID
	TenantName, CorrelationID, Provider, Model, GatewayRequestID string
	CallID                                                       identity.ID
	Attempt, ReplayGeneration                                    int
	Mode, ComparisonMode, ModelAlias, State, TechnicalOutcome    string
	TransportDelivered                                           *bool
	HTTPStatus                                                   *int
	InputTokens, OutputTokens                                    *int64
	CachedInputTokens, RequestBodyBytes                          *int64
	ImageCount                                                   *int
	ReportedCost                                                 *float64
	DurationMS                                                   *int64
	StartedAt                                                    time.Time
	FinishedAt                                                   *time.Time
}

// Setup registers the global LLM usage query in the mediator.
func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil {
		return fmt.Errorf("slice usage/get_global_llm_usage: missing dependency")
	}
	return deps.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		return handle(ctx, deps.DB, raw.(Query))
	})
}

func handle(ctx context.Context, db *gorm.DB, q Query) (Result, error) {
	from, to, err := normalizeWindow(q.From, q.To)
	if err != nil {
		return Result{}, err
	}
	mode := strings.ToLower(strings.TrimSpace(q.Mode))
	if mode == "" {
		mode = "live"
	}
	if mode != "live" && mode != "mock" {
		return Result{}, apperror.New(apperror.InvalidInput, "mode", "invalid execution mode")
	}
	state := strings.ToUpper(strings.TrimSpace(q.State))
	if state != "" && state != "STARTED" && state != "FINISHED" {
		return Result{}, apperror.New(apperror.InvalidInput, "state", "invalid call state")
	}
	costState := strings.ToLower(strings.TrimSpace(q.CostState))
	if costState != "" && costState != "informed" && costState != "missing" {
		return Result{}, apperror.New(apperror.InvalidInput, "cost", "invalid cost filter")
	}
	first, err := graph.PageSize(q.First)
	if err != nil {
		return Result{}, apperror.New(apperror.InvalidInput, "first", "invalid page size")
	}
	if err := validateFilter(q.Provider, q.Model, q.ModelAlias, q.TechnicalOutcome); err != nil {
		return Result{}, err
	}

	var cursorTime *time.Time
	var cursorID *identity.ID
	if q.After != "" {
		cursor, err := graph.DecodeCursor(q.After)
		if err != nil {
			return Result{}, err
		}
		parsedTime, err := time.Parse(time.RFC3339Nano, cursor.Time)
		if err != nil {
			return Result{}, apperror.New(apperror.InvalidInput, "after", "invalid cursor")
		}
		parsedID, err := identity.ParseID(cursor.ID)
		if err != nil {
			return Result{}, apperror.New(apperror.InvalidInput, "after", "invalid cursor")
		}
		cursorTime, cursorID = &parsedTime, &parsedID
	}

	var result Result
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var aggregate globalAggregate
		if err := tx.Raw(`
			SELECT attempted_calls, delivered_calls, incomplete_calls, input_tokens,
			       output_tokens, known_reported_cost, unknown_cost_calls,
			       coverage_started_at, legacy_coverage_calls
			FROM usage.read_llm_usage_summary(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			from, to, nullableID(q.TenantID), nullableID(q.InspectionID), nullableString(q.Provider),
			nullableString(q.Model), nullableString(q.ModelAlias), mode, nullableString(q.TechnicalOutcome),
			nullableString(state), nullableString(costState)).Scan(&aggregate).Error; err != nil {
			return fmt.Errorf("global LLM usage summary: %w", err)
		}
		var cacheAggregate struct {
			CachedInputTokens int64
			CacheHitCalls     int64
			KnownCacheCalls   int64
			UnknownCacheCalls int64
		}
		if err := tx.Raw(`SELECT cached_input_tokens, cache_hit_calls, known_cache_calls, unknown_cache_calls
			FROM usage.read_llm_cache_summary_v1(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			from, to, nullableID(q.TenantID), nullableID(q.InspectionID), nullableString(q.Provider),
			nullableString(q.Model), nullableString(q.ModelAlias), mode, nullableString(q.TechnicalOutcome),
			nullableString(state), nullableString(costState)).Scan(&cacheAggregate).Error; err != nil {
			return fmt.Errorf("global LLM cache usage summary: %w", err)
		}
		coverageComplete := aggregate.CoverageStartedAt != nil && !from.Before(aggregate.CoverageStartedAt.UTC()) && aggregate.LegacyCoverageCalls == 0
		result = Result{
			From: from, To: to, AttemptedCalls: int(aggregate.AttemptedCalls), DeliveredCalls: int(aggregate.DeliveredCalls),
			IncompleteCalls: int(aggregate.IncompleteCalls), InputTokens: aggregate.InputTokens, OutputTokens: aggregate.OutputTokens,
			KnownReportedCost: aggregate.KnownReportedCost, UnknownCostCalls: int(aggregate.UnknownCostCalls),
			CachedInputTokens: cacheAggregate.CachedInputTokens, CacheHitCalls: int(cacheAggregate.CacheHitCalls),
			KnownCacheCalls: int(cacheAggregate.KnownCacheCalls), UnknownCacheCalls: int(cacheAggregate.UnknownCacheCalls),
			CoverageStartedAt: aggregate.CoverageStartedAt, CoverageComplete: coverageComplete,
		}
		result.CostComplete = coverageComplete && aggregate.IncompleteCalls == 0 && aggregate.UnknownCostCalls == 0
		rows := make([]globalCallRow, 0, first+1)
		if err := tx.Raw(`
			SELECT call_id, tenant_id, tenant_name, inspection_id, job_id, event_id, execution_id,
			       correlation_id, attempt, replay_generation, mode, comparison_mode, model_alias,
			       provider, model, gateway_request_id, state, technical_outcome, transport_delivered,
			       http_status, input_tokens, output_tokens, reported_cost, duration_ms, started_at, finished_at,
			       cached_input_tokens, image_count, request_body_bytes
			FROM usage.read_llm_usage_calls_v2(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			from, to, nullableID(q.TenantID), nullableID(q.InspectionID), nullableString(q.Provider),
			nullableString(q.Model), nullableString(q.ModelAlias), mode, nullableString(q.TechnicalOutcome),
			nullableString(state), nullableString(costState), cursorTime, cursorID, first+1).Scan(&rows).Error; err != nil {
			return fmt.Errorf("global LLM usage calls: %w", err)
		}
		if len(rows) > first {
			result.HasNextPage = true
			rows = rows[:first]
		}
		result.Calls = make([]Call, 0, len(rows))
		for _, row := range rows {
			result.Calls = append(result.Calls, row.call())
		}
		if len(result.Calls) > 0 {
			last := result.Calls[len(result.Calls)-1]
			result.EndCursor = graph.EncodeCursor(graph.Cursor{Time: last.StartedAt.UTC().Format(time.RFC3339Nano), ID: last.CallID.String()})
		}
		return nil
	})
	return result, err
}

type globalAggregate struct {
	AttemptedCalls, DeliveredCalls, IncompleteCalls                      int64
	InputTokens, OutputTokens                                            int64
	CachedInputTokens, CacheHitCalls, KnownCacheCalls, UnknownCacheCalls int64
	KnownReportedCost                                                    float64
	UnknownCostCalls, LegacyCoverageCalls                                int64
	CoverageStartedAt                                                    *time.Time
}

type globalCallRow struct {
	CallID, TenantID, InspectionID, JobID, EventID, ExecutionID  identity.ID
	TenantName, CorrelationID, Provider, Model, GatewayRequestID string
	Attempt, ReplayGeneration                                    int
	Mode, ComparisonMode, ModelAlias, State, TechnicalOutcome    string
	TransportDelivered                                           *bool
	HTTPStatus                                                   *int
	InputTokens, OutputTokens                                    *int64
	CachedInputTokens, RequestBodyBytes                          *int64
	ImageCount                                                   *int
	ReportedCost                                                 *float64
	DurationMS                                                   *int64
	StartedAt                                                    time.Time
	FinishedAt                                                   *time.Time
}

func (r globalCallRow) call() Call {
	return Call{CallID: r.CallID, TenantID: r.TenantID, TenantName: r.TenantName, InspectionID: r.InspectionID, JobID: r.JobID, EventID: r.EventID, ExecutionID: r.ExecutionID, CorrelationID: r.CorrelationID, Attempt: r.Attempt, ReplayGeneration: r.ReplayGeneration, Mode: r.Mode, ComparisonMode: r.ComparisonMode, ModelAlias: r.ModelAlias, Provider: r.Provider, Model: r.Model, GatewayRequestID: r.GatewayRequestID, State: r.State, TechnicalOutcome: r.TechnicalOutcome, TransportDelivered: r.TransportDelivered, HTTPStatus: r.HTTPStatus, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, CachedInputTokens: r.CachedInputTokens, ImageCount: r.ImageCount, RequestBodyBytes: r.RequestBodyBytes, ReportedCost: r.ReportedCost, DurationMS: r.DurationMS, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt}
}

func normalizeWindow(from, to time.Time) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	if from.IsZero() {
		from = now.Add(-defaultWindow)
	}
	if to.IsZero() {
		to = now
	}
	from, to = from.UTC(), to.UTC()
	if !from.Before(to) {
		return time.Time{}, time.Time{}, apperror.New(apperror.InvalidInput, "from", "invalid time window")
	}
	if to.Sub(from) > maxWindow {
		return time.Time{}, time.Time{}, apperror.New(apperror.InvalidInput, "to", "time window exceeds 90 days")
	}
	return from, to, nil
}

func validateFilter(values ...string) error {
	for _, value := range values {
		if len(value) > 200 {
			return apperror.New(apperror.InvalidInput, "filter", "filter is too long")
		}
	}
	return nil
}

func nullableID(value *identity.ID) any {
	if value == nil || *value == (identity.ID{}) {
		return nil
	}
	return *value
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}
