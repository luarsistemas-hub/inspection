//go:build integration

package database

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

func TestTask01ScopeSnapshotMigrationRLSAndSchemaVersion118(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	admin, err := Open(os.Getenv("INSPECTION_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	runtimeDB, err := Open(os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := admin.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	if sqlDB, err := runtimeDB.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	if err := (Migrator{DB: admin}).Migrate(ctx); err != nil {
		t.Fatalf("apply test database migrations: %v", err)
	}
	var enabled, forced bool
	var policy string
	if err := admin.Raw(`SELECT c.relrowsecurity,c.relforcerowsecurity,coalesce(p.polname,'') FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_policy p ON p.polrelid=c.oid AND p.polname='tenant_isolation' WHERE n.nspname='inspections' AND c.relname='scope_snapshots'`).Row().Scan(&enabled, &forced, &policy); err != nil {
		t.Fatal(err)
	}
	if !enabled || !forced || policy != "tenant_isolation" {
		t.Fatalf("scope snapshot migration RLS=(%v,%v,%q)", enabled, forced, policy)
	}
	var definition string
	if err := admin.Raw(`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='templates.template_versions'::regclass AND conname='chk_template_schema_version'`).Scan(&definition).Error; err != nil {
		t.Fatal(err)
	}
	var latest int
	if err := admin.Raw("SELECT MAX(version) FROM platform.schema_migrations").Scan(&latest).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(definition, "3") || latest < 118 {
		t.Fatalf("migration 118 schema-version constraint=%q, latest=%d", definition, latest)
	}
	tenantA, tenantB := identity.NewID(), identity.NewID()
	row := InspectionScopeSnapshot{ID: identity.NewID(), TenantID: tenantB, InspectionID: identity.NewID(), TemplateVersionID: identity.NewID(), Profile: "PROPERTY_COMPONENTS", Document: []byte(`{"rooms":[]}`), Digest: strings.Repeat("a", 64), Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := admin.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := (tenanttx.Runner{DB: runtimeDB}).Within(ctx, tenantA, func(tx *gorm.DB) error {
		return tx.Model(&InspectionScopeSnapshot{}).Where("id=?", row.ID).Count(&count).Error
	}); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("tenant A read %d tenant B scope snapshots", count)
	}
}
