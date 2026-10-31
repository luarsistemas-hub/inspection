package llm_usage_by_inspection

import (
	"context"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
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

func TestHandleRejectsInvalidFilters(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	tenantID := identity.NewID()
	for _, q := range []Query{
		{TenantID: tenantID, State: "DONE", First: 1},
		{TenantID: tenantID, Cost: "nonsense", First: 1},
		{TenantID: tenantID, Outcome: strings.Repeat("x", 201), First: 1},
		{TenantID: tenantID, After: graph.EncodeCursor(graph.Cursor{Time: "SEM_VISTORIA", ID: "not-a-uuid"}), First: 1},
	} {
		if _, err := handle(context.Background(), db, q); err == nil {
			t.Errorf("accepted invalid query %+v", q)
		}
	}
}

func TestHandleValidatesTenantWindowAndDependencyFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle(context.Background(), db, Query{First: 25}); err == nil {
		t.Fatal("missing aggregate function did not fail")
	}
	if _, err := handle(context.Background(), db, Query{TenantID: identity.NewID(), First: -1}); err == nil {
		t.Fatal("invalid page size accepted")
	}
	now := time.Now().UTC()
	if _, err := handle(context.Background(), db, Query{TenantID: identity.NewID(), From: now, To: now, First: 25}); err == nil {
		t.Fatal("invalid window accepted")
	}
	bus := mediator.New()
	if err := Setup(Dependencies{DB: db, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Ask(context.Background(), Query{}); err == nil {
		t.Fatal("missing tenant accepted")
	}
}
