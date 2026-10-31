// Package llm_usage_by_tenant exposes paginated tenant LLM cost aggregates.
package llm_usage_by_tenant

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

// Query carries the shared usage filters and tenant keyset cursor.
type Query struct {
	From, To                                                time.Time
	Provider, Model, ModelAlias, Outcome, Mode, State, Cost string
	IncludeTestData                                         bool
	First                                                   int
	After                                                   string
}

// Tenant is an aggregate over every matching call, independent of page size.
type Tenant struct {
	TenantID, TenantName, KnownCostDecimal                                                                                                                          string
	AttemptedCalls, DeliveredCalls, InputTokens, OutputTokens, ConfirmedCostCalls, UnknownCostCalls, UncertainCostCalls, ErrorWithoutCostCalls, UnverifiedCostCalls int
	CostComplete, CoverageComplete                                                                                                                                  bool
}

// Result is one cursor page of tenant aggregates.
type Result struct {
	Nodes       []Tenant
	EndCursor   string
	HasNextPage bool
}

// Dependencies are the database and mediator used by this slice.
type Dependencies struct {
	DB  *gorm.DB
	Bus *mediator.Bus
}
type row struct {
	TenantID              string `gorm:"column:tenant_id"`
	TenantName            string `gorm:"column:tenant_name"`
	AttemptedCalls        int    `gorm:"column:attempted_calls"`
	DeliveredCalls        int    `gorm:"column:delivered_calls"`
	InputTokens           int    `gorm:"column:input_tokens"`
	OutputTokens          int    `gorm:"column:output_tokens"`
	KnownCostDecimal      string `gorm:"column:known_cost_decimal"`
	ConfirmedCostCalls    int    `gorm:"column:confirmed_cost_calls"`
	UnknownCostCalls      int    `gorm:"column:unknown_cost_calls"`
	UncertainCostCalls    int    `gorm:"column:uncertain_cost_calls"`
	ErrorWithoutCostCalls int    `gorm:"column:error_without_cost_calls"`
	UnverifiedCostCalls   int    `gorm:"column:unverified_cost_calls"`
	CostComplete          bool   `gorm:"column:cost_complete"`
	CoverageComplete      bool   `gorm:"column:coverage_complete"`
}

// Setup registers the tenant aggregate query.
func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil {
		return fmt.Errorf("slice usage/llm_usage_by_tenant: missing dependency")
	}
	return deps.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) { return handle(ctx, deps.DB, raw.(Query)) })
}
func handle(ctx context.Context, db *gorm.DB, q Query) (Result, error) {
	from, to := q.From, q.To
	now := time.Now().UTC()
	if from.IsZero() {
		from = now.Add(-30 * 24 * time.Hour)
	}
	if to.IsZero() {
		to = now
	}
	if !from.Before(to) || to.Sub(from) > 90*24*time.Hour {
		return Result{}, apperror.New(apperror.InvalidInput, "from", "invalid time window")
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
	cost := strings.ToLower(strings.TrimSpace(q.Cost))
	if cost != "" && cost != "informed" && cost != "missing" && cost != "uncertain" && cost != "error_without_cost" && cost != "unverified" && cost != "not_sent" {
		return Result{}, apperror.New(apperror.InvalidInput, "cost", "invalid cost filter")
	}
	for _, value := range []string{q.Provider, q.Model, q.ModelAlias, q.Outcome} {
		if len(value) > 200 {
			return Result{}, apperror.New(apperror.InvalidInput, "filter", "filter is too long")
		}
	}
	first, err := graph.PageSize(q.First)
	if err != nil {
		return Result{}, apperror.New(apperror.InvalidInput, "first", "invalid page size")
	}
	var afterName *string
	var afterID any
	if q.After != "" {
		c, e := graph.DecodeCursor(q.After)
		if e != nil {
			return Result{}, e
		}
		if c.ID == "" {
			return Result{}, apperror.New(apperror.InvalidInput, "after", "invalid cursor")
		}
		id, parseErr := identity.ParseID(c.ID)
		if parseErr != nil {
			return Result{}, apperror.New(apperror.InvalidInput, "after", "invalid cursor")
		}
		afterName = &c.Time
		afterID = id
	}
	var rows []row
	err = db.WithContext(ctx).Raw(`SELECT tenant_id::text,tenant_name,attempted_calls,delivered_calls,input_tokens,output_tokens,known_cost_decimal,confirmed_cost_calls,unknown_cost_calls,uncertain_cost_calls,error_without_cost_calls,unverified_cost_calls,cost_complete,coverage_complete FROM usage.read_llm_usage_by_tenant_v1(?,?,?,?,?,?,?,?,?,?,?,?::uuid,?)`, from, to, nullable(q.Provider), nullable(q.Model), nullable(q.ModelAlias), nullable(q.Outcome), mode, nullable(state), nullable(cost), q.IncludeTestData, afterName, afterID, first+1).Scan(&rows).Error
	if err != nil {
		return Result{}, fmt.Errorf("tenant LLM usage aggregate: %w", err)
	}
	out := Result{Nodes: make([]Tenant, 0, len(rows))}
	if len(rows) > first {
		out.HasNextPage = true
		rows = rows[:first]
	}
	for _, r := range rows {
		out.Nodes = append(out.Nodes, Tenant{TenantID: r.TenantID, TenantName: r.TenantName, AttemptedCalls: r.AttemptedCalls, DeliveredCalls: r.DeliveredCalls, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, KnownCostDecimal: r.KnownCostDecimal, ConfirmedCostCalls: r.ConfirmedCostCalls, UnknownCostCalls: r.UnknownCostCalls, UncertainCostCalls: r.UncertainCostCalls, ErrorWithoutCostCalls: r.ErrorWithoutCostCalls, UnverifiedCostCalls: r.UnverifiedCostCalls, CostComplete: r.CostComplete, CoverageComplete: r.CoverageComplete})
	}
	if len(out.Nodes) > 0 {
		last := out.Nodes[len(out.Nodes)-1]
		out.EndCursor = graph.EncodeCursor(graph.Cursor{Time: last.TenantName, ID: last.TenantID})
	}
	return out, nil
}
func nullable(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.TrimSpace(s)
}
