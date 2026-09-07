package database

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"inspection/libs/identity"
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
	if err := Compatible(ctx, admin, 11, 15); err != nil {
		t.Fatal(err)
	}
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
