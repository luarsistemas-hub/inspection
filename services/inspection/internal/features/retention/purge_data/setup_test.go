package purge_data

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestLockInspectionAndCheckLegalHoldBlocksActiveHold(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS inspections").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS retention").Error; err != nil {
		t.Fatal(err)
	}
	if err := createRetentionLockTables(db); err != nil {
		t.Fatal(err)
	}
	tenantID := identity.ID(uuid.New())
	inspectionID := identity.ID(uuid.New())
	inspection := database.Inspection{ID: inspectionID, TenantID: tenantID, BusinessUnitID: identity.ID(uuid.New()), AssetID: identity.ID(uuid.New()), ParticipantID: identity.ID(uuid.New()), TemplateID: identity.ID(uuid.New()), TemplateVersionID: identity.ID(uuid.New()), AnalysisPromptSnapshotID: identity.ID(uuid.New()), Source: "test", SourceKey: uuid.NewString(), Status: "COMPLETED", ReminderInstants: json.RawMessage("[]"), ContextSnapshot: json.RawMessage("{}")}
	if err := db.Create(&inspection).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.LegalHold{ID: identity.ID(uuid.New()), TenantID: tenantID, InspectionID: &inspectionID, Reason: "audit", Active: true}).Error; err != nil {
		t.Fatal(err)
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		return lockInspectionAndCheckLegalHold(tx, tenantID, inspectionID)
	})
	if !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("expected INVALID_STATE for active legal hold, got %v", err)
	}
}

func TestPurgeUnreferencedVideoFramesPreservesReferencedBank(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"media", "analysis", "retention", "origins"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	statements := []string{
		`CREATE TABLE media.video_preparation_jobs (id text, tenant_id text, media_id text)`,
		`CREATE TABLE media.video_evidence_references (id text, tenant_id text, preparation_id text, owner_type text, owner_id text)`,
		`CREATE TABLE analysis.video_plans (id text, tenant_id text, origin_preparation_id text, current_preparation_id text)`,
		`CREATE TABLE retention.legal_holds (id text, tenant_id text, origin_version_id text, active numeric)`,
		`CREATE TABLE origins.origin_versions (id text, tenant_id text, video_preparation_id text, status text DEFAULT 'ACTIVE')`,
		`CREATE TABLE origins.inventory_items (id text, tenant_id text, origin_version_id text, environment_id text, decision text, merged_into_id text)`,
		`CREATE TABLE origins.inventory_environments (id text, tenant_id text, decision text)`,
		`CREATE TABLE origins.inventory_item_evidence (id text, tenant_id text, item_id text, media_id text, frame_id text)`,
		`CREATE TABLE media.video_frames (id text, tenant_id text, preparation_id text, object_key text)`,
		`CREATE TABLE media.video_scope_items (id text, tenant_id text, preparation_id text)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, mediaID := identity.ID(uuid.New()), identity.ID(uuid.New())
	retainedID, purgeableID := identity.ID(uuid.New()), identity.ID(uuid.New())
	for _, preparationID := range []identity.ID{retainedID, purgeableID} {
		if err := db.Exec("INSERT INTO media.video_preparation_jobs (id,tenant_id,media_id) VALUES (?,?,?)", preparationID, tenantID, mediaID).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("INSERT INTO media.video_evidence_references (id,tenant_id,preparation_id,owner_type,owner_id) VALUES (?,?,?,?,?)", identity.NewID(), tenantID, retainedID, "ORIGIN_VERSION", identity.NewID()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO analysis.video_plans (id,tenant_id,current_preparation_id) VALUES (?,?,?)", identity.NewID(), tenantID, purgeableID).Error; err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		preparation identity.ID
		key         string
	}{{retainedID, "frames/retained.jpg"}, {purgeableID, "frames/unreferenced.jpg"}} {
		if err := db.Exec("INSERT INTO media.video_frames (id,tenant_id,preparation_id,object_key) VALUES (?,?,?,?)", identity.NewID(), tenantID, item.preparation, item.key).Error; err != nil {
			t.Fatal(err)
		}
	}
	client := &frameDeleteClient{}
	err = db.Transaction(func(tx *gorm.DB) error {
		return purgeUnreferencedVideoFrames(context.Background(), tx, objectstore.Store{Bucket: "private", Client: client}, tenantID, []identity.ID{mediaID})
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.deleted) != 1 || client.deleted[0] != "frames/unreferenced.jpg" {
		t.Fatalf("deleted frame keys = %v", client.deleted)
	}
	var remaining int64
	if err := db.Table("media.video_frames").Where("preparation_id=?", retainedID).Count(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("referenced frame count = %d, want 1", remaining)
	}
}

func TestPurgeReturnsInvalidStateForLivePlanReference(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"media", "analysis", "retention", "origins"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`CREATE TABLE media.video_preparation_jobs (id text, tenant_id text, media_id text)`,
		`CREATE TABLE media.video_evidence_references (id text, tenant_id text, preparation_id text, owner_type text, owner_id text)`,
		`CREATE TABLE analysis.video_plans (id text, tenant_id text, origin_preparation_id text, current_preparation_id text)`,
		`CREATE TABLE retention.legal_holds (id text, tenant_id text, origin_version_id text, active numeric)`,
		`CREATE TABLE origins.origin_versions (id text, tenant_id text, video_preparation_id text, status text DEFAULT 'ACTIVE')`,
		`CREATE TABLE origins.inventory_items (id text, tenant_id text, origin_version_id text, environment_id text, decision text, merged_into_id text)`,
		`CREATE TABLE origins.inventory_environments (id text, tenant_id text, decision text)`,
		`CREATE TABLE origins.inventory_item_evidence (id text, tenant_id text, item_id text, media_id text, frame_id text)`,
		`CREATE TABLE media.video_frames (id text, tenant_id text, preparation_id text, object_key text)`,
		`CREATE TABLE media.video_scope_items (id text, tenant_id text, preparation_id text)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, mediaID, preparationID, planID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	for _, statement := range []string{
		"INSERT INTO media.video_preparation_jobs VALUES (?,?,?)",
		"INSERT INTO analysis.video_plans (id,tenant_id,current_preparation_id) VALUES (?,?,?)",
		"INSERT INTO media.video_evidence_references VALUES (?,?,?,?,?)",
		"INSERT INTO media.video_frames VALUES (?,?,?,?)",
	} {
		var execErr error
		switch statement {
		case "INSERT INTO media.video_preparation_jobs VALUES (?,?,?)":
			execErr = db.Exec(statement, preparationID, tenantID, mediaID).Error
		case "INSERT INTO analysis.video_plans (id,tenant_id,current_preparation_id) VALUES (?,?,?)":
			execErr = db.Exec(statement, planID, tenantID, preparationID).Error
		case "INSERT INTO media.video_evidence_references VALUES (?,?,?,?,?)":
			execErr = db.Exec(statement, identity.NewID(), tenantID, preparationID, "VIDEO_PLAN", planID).Error
		case "INSERT INTO media.video_frames VALUES (?,?,?,?)":
			execErr = db.Exec(statement, identity.NewID(), tenantID, preparationID, "frames/retained.jpg").Error
		}
		if execErr != nil {
			t.Fatal(execErr)
		}
	}
	client := &frameDeleteClient{}
	err = db.Transaction(func(tx *gorm.DB) error {
		return purgeUnreferencedVideoFrames(context.Background(), tx, objectstore.Store{Bucket: "private", Client: client}, tenantID, []identity.ID{mediaID})
	})
	if !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("live plan dependency error=%v, want INVALID_STATE", err)
	}
	if len(client.deleted) != 0 {
		t.Fatalf("live plan reference deleted objects: %v", client.deleted)
	}
}

func TestPurgeReleasesSealedPlanReferencesAndKeepsPromotedOriginFrames(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"media", "analysis", "reports", "retention", "origins"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`CREATE TABLE reports.report_snapshots (id text, tenant_id text, inspection_id text)`,
		`CREATE TABLE media.video_evidence_references (id text, tenant_id text, preparation_id text, owner_type text, owner_id text)`,
		`CREATE TABLE analysis.video_plans (id text, tenant_id text, inspection_id text, status text)`,
		`CREATE TABLE media.video_preparation_jobs (id text, tenant_id text, media_id text)`,
		`CREATE TABLE retention.legal_holds (id text, tenant_id text, origin_version_id text, active numeric)`,
		`CREATE TABLE origins.origin_versions (id text, tenant_id text, video_preparation_id text, status text DEFAULT 'ACTIVE')`,
		`CREATE TABLE origins.inventory_items (id text, tenant_id text, origin_version_id text, environment_id text, decision text, merged_into_id text)`,
		`CREATE TABLE origins.inventory_environments (id text, tenant_id text, decision text)`,
		`CREATE TABLE origins.inventory_item_evidence (id text, tenant_id text, item_id text, media_id text, frame_id text)`,
		`CREATE TABLE media.video_frames (id text, tenant_id text, preparation_id text, object_key text)`,
		`CREATE TABLE media.video_scope_items (id text, tenant_id text, preparation_id text)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, inspectionID := identity.NewID(), identity.NewID()
	mediaID := identity.NewID()
	sealedPlanID := identity.NewID()
	planPreparationID, originPreparationID := identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO analysis.video_plans VALUES (?,?,?,?)", sealedPlanID, tenantID, inspectionID, "SEALED").Error; err != nil {
		t.Fatal(err)
	}
	for _, preparationID := range []identity.ID{planPreparationID, originPreparationID} {
		if err := db.Exec("INSERT INTO media.video_preparation_jobs VALUES (?,?,?)", preparationID, tenantID, mediaID).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO media.video_frames VALUES (?,?,?,?)", identity.NewID(), tenantID, preparationID, "frames/"+preparationID.String()+".jpg").Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("INSERT INTO media.video_evidence_references VALUES (?,?,?,?,?)", identity.NewID(), tenantID, planPreparationID, "VIDEO_PLAN", sealedPlanID).Error; err != nil {
		t.Fatal(err)
	}
	originID := identity.NewID()
	if err := db.Exec("INSERT INTO origins.origin_versions (id,tenant_id,video_preparation_id) VALUES (?,?,?)", originID, tenantID, originPreparationID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO media.video_evidence_references VALUES (?,?,?,?,?)", identity.NewID(), tenantID, originPreparationID, "ORIGIN_VERSION", originID).Error; err != nil {
		t.Fatal(err)
	}
	if err := releasePurgedReportReferences(db, tenantID, inspectionID); err != nil {
		t.Fatal(err)
	}
	client := &frameDeleteClient{}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return purgeUnreferencedVideoFrames(context.Background(), tx, objectstore.Store{Bucket: "private", Client: client}, tenantID, []identity.ID{mediaID})
	}); err != nil {
		t.Fatal(err)
	}
	if len(client.deleted) != 1 || client.deleted[0] != "frames/"+planPreparationID.String()+".jpg" {
		t.Fatalf("deleted frame keys = %v; want only sealed plan bank", client.deleted)
	}
	var live int64
	if err := db.Table("media.video_evidence_references").Where("preparation_id=? AND owner_type='ORIGIN_VERSION'", originPreparationID).Count(&live).Error; err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Fatalf("promoted origin references = %d, want 1", live)
	}
}

type frameDeleteClient struct{ deleted []string }

func (*frameDeleteClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (*frameDeleteClient) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "", nil
}
func (*frameDeleteClient) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (*frameDeleteClient) AbortMultipart(context.Context, string, string, string) error { return nil }
func (*frameDeleteClient) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{}, nil
}
func (*frameDeleteClient) Get(context.Context, string, string) (io.ReadCloser, error) {
	return nil, nil
}
func (*frameDeleteClient) Put(context.Context, string, string, io.Reader, int64, string) error {
	return nil
}
func (c *frameDeleteClient) Delete(_ context.Context, _, key string) error {
	c.deleted = append(c.deleted, key)
	return nil
}

func TestLockInspectionAndCheckLegalHoldAllowsNoActiveHold(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS inspections").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS retention").Error; err != nil {
		t.Fatal(err)
	}
	if err := createRetentionLockTables(db); err != nil {
		t.Fatal(err)
	}
	tenantID := identity.ID(uuid.New())
	inspectionID := identity.ID(uuid.New())
	inspection := database.Inspection{ID: inspectionID, TenantID: tenantID, BusinessUnitID: identity.ID(uuid.New()), AssetID: identity.ID(uuid.New()), ParticipantID: identity.ID(uuid.New()), TemplateID: identity.ID(uuid.New()), TemplateVersionID: identity.ID(uuid.New()), AnalysisPromptSnapshotID: identity.ID(uuid.New()), Source: "test", SourceKey: uuid.NewString(), Status: "COMPLETED", ReminderInstants: json.RawMessage("[]"), ContextSnapshot: json.RawMessage("{}")}
	if err := db.Create(&inspection).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return lockInspectionAndCheckLegalHold(tx, tenantID, inspectionID)
	}); err != nil {
		t.Fatalf("released legal hold should not block purge: %v", err)
	}
}

func TestLockVideoSourcesAndCheckHoldsProtectsSourceCleanup(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"inspections", "retention", "media"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := createRetentionLockTables(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE media.video_sources (
		id text PRIMARY KEY, tenant_id text NOT NULL, media_id text NOT NULL, status text NOT NULL
	)`).Error; err != nil {
		t.Fatal(err)
	}
	tenantID, mediaID := identity.ID(uuid.New()), identity.ID(uuid.New())
	if err := db.Exec("INSERT INTO media.video_sources (id,tenant_id,media_id,status) VALUES (?,?,?,?)", identity.ID(uuid.New()), tenantID, mediaID, "RETAINED").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO retention.legal_holds (id,tenant_id,source_media_id,reason,active) VALUES (?,?,?,?,?)", identity.ID(uuid.New()), tenantID, mediaID, "hold source", true).Error; err != nil {
		t.Fatal(err)
	}
	err = db.Transaction(func(tx *gorm.DB) error { return lockVideoSourcesAndCheckHolds(tx, tenantID, []identity.ID{mediaID}) })
	if !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("active source hold must return INVALID_STATE, got %v", err)
	}
	if err := db.Exec("UPDATE retention.legal_holds SET active=false").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE media.video_sources SET status='DELETING'").Error; err != nil {
		t.Fatal(err)
	}
	err = db.Transaction(func(tx *gorm.DB) error { return lockVideoSourcesAndCheckHolds(tx, tenantID, []identity.ID{mediaID}) })
	if !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("concurrent source deletion must return INVALID_STATE, got %v", err)
	}
}

func createRetentionLockTables(db *gorm.DB) error {
	if err := db.Exec(`CREATE TABLE inspections.inspections (
		id text PRIMARY KEY, tenant_id text NOT NULL, business_unit_id text NOT NULL,
		asset_id text NOT NULL, participant_id text NOT NULL, template_id text NOT NULL,
		 template_version_id text NOT NULL, analysis_prompt_snapshot_id text NOT NULL, inventory_prompt_snapshot_id text,
		project_id text, stage_id text, source text NOT NULL, source_key text NOT NULL, media_kind text NOT NULL DEFAULT 'PHOTO',
		source_reason text, state_reason text, status text NOT NULL, active_video_plan_id text,
		evidence_count integer NOT NULL DEFAULT 0, due_at datetime NOT NULL,
		deadline_at datetime NOT NULL, reminder_instants blob NOT NULL,
		context_snapshot blob NOT NULL, version integer NOT NULL DEFAULT 1,
		created_at datetime NOT NULL, updated_at datetime
	)`).Error; err != nil {
		return err
	}
	return db.Exec(`CREATE TABLE retention.legal_holds (
		id text PRIMARY KEY, tenant_id text NOT NULL, inspection_id text,
		source_media_id text, origin_version_id text, reason text NOT NULL, active numeric NOT NULL DEFAULT 1,
		created_at datetime, released_at datetime
	)`).Error
}
