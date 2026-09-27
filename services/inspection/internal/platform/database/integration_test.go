package database

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database/migrations"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostgresMigrationAndRLSIT341ToIT349(t *testing.T) {
	dsn := os.Getenv("INSPECTION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("INSPECTION_TEST_DATABASE_URL not set")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	m := Migrator{DB: admin}
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(ctx); err != nil {
		t.Fatalf("retry: %v", err)
	}
	var usageIndex string
	if err := admin.Raw(`SELECT pg_get_indexdef(indexrelid) FROM pg_index WHERE indexrelid = to_regclass('usage.idx_usage_daily')`).Scan(&usageIndex).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(usageIndex, "(tenant_id, day)") {
		t.Fatalf("usage daily index is not tenant scoped: %s", usageIndex)
	}
	if err := Compatible(ctx, admin, 13, migrations.LatestVersion()); err != nil {
		t.Fatal(err)
	}
	assertLegacyUsageDailyIndexMigration(t, ctx, admin, m)
	var task5Indexes int64
	if err := admin.Raw(`SELECT count(*) FROM pg_indexes WHERE indexname IN ('idx_capture_draft_responsibility','idx_screening_media','idx_recapture_inspection','idx_recapture_one_active')`).Scan(&task5Indexes).Error; err != nil || task5Indexes != 4 {
		t.Fatalf("task 5 indexes=%d err=%v", task5Indexes, err)
	}
	var immutableAnswerTrigger int64
	if err := admin.Raw(`SELECT count(*) FROM pg_trigger WHERE tgname='immutable_submitted_answer' AND NOT tgisinternal`).Scan(&immutableAnswerTrigger).Error; err != nil || immutableAnswerTrigger != 1 {
		t.Fatalf("immutable answer trigger=%d err=%v", immutableAnswerTrigger, err)
	}
	var forced, bypass bool
	var owner string
	if err := admin.Raw(`SELECT c.relforcerowsecurity, pg_get_userbyid(c.relowner), r.rolbypassrls FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.rolname='inspection_runtime' WHERE n.nspname='tenancy' AND c.relname='tenants'`).Row().Scan(&forced, &owner, &bypass); err != nil {
		t.Fatal(err)
	}
	if !forced || owner == "inspection_runtime" || bypass {
		t.Fatalf("unsafe role forced=%v owner=%s bypass=%v", forced, owner, bypass)
	}
	if err := admin.Exec(`ALTER ROLE inspection_runtime LOGIN PASSWORD 'runtime-test'`).Error; err != nil {
		t.Fatal(err)
	}
	runtimeDSN := os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL")
	if runtimeDSN == "" {
		t.Fatal("INSPECTION_TEST_RUNTIME_DATABASE_URL not set")
	}
	runtimeDB, err := gorm.Open(postgres.Open(runtimeDSN), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	tenants := []identity.ID{identity.NewID(), identity.NewID()}
	for _, id := range tenants {
		row := Tenant{ID: id, TenantID: id, Name: "tenant", Language: "pt-BR", DefaultTimezone: "America/Sao_Paulo", Status: "ACTIVE", Version: 1}
		if err := admin.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := runtimeDB.Model(&Tenant{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("missing context saw %d", count)
	}
	runner := tenanttx.Runner{DB: runtimeDB}
	if err := runner.Within(ctx, tenants[0], func(tx *gorm.DB) error { return tx.Model(&Tenant{}).Count(&count).Error }); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("tenant A saw %d rows", count)
	}
	draftA := CaptureDraft{ID: identity.NewID(), TenantID: tenants[0], ResponsibilityID: identity.NewID(), Kind: "INSPECTION", ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1}
	draftB := CaptureDraft{ID: identity.NewID(), TenantID: tenants[1], ResponsibilityID: identity.NewID(), Kind: "INSPECTION", ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1}
	for _, draft := range []CaptureDraft{draftA, draftB} {
		if err := admin.Create(&draft).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := runner.Within(ctx, tenants[0], func(tx *gorm.DB) error {
		var visible int64
		if err := tx.Model(&CaptureDraft{}).Count(&visible).Error; err != nil {
			return err
		}
		if visible != 1 {
			return fmt.Errorf("tenant A saw %d capture drafts", visible)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := admin.Model(&CaptureDraft{}).Where("id=?", draftA.ID).Update("policy_payload", []byte(`{"gpsRequired":true}`)).Error; err == nil {
		t.Fatal("capture policy snapshot was mutable")
	}
	foreignMedia := MediaObject{ID: identity.NewID(), TenantID: tenants[1], ResponsibilityID: draftB.ResponsibilityID, ObjectKey: "tenant-b/private/" + identity.NewID().String(), ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 1, Status: "UPLOADING", Flags: []byte(`[]`), IdempotencyKey: identity.NewID().String()}
	if err := admin.Create(&foreignMedia).Error; err != nil {
		t.Fatal(err)
	}
	if err := runner.Within(ctx, tenants[0], func(tx *gorm.DB) error {
		var visible int64
		if err := tx.Model(&MediaObject{}).Count(&visible).Error; err != nil {
			return err
		}
		if visible != 0 {
			return fmt.Errorf("tenant A saw %d media objects", visible)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	other := Tenant{ID: identity.NewID(), TenantID: tenants[1], Name: "blocked", Language: "pt-BR", DefaultTimezone: "UTC", Status: "ACTIVE", Version: 1}
	if err := runner.Within(ctx, tenants[0], func(tx *gorm.DB) error { return tx.Create(&other).Error }); err == nil {
		t.Fatal("cross-tenant insert accepted")
	}
}

func assertLegacyUsageDailyIndexMigration(t *testing.T, ctx context.Context, admin *gorm.DB, migrator Migrator) {
	t.Helper()
	day := time.Date(2001, 2, 3, 0, 0, 0, 0, time.UTC)
	seed := UsageDailySummary{
		ID:       identity.NewDeterministicID("usage-daily-migration-test", "summary"),
		TenantID: identity.NewDeterministicID("usage-daily-migration-test", "tenant"),
		Day:      day, Requests: 7, InputTokens: 11, OutputTokens: 13, Cost: 0.25, UpdatedAt: time.Now().UTC(),
	}
	t.Cleanup(func() {
		if err := admin.Where("tenant_id=?", seed.TenantID).Delete(&UsageDailySummary{}).Error; err != nil {
			t.Errorf("cleanup usage summary seed: %v", err)
		}
		// Reapplies version 47 when the test stops before the migrator restores the index.
		if err := migrator.Migrate(ctx); err != nil {
			t.Errorf("restore usage summary index: %v", err)
		}
	})
	if err := admin.Exec(`DELETE FROM platform.schema_migrations WHERE version=47`).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Where("tenant_id=?", seed.TenantID).Delete(&UsageDailySummary{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec(`DROP INDEX usage.idx_usage_daily`).Error; err != nil {
		t.Fatal(err)
	}
	// Scoped to the seed tenant so rows written by other tests cannot collide on the legacy day-only key.
	if err := admin.Exec(fmt.Sprintf(`CREATE UNIQUE INDEX idx_usage_daily ON usage.daily_summaries(day) WHERE tenant_id = '%s'`, seed.TenantID)).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Create(&seed).Error; err != nil {
		t.Fatalf("seed legacy usage summary: %v", err)
	}
	if err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("apply tenant-scoped usage summary migration: %v", err)
	}
	var preserved UsageDailySummary
	if err := admin.Where("tenant_id=? AND day=?", seed.TenantID, day).First(&preserved).Error; err != nil {
		t.Fatalf("read preserved usage summary: %v", err)
	}
	if preserved.ID != seed.ID || preserved.Requests != seed.Requests || preserved.InputTokens != seed.InputTokens || preserved.OutputTokens != seed.OutputTokens || preserved.Cost != seed.Cost {
		t.Fatalf("legacy usage summary changed during index migration: got %+v want %+v", preserved, seed)
	}
}
