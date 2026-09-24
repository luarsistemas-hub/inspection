// Package list_llm_usage_tenants exposes the super-admin tenant filter options.
package list_llm_usage_tenants

import (
	"context"
	"fmt"
	"strings"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/gorm"
)

// Query identifies a page of tenants searchable by name or ID.
type Query struct {
	Search string
	First  int
	After  string
}

// Tenant is the safe tenant projection used by the LLM usage filter.
type Tenant struct {
	ID, Name, Language, DefaultTimezone, Status string
	Version                                     int
}

// Result is a paginated tenant projection.
type Result struct {
	Nodes       []Tenant
	EndCursor   string
	HasNextPage bool
}

// Dependencies are the dependencies of the tenant lookup slice.
type Dependencies struct {
	DB  *gorm.DB
	Bus *mediator.Bus
}

// Setup registers the tenant lookup query in the mediator.
func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil {
		return fmt.Errorf("slice usage/list_llm_usage_tenants: missing dependency")
	}
	return deps.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		return handle(ctx, deps.DB, raw.(Query))
	})
}

func handle(ctx context.Context, db *gorm.DB, q Query) (Result, error) {
	if len(q.Search) > 200 {
		return Result{}, apperror.New(apperror.InvalidInput, "search", "search is too long")
	}
	first, err := graph.PageSize(q.First)
	if err != nil {
		return Result{}, apperror.New(apperror.InvalidInput, "first", "invalid page size")
	}
	var afterName, afterID *string
	if q.After != "" {
		cursor, err := graph.DecodeCursor(q.After)
		if err != nil {
			return Result{}, err
		}
		parsedID, err := identity.ParseID(cursor.ID)
		if err != nil {
			return Result{}, apperror.New(apperror.InvalidInput, "after", "invalid cursor")
		}
		afterName = &cursor.Time
		id := parsedID.String()
		afterID = &id
	}
	var rows []tenantRow
	if err := db.WithContext(ctx).Raw(`SELECT id, name, language, default_timezone, status, version FROM usage.read_llm_usage_tenants(?, ?, ?, ?)`, nullableSearch(q.Search), afterName, afterID, first+1).Scan(&rows).Error; err != nil {
		return Result{}, fmt.Errorf("global LLM usage tenants: %w", err)
	}
	result := Result{Nodes: make([]Tenant, 0, len(rows))}
	if len(rows) > first {
		result.HasNextPage = true
		rows = rows[:first]
	}
	for _, row := range rows {
		result.Nodes = append(result.Nodes, Tenant{ID: row.ID.String(), Name: row.Name, Language: row.Language, DefaultTimezone: row.DefaultTimezone, Status: row.Status, Version: row.Version})
	}
	if len(result.Nodes) > 0 {
		last := result.Nodes[len(result.Nodes)-1]
		result.EndCursor = graph.EncodeCursor(graph.Cursor{Time: last.Name, ID: last.ID})
	}
	return result, nil
}

type tenantRow struct {
	ID                                      identity.ID
	Name, Language, DefaultTimezone, Status string
	Version                                 int
}

func nullableSearch(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}
