//go:build integration

package database

import (
	"context"
	"os"
	"testing"
)

func TestTask03ImageTruncationMigrationAddsDiagnosticColumnIT(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL URL is required")
	}
	ctx := context.Background()
	db, err := Open(os.Getenv("INSPECTION_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	if err := (Migrator{DB: db}).Migrate(ctx); err != nil {
		t.Fatalf("apply disposable test database migrations: %v", err)
	}
	var count int64
	if err := db.Raw(`SELECT count(*) FROM information_schema.columns WHERE table_schema='analysis' AND table_name='inventory_jobs' AND column_name='candidate_image_truncated'`).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("candidate_image_truncated column count=%d", count)
	}
	var latest int
	if err := db.Raw("SELECT MAX(version) FROM platform.schema_migrations").Scan(&latest).Error; err != nil {
		t.Fatal(err)
	}
	if latest < 126 {
		t.Fatalf("applied schema version=%d, want at least 126", latest)
	}
	var notAssessedNullable, duplicateIndex int64
	if err := db.Raw(`SELECT count(*) FROM information_schema.columns WHERE table_schema='origins' AND table_name='inventory_items' AND column_name='not_assessed' AND is_nullable='NO' AND column_default LIKE '%[]%'`).Scan(&notAssessedNullable).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(`SELECT count(*) FROM pg_indexes WHERE schemaname='origins' AND indexname='idx_inventory_items_component_key'`).Scan(&duplicateIndex).Error; err != nil {
		t.Fatal(err)
	}
	if notAssessedNullable != 1 || duplicateIndex != 0 {
		t.Fatalf("inventory schema notAssessed=%d duplicate component index=%d", notAssessedNullable, duplicateIndex)
	}
}
