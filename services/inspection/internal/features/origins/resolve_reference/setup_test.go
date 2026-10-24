package resolve_reference

import (
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPropertyOriginContractsIT311IT312IT313IT315IT320(t *testing.T) {
	t.Run("IT-311 invalid current evidence blocks property capture", func(t *testing.T) {
		db, query, _ := originFixture(t, 0)
		if _, err := resolve(db, query); code(err) != apperror.InvalidState {
			t.Fatalf("empty origin evidence accepted: %v", err)
		}
	})
	t.Run("IT-312 property without active origin is blocked", func(t *testing.T) {
		db, query, _ := originFixture(t, 1)
		if err := db.Model(&database.Origin{}).Where("tenant_id=?", query.TenantID).Update("active_version_id", nil).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := resolve(db, query); code(err) != apperror.InvalidInput {
			t.Fatalf("missing active origin accepted: %v", err)
		}
	})
	t.Run("IT-313 origin exceeding capture limit is blocked", func(t *testing.T) {
		db, query, _ := originFixture(t, core.MaxActivePhotos+1)
		if _, err := resolve(db, query); code(err) != apperror.InvalidState {
			t.Fatalf("oversized origin accepted: %v", err)
		}
	})
	t.Run("IT-315 fixed origin rejects a newly active mismatch", func(t *testing.T) {
		db, query, activeID := originFixture(t, 1)
		oldID := identity.NewID()
		var origin database.Origin
		if err := db.Where("tenant_id=? AND asset_id=?", query.TenantID, query.AssetID).Take(&origin).Error; err != nil {
			t.Fatal(err)
		}
		oldVersion := database.OriginVersion{ID: oldID, TenantID: query.TenantID, OriginID: origin.ID, VersionNumber: 0, ResponsibilityID: identity.Ptr(identity.NewID()), Status: "INVALIDATED", IdempotencyKey: "origin-old", CreatedAt: time.Now().UTC()}
		if err := db.Create(&oldVersion).Error; err != nil {
			t.Fatal(err)
		}
		query.VersionID = &oldID
		if _, err := resolve(db, query); code(err) != apperror.Conflict {
			t.Fatalf("stale requested origin accepted: %v", err)
		}
		query.VersionID = &activeID
		result, err := resolve(db, query)
		if err != nil || result.Version.ID != activeID {
			t.Fatalf("fixed active origin changed: %+v %v", result, err)
		}
		if len(result.Evidence) != 1 || len(result.Evidence[0].AttentionItems) != 1 || result.Evidence[0].AttentionItems[0] != "Cafeteira" {
			t.Fatalf("origin attention items were not projected: %+v", result.Evidence)
		}
	})
	t.Run("IT-320 many rooms retain grouped category and instructions", func(t *testing.T) {
		db, query, _ := originFixture(t, core.MaxActivePhotos)
		result, err := resolve(db, query)
		if err != nil || len(result.Requirements) != core.MaxActivePhotos {
			t.Fatalf("large origin failed: requirements=%d err=%v", len(result.Requirements), err)
		}
		for index, requirement := range result.Requirements {
			if requirement.Section == "" || requirement.Label != requirement.Section || requirement.Instructions == "" || requirement.Key != "origin:"+result.Evidence[index].MediaID.String() {
				t.Fatalf("origin item %d lost navigation data: %+v", index, requirement)
			}
		}
	})
}

func TestResolveReferenceMissingOriginReturnsInvalidInput(t *testing.T) {
	t.Run("origin does not exist", func(t *testing.T) {
		db, query, _ := originFixture(t, 1)
		if err := db.Where("tenant_id=? AND asset_id=?", query.TenantID, query.AssetID).Delete(&database.Origin{}).Error; err != nil {
			t.Fatal(err)
		}
		_, err := resolve(db, query)
		if got := code(err); got != apperror.InvalidInput {
			t.Fatalf("missing origin error code = %s, want %s: %v", got, apperror.InvalidInput, err)
		}
	})

	t.Run("origin has no active version", func(t *testing.T) {
		db, query, _ := originFixture(t, 1)
		if err := db.Model(&database.Origin{}).Where("tenant_id=? AND asset_id=?", query.TenantID, query.AssetID).Update("active_version_id", nil).Error; err != nil {
			t.Fatal(err)
		}
		_, err := resolve(db, query)
		if got := code(err); got != apperror.InvalidInput {
			t.Fatalf("missing active version error code = %s, want %s: %v", got, apperror.InvalidInput, err)
		}
	})
}

func TestVideoOriginCannotBePinnedWhilePreparationIsDiscoveringIT056(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS origins").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&database.Origin{}, &database.OriginVersion{}, &database.VideoPreparationJob{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenantID, assetID, originID, versionID, preparationID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	origin := database.Origin{ID: originID, TenantID: tenantID, AssetID: assetID, ActiveVersionID: &versionID, Version: 1, CreatedAt: now, UpdatedAt: now}
	version := database.OriginVersion{ID: versionID, TenantID: tenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: identity.Ptr(identity.NewID()), Status: "ACTIVE", MediaKind: "VIDEO", VideoPreparationID: &preparationID, ScopeManifestDigest: "manifest-discovering", IdempotencyKey: "video-origin-discovering", CreatedAt: now, ActivatedAt: &now}
	preparation := database.VideoPreparationJob{ID: preparationID, TenantID: tenantID, MediaID: identity.NewID(), Status: "PROCESSING", Phase: "DISCOVERING", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, CreatedAt: now}
	for _, row := range []any{&origin, &version, &preparation} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	_, err = resolve(db, Query{TenantID: tenantID, AssetID: assetID, MediaKind: "VIDEO"})
	if code(err) != apperror.InvalidState {
		t.Fatalf("createInspection reference lookup accepted DISCOVERING origin: %v", err)
	}
}

func TestVideoOriginUsesOneMediaRequirementAndKeepsScopeProjection(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS origins").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&database.Origin{}, &database.OriginVersion{}, &database.VideoPreparationJob{}, &database.VideoScopeItem{}, &database.VideoFrame{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenantID, assetID, originID, versionID, preparationID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	mediaID, frameID, scopeID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	origin := database.Origin{ID: originID, TenantID: tenantID, AssetID: assetID, ActiveVersionID: &versionID, Version: 1, CreatedAt: now, UpdatedAt: now}
	version := database.OriginVersion{ID: versionID, TenantID: tenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: identity.Ptr(identity.NewID()), Status: "ACTIVE", MediaKind: "VIDEO", VideoPreparationID: &preparationID, ScopeManifestDigest: "manifest", IdempotencyKey: "video-origin", CreatedAt: now, ActivatedAt: &now}
	preparation := database.VideoPreparationJob{ID: preparationID, TenantID: tenantID, MediaID: mediaID, Status: "READY", ManifestDigest: "manifest", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, CreatedAt: now}
	frame := database.VideoFrame{ID: frameID, TenantID: tenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 1200000, SHA256: "digest", ObjectKey: "frame.jpg", CreatedAt: now}
	scope := database.VideoScopeItem{ID: scopeID, TenantID: tenantID, PreparationID: preparationID, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "front view", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: []byte(`[]`), CreatedAt: now}
	// A repeated, visually identical bedroom keeps an unresolved identity.
	bedroomFrameID, bedroomScopeID := identity.NewID(), identity.NewID()
	bedroomFrame := database.VideoFrame{ID: bedroomFrameID, TenantID: tenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 9000000, SHA256: "digest-bedroom", ObjectKey: "bedroom.jpg", CreatedAt: now}
	bedroom := database.VideoScopeItem{ID: bedroomScopeID, TenantID: tenantID, PreparationID: preparationID, FrameID: bedroomFrameID, EnvironmentCandidate: "bedroom", ComponentLabel: "wardrobe", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "AMBIGUOUS", Landmarks: []byte(`[]`), CreatedAt: now.Add(time.Second)}
	for _, row := range []any{&origin, &version, &preparation, &frame, &scope, &bedroomFrame, &bedroom} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	result, err := resolve(db, Query{TenantID: tenantID, AssetID: assetID, MediaKind: "VIDEO"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Requirements) != 1 || result.Requirements[0].Key != "video:main" || result.Requirements[0].EvidenceKind != "VIDEO" {
		t.Fatalf("video requirements = %+v, want only video:main", result.Requirements)
	}
	if len(result.VideoScope) != 2 || result.VideoScope[0].ScopeItemID != scopeID || result.VideoScope[0].ComponentLabel != "sink" || result.VideoScope[0].ViewLabel != "front view" || result.VideoScope[0].Ambiguous {
		t.Fatalf("video scope projection lost source component: %+v", result.VideoScope)
	}
	if result.VideoScope[1].ScopeItemID != bedroomScopeID || !result.VideoScope[1].Ambiguous {
		t.Fatalf("identity-ambiguous bedroom was not marked ambiguous: %+v", result.VideoScope[1])
	}
}

func originFixture(t *testing.T, evidenceCount int) (*gorm.DB, Query, identity.ID) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS origins").Error; err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&database.Origin{}, &database.OriginVersion{}, &database.OriginEvidence{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenantID, assetID, originID, versionID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	origin := database.Origin{ID: originID, TenantID: tenantID, AssetID: assetID, ActiveVersionID: &versionID, Version: 1, CreatedAt: now, UpdatedAt: now}
	version := database.OriginVersion{ID: versionID, TenantID: tenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: identity.Ptr(identity.NewID()), Status: "ACTIVE", IdempotencyKey: "origin", CreatedAt: now, ActivatedAt: &now}
	if err := db.Create(&origin).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&version).Error; err != nil {
		t.Fatal(err)
	}
	for index := 0; index < evidenceCount; index++ {
		attentionItems := []byte(`[]`)
		if index == 0 {
			attentionItems = []byte(`["Cafeteira"]`)
		}
		item := database.OriginEvidence{ID: identity.NewID(), TenantID: tenantID, OriginVersionID: versionID, MediaID: identity.NewID(), Category: "room-" + number(index/10+1), Description: "view-" + number(index+1), AttentionItems: attentionItems, CreatedAt: now.Add(time.Duration(index) * time.Second)}
		if err := db.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, Query{TenantID: tenantID, AssetID: assetID}, versionID
}

func code(err error) apperror.Code {
	code, _, _ := apperror.Public(err)
	return code
}

func number(value int) string {
	if value < 10 {
		return string(rune('0' + value))
	}
	return number(value/10) + string(rune('0'+value%10))
}
