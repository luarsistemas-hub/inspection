//go:build integration

package database_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/database/migrations"
)

// preVideoSchemaVersion is the last catalog step before the video feature.
const preVideoSchemaVersion = 53

// TestVV_IT_356VideoFeatureMigrationPreservesExistingPhotoRecords upgrades a
// database that the pre-video (v53) code migrated and seeded. It runs only
// through the upgrade script, which exports INSPECTION_TEST_V53_FIXTURE.
func TestVV_IT_356VideoFeatureMigrationPreservesExistingPhotoRecords(t *testing.T) {
	raw := os.Getenv("INSPECTION_TEST_V53_FIXTURE")
	if raw == "" || os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" {
		t.Skip("requires a v53 database seeded by the upgrade script")
	}
	var fixture struct {
		TenantID     string `json:"tenantId"`
		InspectionID string `json:"inspectionId"`
		DraftID      string `json:"draftId"`
		MediaID      string `json:"mediaId"`
	}
	if err := json.Unmarshal([]byte(raw), &fixture); err != nil || fixture.TenantID == "" {
		t.Fatalf("invalid v53 fixture %q: %v", raw, err)
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	db := h.MigrationDB
	version := func() int {
		t.Helper()
		var latest int
		if err := db.Raw("SELECT COALESCE(MAX(version),0) FROM platform.schema_migrations").Scan(&latest).Error; err != nil {
			t.Fatal(err)
		}
		return latest
	}
	if got := version(); got != preVideoSchemaVersion {
		t.Fatalf("starting schema version=%d, want pre-video %d", got, preVideoSchemaVersion)
	}
	type row struct {
		ID, Status, Hash string
		Version          int64
	}
	tables := map[string]string{
		"inspections":      "inspections.inspections",
		"responsibilities": "inspections.responsibilities",
		"drafts":           "capture.capture_drafts",
		"media":            "media.media_objects",
		"submissions":      "capture.submission_versions",
	}
	type state struct {
		counts                 map[string]int64
		inspection, draft, med row
		requirements           string
	}
	// Only columns that exist in both schemas are read.
	take := func() state {
		t.Helper()
		s := state{counts: map[string]int64{}}
		for name, table := range tables {
			var count int64
			if err := db.Table(table).Where("tenant_id=?", fixture.TenantID).Count(&count).Error; err != nil {
				t.Fatalf("count %s: %v", table, err)
			}
			s.counts[name] = count
		}
		if err := db.Raw("SELECT id::text AS id, status, '' AS hash, version FROM inspections.inspections WHERE tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Scan(&s.inspection).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Raw("SELECT id::text AS id, status, '' AS hash, version FROM capture.capture_drafts WHERE tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Scan(&s.draft).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Raw("SELECT requirements::text FROM capture.capture_drafts WHERE tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Scan(&s.requirements).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Raw("SELECT id::text AS id, status, sha256 AS hash, 0 AS version FROM media.media_objects WHERE tenant_id=? AND id=?", fixture.TenantID, fixture.MediaID).Scan(&s.med).Error; err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := take()
	if before.inspection.ID == "" || before.draft.ID == "" || before.med.ID == "" {
		t.Fatalf("v53 fixture rows missing: %+v", before)
	}
	if err := (database.Migrator{DB: db}).Migrate(ctx); err != nil {
		t.Fatalf("upgrade v53 records to the video feature schema: %v", err)
	}
	if got := version(); got != migrations.LatestVersion() {
		t.Fatalf("schema version after upgrade=%d, want %d", got, migrations.LatestVersion())
	}
	after := take()
	for name, count := range before.counts {
		if after.counts[name] != count {
			t.Errorf("%s rows %d -> %d after upgrade", name, count, after.counts[name])
		}
	}
	if after.inspection != before.inspection || after.draft != before.draft || after.med != before.med || after.requirements != before.requirements {
		t.Errorf("upgrade rewrote existing records: before=%+v after=%+v", before, after)
	}
	var kinds struct{ Inspection, Draft, Media string }
	if err := db.Raw(`SELECT
		(SELECT media_kind FROM inspections.inspections WHERE tenant_id=? AND id=?) AS inspection,
		(SELECT media_kind FROM capture.capture_drafts WHERE tenant_id=? AND id=?) AS draft,
		(SELECT media_kind FROM media.media_objects WHERE tenant_id=? AND id=?) AS media`,
		fixture.TenantID, fixture.InspectionID, fixture.TenantID, fixture.DraftID, fixture.TenantID, fixture.MediaID).Scan(&kinds).Error; err != nil {
		t.Fatal(err)
	}
	if kinds.Inspection != "PHOTO" || kinds.Draft != "PHOTO" || kinds.Media != "PHOTO" {
		t.Errorf("existing records modality after upgrade=%+v; want PHOTO without conversion", kinds)
	}
	var videoRows int64
	if err := db.Raw(`SELECT (SELECT count(*) FROM capture.video_attempts WHERE tenant_id=?) + (SELECT count(*) FROM media.video_preparation_jobs WHERE tenant_id=?)`, fixture.TenantID, fixture.TenantID).Scan(&videoRows).Error; err != nil {
		t.Fatal(err)
	}
	if videoRows != 0 {
		t.Errorf("upgrade created %d video rows for existing PHOTO records", videoRows)
	}
}
