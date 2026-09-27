package process_comparison

import (
	"context"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostgresDailySummaryUpsertIsTenantScopedAndAtomic(t *testing.T) {
	adminURL := os.Getenv("INSPECTION_TEST_DATABASE_URL")
	runtimeURL := os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL")
	if adminURL == "" || runtimeURL == "" {
		t.Skip("INSPECTION_TEST_DATABASE_URL and INSPECTION_TEST_RUNTIME_DATABASE_URL are required")
	}
	admin, err := gorm.Open(postgres.Open(adminURL), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	runtimeDB, err := gorm.Open(postgres.Open(runtimeURL), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var definition string
	if err := admin.Raw(`SELECT pg_get_indexdef(indexrelid) FROM pg_index WHERE indexrelid = to_regclass('usage.idx_usage_daily')`).Scan(&definition).Error; err != nil {
		t.Fatal(err)
	}
	if !containsUsageTenantDayIndex(definition) {
		t.Fatalf("usage.daily_summaries must have the (tenant_id, day) unique index; got %q", definition)
	}

	runner := tenanttx.Runner{DB: runtimeDB}
	tenantA, tenantB := identity.NewID(), identity.NewID()
	day := time.Date(2031, 4, 5, 23, 30, 0, 0, time.FixedZone("UTC-3", -3*60*60))
	inputA, outputA, costA := int64(3), int64(4), 0.25
	if err := withinDailyTenant(t, runner, tenantA, llm.StructuredResult{InputTokens: &inputA, OutputTokens: &outputA, Cost: &costA}, day); err != nil {
		t.Fatal(err)
	}
	var originalA database.UsageDailySummary
	if err := runner.Within(context.Background(), tenantA, func(tx *gorm.DB) error {
		return tx.Where("day=?", day.UTC()).First(&originalA).Error
	}); err != nil {
		t.Fatal(err)
	}
	inputB, outputB, costB := int64(8), int64(9), 0.75
	if err := withinDailyTenant(t, runner, tenantB, llm.StructuredResult{InputTokens: &inputB, OutputTokens: &outputB, Cost: &costB}, day); err != nil {
		t.Fatalf("second tenant could not write the same day: %v", err)
	}

	const concurrentWrites = 16
	var wg sync.WaitGroup
	errs := make(chan error, concurrentWrites)
	for i := 0; i < concurrentWrites; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			input, output, cost := int64(2), int64(1), 0.5
			errs <- runner.Within(context.Background(), tenantA, func(tx *gorm.DB) error {
				return upsertDailySummary(context.Background(), tx, tenantA, llm.StructuredResult{InputTokens: &input, OutputTokens: &output, Cost: &cost}, day)
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent summary upsert: %v", err)
		}
	}

	var summaryA, summaryB database.UsageDailySummary
	if err := runner.Within(context.Background(), tenantA, func(tx *gorm.DB) error {
		return tx.Where("day=?", day.UTC()).First(&summaryA).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := runner.Within(context.Background(), tenantB, func(tx *gorm.DB) error {
		return tx.Where("day=?", day.UTC()).First(&summaryB).Error
	}); err != nil {
		t.Fatal(err)
	}
	if summaryA.ID != originalA.ID || summaryA.Requests != concurrentWrites+1 || summaryA.InputTokens != 3+2*concurrentWrites || summaryA.OutputTokens != 4+concurrentWrites || math.Abs(summaryA.Cost-(0.25+0.5*concurrentWrites)) > 1e-9 {
		t.Fatalf("tenant A summary lost increments: %+v", summaryA)
	}
	if summaryB.Requests != 1 || summaryB.InputTokens != 8 || summaryB.OutputTokens != 9 || math.Abs(summaryB.Cost-0.75) > 1e-9 {
		t.Fatalf("tenant B summary changed unexpectedly: %+v", summaryB)
	}
	if summaryA.Day.UTC().Format("2006-01-02") != "2031-04-06" {
		t.Fatalf("day was not grouped in UTC: %s", summaryA.Day)
	}

	secondDay := day.Add(24 * time.Hour)
	if err := withinDailyTenant(t, runner, tenantA, llm.StructuredResult{}, secondDay); err != nil {
		t.Fatal(err)
	}
	var nextDay database.UsageDailySummary
	if err := runner.Within(context.Background(), tenantA, func(tx *gorm.DB) error {
		return tx.Where("day=?", secondDay.UTC()).First(&nextDay).Error
	}); err != nil {
		t.Fatal(err)
	}
	if nextDay.Requests != 1 || nextDay.InputTokens != 0 || nextDay.OutputTokens != 0 || nextDay.Cost != 0 {
		t.Fatalf("missing provider usage should contribute zero: %+v", nextDay)
	}
}

func withinDailyTenant(t *testing.T, runner tenanttx.Runner, tenantID identity.ID, result llm.StructuredResult, now time.Time) error {
	t.Helper()
	return runner.Within(context.Background(), tenantID, func(tx *gorm.DB) error {
		return upsertDailySummary(context.Background(), tx, tenantID, result, now)
	})
}

func containsUsageTenantDayIndex(definition string) bool {
	return strings.Contains(definition, "UNIQUE INDEX idx_usage_daily") && strings.Contains(definition, "(tenant_id, day)")
}
