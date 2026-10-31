// Package get_global_llm_usage exposes the super-admin LLM ledger projection.
package get_global_llm_usage

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/usage/core"
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
	From, To          time.Time
	TenantID          *identity.ID
	InspectionID      *identity.ID
	WithoutInspection bool
	Provider          string
	Model             string
	ModelAlias        string
	Mode              string
	TechnicalOutcome  string
	State             string
	CostState         string
	IncludeTestData   bool
	First             int
	After             string
}

// Dependencies are the dependencies of the global usage slice.
type Dependencies struct {
	DB  *gorm.DB
	Bus *mediator.Bus
}

// Result is the aggregate and page returned by the global ledger query.
type Result struct {
	From, To                 time.Time
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
	CoverageStartedAt        *time.Time
	CoverageComplete         bool
	CostComplete             bool
	Calls                    []Call
	EndCursor                string
	HasNextPage              bool
}

// Call is a safe, non-content projection of one global LLM call.
type Call struct {
	TenantID, JobID, EventID, ExecutionID                        identity.ID
	InspectionID                                                 *identity.ID
	TenantName, CorrelationID, Provider, Model, GatewayRequestID string
	CallID                                                       identity.ID
	GatewayCallID, ReportedCostDecimal, CostSource, CostState    string
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
	if costState != "" && costState != "informed" && costState != "missing" && costState != "uncertain" && costState != "error_without_cost" && costState != "unverified" && costState != "not_sent" {
		return Result{}, apperror.New(apperror.InvalidInput, "cost", "invalid cost filter")
	}
	if q.WithoutInspection && q.InspectionID != nil {
		return Result{}, apperror.New(apperror.InvalidInput, "withoutInspection", "Não é possível combinar o filtro sem vistoria com uma vistoria específica.")
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
		var costAggregate struct {
			AttemptedCalls        int64      `gorm:"column:attempted_calls"`
			DeliveredCalls        int64      `gorm:"column:delivered_calls"`
			IncompleteCalls       int64      `gorm:"column:incomplete_calls"`
			InputTokens           int64      `gorm:"column:input_tokens"`
			OutputTokens          int64      `gorm:"column:output_tokens"`
			CachedInputTokens     int64      `gorm:"column:cached_input_tokens"`
			CacheHitCalls         int64      `gorm:"column:cache_hit_calls"`
			KnownCacheCalls       int64      `gorm:"column:known_cache_calls"`
			UnknownCacheCalls     int64      `gorm:"column:unknown_cache_calls"`
			KnownCostDecimal      string     `gorm:"column:known_cost_decimal"`
			UnknownCostCalls      int64      `gorm:"column:unknown_cost_calls"`
			UncertainCostCalls    int64      `gorm:"column:uncertain_cost_calls"`
			ErrorWithoutCostCalls int64      `gorm:"column:error_without_cost_calls"`
			UnverifiedCostCalls   int64      `gorm:"column:unverified_cost_calls"`
			CostCoverageStartedAt *time.Time `gorm:"column:cost_coverage_started_at"`
		}
		if err := tx.Raw(`SELECT attempted_calls, delivered_calls, incomplete_calls, input_tokens, output_tokens, cached_input_tokens, cache_hit_calls, known_cache_calls, unknown_cache_calls, known_cost_decimal, unknown_cost_calls, uncertain_cost_calls, error_without_cost_calls, unverified_cost_calls, cost_coverage_started_at
			FROM usage.read_llm_cost_coverage_v1(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			from, to, nullableID(q.TenantID), nullableID(q.InspectionID), q.WithoutInspection, nullableString(q.Provider), nullableString(q.Model), nullableString(q.ModelAlias), mode, nullableString(q.TechnicalOutcome), nullableString(state), nullableString(costState), q.IncludeTestData).Scan(&costAggregate).Error; err != nil {
			return fmt.Errorf("global LLM cost coverage: %w", err)
		}
		coverageComplete := core.WindowCoverageComplete(costAggregate.CostCoverageStartedAt, from, costAggregate.UnverifiedCostCalls)
		knownCost, _ := strconv.ParseFloat(costAggregate.KnownCostDecimal, 64)
		result = Result{
			From: from, To: to, AttemptedCalls: int(costAggregate.AttemptedCalls), DeliveredCalls: int(costAggregate.DeliveredCalls),
			IncompleteCalls: int(costAggregate.IncompleteCalls), InputTokens: costAggregate.InputTokens, OutputTokens: costAggregate.OutputTokens,
			KnownReportedCost: knownCost, KnownReportedCostDecimal: costAggregate.KnownCostDecimal,
			UnknownCostCalls: int(costAggregate.UnknownCostCalls), UncertainCostCalls: int(costAggregate.UncertainCostCalls),
			ErrorWithoutCostCalls: int(costAggregate.ErrorWithoutCostCalls), UnverifiedCostCalls: int(costAggregate.UnverifiedCostCalls),
			CachedInputTokens: costAggregate.CachedInputTokens, CacheHitCalls: int(costAggregate.CacheHitCalls),
			KnownCacheCalls: int(costAggregate.KnownCacheCalls), UnknownCacheCalls: int(costAggregate.UnknownCacheCalls),
			CoverageStartedAt: costAggregate.CostCoverageStartedAt, CoverageComplete: coverageComplete,
		}
		result.CostComplete = core.CostComplete(costAggregate.UnknownCostCalls, costAggregate.UncertainCostCalls, costAggregate.UnverifiedCostCalls)
		rows := make([]globalCallRow, 0, first+1)
		if err := tx.Raw(`
			SELECT call_id, tenant_id, tenant_name, inspection_id, job_id, event_id, execution_id,
			       correlation_id, attempt, replay_generation, mode, comparison_mode, model_alias,
			       provider, model, gateway_request_id, state, technical_outcome, transport_delivered,
			       http_status, input_tokens, output_tokens, reported_cost, duration_ms, started_at, finished_at,
			       cached_input_tokens, image_count, request_body_bytes, gateway_call_id, cost_source, reported_cost_decimal, cost_state
			FROM usage.read_llm_usage_calls_v3(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			from, to, nullableID(q.TenantID), nullableID(q.InspectionID), q.WithoutInspection, nullableString(q.Provider),
			nullableString(q.Model), nullableString(q.ModelAlias), mode, nullableString(q.TechnicalOutcome),
			nullableString(state), nullableString(costState), cursorTime, cursorID, first+1, q.IncludeTestData).Scan(&rows).Error; err != nil {
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

type globalCallRow struct {
	CallID, TenantID, JobID, EventID, ExecutionID                identity.ID
	InspectionID                                                 *identity.ID
	TenantName, CorrelationID, Provider, Model, GatewayRequestID string
	GatewayCallID, ReportedCostDecimal, CostSource, CostState    string
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
	return Call{CallID: r.CallID, TenantID: r.TenantID, TenantName: r.TenantName, InspectionID: r.InspectionID, JobID: r.JobID, EventID: r.EventID, ExecutionID: r.ExecutionID, CorrelationID: r.CorrelationID, Attempt: r.Attempt, ReplayGeneration: r.ReplayGeneration, Mode: r.Mode, ComparisonMode: r.ComparisonMode, ModelAlias: r.ModelAlias, Provider: r.Provider, Model: r.Model, GatewayRequestID: r.GatewayRequestID, GatewayCallID: r.GatewayCallID, ReportedCostDecimal: r.ReportedCostDecimal, CostSource: r.CostSource, CostState: r.CostState, State: r.State, TechnicalOutcome: r.TechnicalOutcome, TransportDelivered: r.TransportDelivered, HTTPStatus: r.HTTPStatus, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, CachedInputTokens: r.CachedInputTokens, ImageCount: r.ImageCount, RequestBodyBytes: r.RequestBodyBytes, ReportedCost: r.ReportedCost, DurationMS: r.DurationMS, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt}
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
