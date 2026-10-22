package prepare_video_comparisons

import (
	"context"
	"errors"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func sqliteFrameReaderDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"media", "inspections", "origins"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		"CREATE TABLE media.video_preparation_jobs (id TEXT, tenant_id TEXT, media_id TEXT, status TEXT, manifest_digest TEXT, generation INTEGER)",
		"CREATE TABLE media.media_objects (id TEXT, tenant_id TEXT, responsibility_id TEXT)",
		"CREATE TABLE inspections.responsibilities (id TEXT, tenant_id TEXT, inspection_id TEXT)",
		"CREATE TABLE inspections.inspections (id TEXT, tenant_id TEXT, asset_id TEXT)",
		"CREATE TABLE origins.origins (id TEXT, tenant_id TEXT, asset_id TEXT)",
		"CREATE TABLE origins.origin_versions (id TEXT, tenant_id TEXT, origin_id TEXT, responsibility_id TEXT)",
		"CREATE TABLE media.video_frames (id TEXT, tenant_id TEXT, source_media_id TEXT, preparation_id TEXT, pts_us INTEGER, sha256 TEXT, object_key TEXT, size_bytes INTEGER, quality TEXT)",
		"CREATE TABLE media.video_scope_items (id TEXT, tenant_id TEXT, preparation_id TEXT, frame_id TEXT, environment_candidate TEXT, component_label TEXT, view_label TEXT, visibility TEXT, identity_status TEXT, landmarks BLOB, created_at DATETIME)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create SQLite fixture table: %v", err)
		}
	}
	return db
}

func TestDBFrameReaderLoadsReadyTenantFramesAndRejectsWrongScopeOrState(t *testing.T) {
	db := sqliteFrameReaderDB(t)
	tenant, otherTenant := testID("reader-tenant"), testID("reader-other-tenant")
	prep, mediaID, responsibilityID, inspectionID := testID("reader-prep"), testID("reader-media"), testID("reader-responsibility"), testID("reader-inspection")
	frameID, scopeID := testID("reader-frame"), testID("reader-scope")
	job := database.VideoPreparationJob{ID: prep, TenantID: tenant, MediaID: mediaID, Status: "READY", ManifestDigest: digest, Generation: 3}
	media := database.MediaObject{ID: mediaID, TenantID: tenant, ResponsibilityID: responsibilityID, ObjectKey: "video/source"}
	responsibility := database.Responsibility{ID: responsibilityID, TenantID: tenant, InspectionID: inspectionID}
	inspection := database.Inspection{ID: inspectionID, TenantID: tenant, AssetID: testID("reader-asset")}
	frame := database.VideoFrame{ID: frameID, TenantID: tenant, SourceMediaID: mediaID, PreparationID: prep, PTSMicros: 123456, SHA256: digest, ObjectKey: "video/frame", SizeBytes: 9, Quality: "LOW_QUALITY"}
	scope := database.VideoScopeItem{ID: scopeID, TenantID: tenant, PreparationID: prep, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "front", Visibility: "VISIBLE", IdentityStatus: "AMBIGUOUS"}
	statements := []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO media.video_preparation_jobs VALUES (?, ?, ?, ?, ?, ?)", []any{job.ID.String(), tenant.String(), mediaID.String(), job.Status, job.ManifestDigest, job.Generation}},
		{"INSERT INTO media.media_objects VALUES (?, ?, ?)", []any{media.ID.String(), tenant.String(), responsibilityID.String()}},
		{"INSERT INTO inspections.responsibilities VALUES (?, ?, ?)", []any{responsibility.ID.String(), tenant.String(), inspectionID.String()}},
		{"INSERT INTO inspections.inspections VALUES (?, ?, ?)", []any{inspection.ID.String(), tenant.String(), inspection.AssetID.String()}},
		{"INSERT INTO media.video_frames VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)", []any{frame.ID.String(), tenant.String(), mediaID.String(), prep.String(), frame.PTSMicros, frame.SHA256, frame.ObjectKey, frame.SizeBytes, frame.Quality}},
		{"INSERT INTO media.video_scope_items VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", []any{scope.ID.String(), tenant.String(), prep.String(), frame.ID.String(), scope.EnvironmentCandidate, scope.ComponentLabel, scope.ViewLabel, scope.Visibility, scope.IdentityStatus, []byte("[]"), "2026-10-17T00:00:00Z"}},
	}
	for _, statement := range statements {
		if err := db.Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
	reader := DBFrameReader{DB: db, Within: within}
	frames, err := reader.LoadPreparationFrames(context.Background(), tenant, prep)
	if err != nil || len(frames) != 1 {
		t.Fatalf("ready preparation should load its frame: frames=%+v err=%v", frames, err)
	}
	got := frames[0]
	if got.ID != frameID || got.TenantID != tenant || got.AssetID != inspection.AssetID || got.PreparationID != prep || got.Generation != 3 || got.Digest != digest || got.PTS != frame.PTSMicros || got.Environment != "kitchen" || got.Component != scopeID.String() || got.View != "front" || got.Visibility != "VISIBLE" || got.IdentityStatus != "AMBIGUOUS" || got.Quality != "LOW_QUALITY" || !got.Visible {
		t.Fatalf("loaded frame fields differ from authoritative records: %+v", got)
	}
	if _, err := reader.LoadPreparationFrames(context.Background(), otherTenant, prep); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("other tenant preparation should be rejected: %v", err)
	}
	job.Status = "FAILED"
	if err := db.Model(&database.VideoPreparationJob{}).Where("id = ?", prep).Update("status", job.Status).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := reader.LoadPreparationFrames(context.Background(), tenant, prep); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("non-ready preparation should be rejected: %v", err)
	}
}

func TestDBFrameReaderResolvesAssetOfOriginMediaWithoutInspectionResponsibility(t *testing.T) {
	db := sqliteFrameReaderDB(t)
	tenant := testID("origin-reader-tenant")
	prep, mediaID, responsibilityID := testID("origin-reader-prep"), testID("origin-reader-media"), testID("origin-reader-responsibility")
	originID, assetID, frameID := testID("origin-reader-origin"), testID("origin-reader-asset"), testID("origin-reader-frame")
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO media.video_preparation_jobs VALUES (?, ?, ?, 'READY', ?, 1)", []any{prep.String(), tenant.String(), mediaID.String(), digest}},
		{"INSERT INTO media.media_objects VALUES (?, ?, ?)", []any{mediaID.String(), tenant.String(), responsibilityID.String()}},
		{"INSERT INTO origins.origins VALUES (?, ?, ?)", []any{originID.String(), tenant.String(), assetID.String()}},
		{"INSERT INTO origins.origin_versions VALUES (?, ?, ?, ?)", []any{testID("origin-reader-version").String(), tenant.String(), originID.String(), responsibilityID.String()}},
		{"INSERT INTO media.video_frames VALUES (?, ?, ?, ?, 1, ?, 'video/frame', 9, '')", []any{frameID.String(), tenant.String(), mediaID.String(), prep.String(), digest}},
	} {
		if err := db.Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
	reader := DBFrameReader{DB: db, Within: within}
	frames, err := reader.LoadPreparationFrames(context.Background(), tenant, prep)
	if err != nil || len(frames) != 1 || frames[0].AssetID != assetID {
		t.Fatalf("origin frames did not resolve the origin asset: frames=%+v err=%v", frames, err)
	}
	if err := db.Exec("DELETE FROM origins.origin_versions").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := reader.LoadPreparationFrames(context.Background(), tenant, prep); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("media without an inspection or origin owner must be a permanent plan error, got %v", err)
	}
}
