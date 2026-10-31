package llm_usage_by_tenant

import (
	"context"
	"strings"
	"testing"
	"time"

	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupValidatesDependencies(t *testing.T) {
	if Setup(Dependencies{}) == nil {
		t.Fatal("missing dependencies accepted")
	}
	if Setup(Dependencies{DB: &gorm.DB{}}) == nil {
		t.Fatal("missing bus accepted")
	}
}

func TestHandleRejectsInvalidFiltersAndTenantCursor(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []Query{
		{State: "DONE", First: 1},
		{Cost: "nonsense", First: 1},
		{Provider: strings.Repeat("x", 201), First: 1},
		{After: graph.EncodeCursor(graph.Cursor{Time: "tenant", ID: "not-a-uuid"}), First: 1},
	} {
		if _, err := handle(context.Background(), db, q); err == nil {
			t.Errorf("accepted invalid query %+v", q)
		}
	}
}

func TestHandleRejectsInvalidWindowAndReturnsDependencyFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle(context.Background(), db, Query{First: 25}); err == nil {
		t.Fatal("missing aggregate function did not fail")
	}
	now := time.Now().UTC()
	if _, err := handle(context.Background(), db, Query{From: now, To: now, First: 25}); err == nil {
		t.Fatal("invalid window accepted")
	}
	bus := mediator.New()
	if err := Setup(Dependencies{DB: db, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Ask(context.Background(), Query{First: -1}); err == nil {
		t.Fatal("invalid page size accepted")
	}
}
