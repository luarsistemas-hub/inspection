//go:build integration

package database

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestMigratorUpgradeFromV78WithLegacyRows upgrades a database built by the
// previous release migrator and verifies preservation of representative rows.
func TestMigratorUpgradeFromV78WithLegacyRows(t *testing.T) {
	url := os.Getenv("INSPECTION_TEST_UPGRADE_DATABASE_URL")
	if url == "" {
		t.Skip("fresh disposable v78 upgrade database URL is required")
	}
	db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var maxBefore int
	if err := db.Raw(`SELECT MAX(version) FROM platform.schema_migrations`).Scan(&maxBefore).Error; err != nil || maxBefore != 78 {
		t.Fatalf("upgrade database max version=%d, want 78 (is this a database built by the previous release migrator?): %v", maxBefore, err)
	}
	var retentionAnchorColumns int64
	if err := db.Raw(`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema='media' AND table_name='video_sources' AND column_name='retention_anchor'`).Scan(&retentionAnchorColumns).Error; err != nil || retentionAnchorColumns != 0 {
		t.Fatalf("v78 must not contain retention_anchor, found %d columns: %v", retentionAnchorColumns, err)
	}
	var legacyPrompt struct {
		ID              identity.ID
		DefinitionJSON  json.RawMessage
		CanonicalDigest string
		Revision        int64
		UpdatedBy       identity.ID
		CreatedAt       time.Time
		UpdatedAt       time.Time
	}
	if err := db.Raw(`SELECT id,definition_json,canonical_digest,revision,updated_by,created_at,updated_at FROM analysis.prompts WHERE analysis_type='REAL_ESTATE'`).Scan(&legacyPrompt).Error; err != nil {
		t.Fatal(err)
	}
	if legacyPrompt.ID == (identity.ID{}) {
		t.Fatal("v78 REAL_ESTATE prompt seed is missing")
	}
	legacyDigest := strings.Repeat("e", 64)
	legacyPrompt.Revision = 4
	if err := db.Exec(`UPDATE analysis.prompts SET canonical_digest=?,revision=4,updated_at=now() WHERE id=?`, legacyDigest, legacyPrompt.ID).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tenantID, promptID, snapshotID := identity.NewID(), legacyPrompt.ID, identity.NewID()
	if err := db.Exec(`INSERT INTO tenancy.tenants(id,tenant_id,name,language,default_timezone,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, tenantID, tenantID, "Upgrade fixture", "pt-BR", "America/Sao_Paulo", "ACTIVE", now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO analysis.prompt_snapshots(id,analysis_type,definition_json,canonical_digest,created_at) VALUES(?,?,?,?,?)`, snapshotID, "REAL_ESTATE", legacyPrompt.DefinitionJSON, legacyDigest, now).Error; err != nil {
		t.Fatal(err)
	}
	jobID, runID, findingID := identity.NewID(), identity.NewID(), identity.NewID()
	inspectionID, mediaID, readyMediaID := identity.NewID(), identity.NewID(), identity.NewID()
	sourceID, legacyReadySourceID := identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO analysis.comparison_jobs(id,tenant_id,inspection_id,prompt_snapshot_id,requirement_key,model_alias,input_digest,prompt_digest,status,attempts,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, jobID, tenantID, inspectionID, snapshotID, "legacy:room", "legacy-model", "input", "prompt", "SUCCEEDED", 1, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO analysis.analysis_runs(id,tenant_id,job_id,prompt_snapshot_id,provider,model,gateway_request_id,input_digest,prompt_digest,output,latency_ms,validation_outcome,created_at) VALUES(?,?,?,?,?,?,?,?,?,?::jsonb,?,?,?)`, runID, tenantID, jobID, snapshotID, "fixture", "fixture", "upgrade", "input", "prompt", `{}`, 1, "ACCEPTED", now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO analysis.findings(id,tenant_id,analysis_run_id,category,title,description,severity,quality,recommended_action,confidence,evidence,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?::jsonb,?)`, findingID, tenantID, runID, "DAMAGE", "Legacy", "Legacy finding", "LOW", "CLEAR", "Inspect", .9, `[]`, now).Error; err != nil {
		t.Fatal(err)
	}
	for _, mediaRow := range []struct{ id, key, hash string }{{mediaID.String(), "upgrade/", strings.Repeat("a", 64)}, {readyMediaID.String(), "upgrade/ready/", strings.Repeat("b", 64)}} {
		if err := db.Exec(`INSERT INTO media.media_objects(id,tenant_id,responsibility_id,object_key,content_type,sha256,size_bytes,status,media_kind,idempotency_key,created_at) VALUES(?::uuid,?,?,?,?,?,1,'READY','VIDEO',?,?)`, mediaRow.id, tenantID, identity.NewID(), mediaRow.key+mediaRow.id, "video/mp4", mediaRow.hash, "upgrade-"+mediaRow.id, now).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`INSERT INTO media.video_sources(id,tenant_id,media_id,container,codec,pixel_format,color_transfer,duration_micros,width,height,frame_rate,screening_status,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, sourceID, tenantID, mediaID, "mp4", "h264", "yuv420p", "bt709", 1, 1, 1, 30, "APPROVED", "RETAINED", now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO media.video_sources(id,tenant_id,media_id,container,codec,pixel_format,color_transfer,duration_micros,width,height,frame_rate,screening_status,first_frame_ready_at,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, legacyReadySourceID, tenantID, readyMediaID, "mp4", "h264", "yuv420p", "bt709", 1, 1, 1, 30, "APPROVED", now, "RETAINED", now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO media.video_policies(tenant_id,max_duration_minutes,origin_retention_days,current_retention_days,version,last_mutation_id,last_payload_digest,updated_at) VALUES(?,?,?,?,?,?,?,?)`, tenantID, 15, 30, 7, 2, "", "", now).Error; err != nil {
		t.Fatal(err)
	}
	templateID, templateVersionID := identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO templates.templates(id,tenant_id,key,name,segment_version_id,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, templateID, tenantID, "legacy", "Legacy", identity.NewID(), 1, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO templates.template_versions(id,tenant_id,template_id,version_number,schema_version,definition_json,canonical_digest,status,idempotency_key,published_at,created_by) VALUES(?,?,?,?,?,?::jsonb,?,?,?, ?,?)`, templateVersionID, tenantID, templateID, 1, 1, `{}`, "legacy-template", "ACTIVE", "legacy-v1", now, identity.NewID()).Error; err != nil {
		t.Fatal(err)
	}

	migrator := Migrator{DB: db}
	if err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("upgrade v78 to v85: %v", err)
	}
	if err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("idempotent second upgrade: %v", err)
	}
	var maxVersion int
	if err := db.Raw(`SELECT MAX(version) FROM platform.schema_migrations`).Scan(&maxVersion).Error; err != nil {
		t.Fatal(err)
	}
	if maxVersion != 86 {
		t.Fatalf("max migration=%d, want 86", maxVersion)
	}
	assertVideoPlanPreparationCatalog(t, db)
	var upgradedFinding FindingRecord
	if err := db.First(&upgradedFinding, "id=?", findingID).Error; err != nil {
		t.Fatal(err)
	}
	if upgradedFinding.ComparisonJobID == nil || *upgradedFinding.ComparisonJobID != jobID || upgradedFinding.InventoryJobID != nil {
		t.Fatalf("legacy finding backfill=%+v", upgradedFinding)
	}
	badWriterRow := FindingRecord{ID: identity.NewID(), TenantID: tenantID, AnalysisRunID: runID, Category: "DAMAGE", Title: "Missing job", Description: "Legacy writer shape", Severity: "LOW", Quality: "CLEAR", RecommendedAction: "Inspect", Confidence: .9, Evidence: json.RawMessage(`[]`), CreatedAt: now}
	if err := db.Create(&badWriterRow).Error; err == nil || !strings.Contains(err.Error(), "chk_finding_one_job") {
		t.Fatalf("legacy finding without job id error=%v", err)
	}
	comparisonJobID := jobID
	writerRow := badWriterRow
	writerRow.ID, writerRow.ComparisonJobID = identity.NewID(), &comparisonJobID
	if err := db.Create(&writerRow).Error; err != nil {
		t.Fatalf("comparison finding writer shape rejected: %v", err)
	}
	var findingsTask AnalysisPrompt
	if err := db.Where("analysis_type=? AND task='FINDINGS'", "REAL_ESTATE").First(&findingsTask).Error; err != nil {
		t.Fatal(err)
	}
	if findingsTask.ID != promptID || findingsTask.Revision != 4 || findingsTask.CanonicalDigest != legacyDigest {
		t.Fatalf("legacy FINDINGS prompt changed during migration: id=%s revision=%d digest=%s", findingsTask.ID, findingsTask.Revision, findingsTask.CanonicalDigest)
	}
	var legacySnapshot AnalysisPromptSnapshot
	if err := db.Where("id=?", snapshotID).First(&legacySnapshot).Error; err != nil || legacySnapshot.Task != "FINDINGS" {
		t.Fatalf("legacy prompt snapshot task=%q err=%v", legacySnapshot.Task, err)
	}
	var sourceAfter VideoSource
	if err := db.First(&sourceAfter, "id=?", sourceID).Error; err != nil {
		t.Fatal(err)
	}
	if sourceAfter.RetentionAnchor != "FIXED" || sourceAfter.FirstFrameReadyAt != nil || sourceAfter.SourceExpiryAt != nil {
		t.Fatalf("legacy pre-ready source changed: %+v", sourceAfter)
	}
	var legacyReadyAfter VideoSource
	if err := db.First(&legacyReadyAfter, "id=?", legacyReadySourceID).Error; err != nil {
		t.Fatal(err)
	}
	wantExpiry := now.Add(30 * 24 * time.Hour)
	if legacyReadyAfter.RetentionAnchor != "FIXED" || legacyReadyAfter.RetentionDays == nil || *legacyReadyAfter.RetentionDays != 30 || legacyReadyAfter.SourceExpiryAt == nil || !legacyReadyAfter.SourceExpiryAt.Equal(wantExpiry) {
		t.Fatalf("legacy ready source backfill=%+v, want expiry %s without shortening retained data", legacyReadyAfter, wantExpiry)
	}
	var policyAfter VideoPolicy
	if err := db.First(&policyAfter, "tenant_id=?", tenantID).Error; err != nil {
		t.Fatal(err)
	}
	if policyAfter.CurrentRetentionDays != 7 || policyAfter.SupersededBaselineRetentionDays != 365 {
		t.Fatalf("stored policy values changed: %+v", policyAfter)
	}
	policyAfter.CurrentRetentionDays, policyAfter.Version = 8, policyAfter.Version+1
	if err := db.Save(&policyAfter).Error; err != nil {
		t.Fatalf("video policy update rejects retained v84 field: %v", err)
	}
	var savedPolicy VideoPolicy
	if err := db.First(&savedPolicy, "tenant_id=?", tenantID).Error; err != nil || savedPolicy.SupersededBaselineRetentionDays != 365 {
		t.Fatalf("video policy update lost superseded retention: %+v err=%v", savedPolicy, err)
	}
	var versionAfter TemplateVersion
	if err := db.First(&versionAfter, "id=?", templateVersionID).Error; err != nil {
		t.Fatal(err)
	}
	if versionAfter.SchemaVersion != 1 {
		t.Fatalf("legacy template version changed to schema %d", versionAfter.SchemaVersion)
	}
}
