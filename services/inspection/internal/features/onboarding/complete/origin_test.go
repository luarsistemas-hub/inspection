package complete

import (
	"context"
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestEnsureOriginSupersedesActiveVersionWhenAssetIsReused(t *testing.T) {
	db := openOriginTestDB(t)
	ctx := context.Background()
	tenantID, assetID, templateVersionID := identity.NewID(), identity.NewID(), identity.NewID()
	service := Service{DB: db, Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) }}

	firstSession, secondSession := identity.NewID(), identity.NewID()
	firstMedia := createReadyOriginMedia(t, db, tenantID, firstSession)
	secondMedia := createReadyOriginMedia(t, db, tenantID, secondSession)

	firstVersion, err := service.ensureOrigin(ctx, tenantID, firstSession, assetID, templateVersionID, []identity.ID{firstMedia})
	if err != nil {
		t.Fatal(err)
	}
	secondVersion, err := service.ensureOrigin(ctx, tenantID, secondSession, assetID, templateVersionID, []identity.ID{secondMedia})
	if err != nil {
		t.Fatalf("reused asset must receive a new origin version: %v", err)
	}

	var origins []database.Origin
	if err := db.Where("tenant_id = ? AND asset_id = ?", tenantID, assetID).Find(&origins).Error; err != nil || len(origins) != 1 {
		t.Fatalf("asset must keep exactly one origin: count=%d err=%v", len(origins), err)
	}
	if origins[0].ActiveVersionID == nil || *origins[0].ActiveVersionID != secondVersion {
		t.Fatalf("origin must point to the newest version: %+v", origins[0])
	}

	var first, second database.OriginVersion
	if err := db.Where("id = ?", firstVersion).First(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id = ?", secondVersion).First(&second).Error; err != nil {
		t.Fatal(err)
	}
	if first.Status != "SUPERSEDED" || first.VersionNumber != 1 {
		t.Fatalf("previous version must be superseded: %+v", first)
	}
	if second.Status != "ACTIVE" || second.VersionNumber != 2 || second.SupersedesID == nil || *second.SupersedesID != firstVersion {
		t.Fatalf("new version must be active and link to the previous one: %+v", second)
	}

	retried, err := service.ensureOrigin(ctx, tenantID, firstSession, assetID, templateVersionID, []identity.ID{firstMedia})
	if err != nil || retried != firstVersion {
		t.Fatalf("retry of a superseded session must return its own version: id=%s err=%v", retried, err)
	}
}

func openOriginTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"origins", "media", "capture"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	statements := []string{
		`CREATE TABLE origins.origins (id blob PRIMARY KEY, tenant_id blob, asset_id blob, active_version_id blob, version integer, created_at datetime, updated_at datetime, UNIQUE (tenant_id, asset_id))`,
		`CREATE TABLE origins.origin_versions (id blob PRIMARY KEY, tenant_id blob, origin_id blob, version_number integer, responsibility_id blob UNIQUE, supersedes_id blob, status text, idempotency_key text, created_at datetime, submitted_at datetime, activated_at datetime, invalidated_at datetime, UNIQUE (tenant_id, origin_id, version_number), UNIQUE (tenant_id, idempotency_key))`,
		`CREATE TABLE origins.origin_evidence (id blob PRIMARY KEY, tenant_id blob, origin_version_id blob, media_id blob UNIQUE, category text, description text, attention_items blob, created_at datetime)`,
		`CREATE TABLE media.media_objects (id blob PRIMARY KEY, tenant_id blob, responsibility_id blob, object_key text UNIQUE, content_type text, sha256 text, size_bytes integer, status text, idempotency_key text, requirement_key text, description text, attention_items blob, capture_source text, captured_at datetime, device_context blob, latitude_e6 integer, longitude_e6 integer, accuracy_mm integer, distance_mm integer, flags blob, replaces_media_id blob, created_at datetime)`,
		`CREATE TABLE capture.capture_drafts (id blob PRIMARY KEY, tenant_id blob, responsibility_id blob, kind text, template_version_id blob, reference_payload blob, policy_payload blob, requirements blob, status text, version integer, created_at datetime, updated_at datetime, UNIQUE (tenant_id, responsibility_id))`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func createReadyOriginMedia(t *testing.T, db *gorm.DB, tenantID, sessionID identity.ID) identity.ID {
	t.Helper()
	media := database.MediaObject{
		ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: sessionID,
		ObjectKey: identity.NewID().String(), ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64),
		Status: "READY", RequirementKey: "reference", Description: "Fachada",
	}
	if err := db.Create(&media).Error; err != nil {
		t.Fatal(err)
	}
	return media.ID
}
