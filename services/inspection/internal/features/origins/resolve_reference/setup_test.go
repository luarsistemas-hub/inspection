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
		if _, err := resolve(db, query); code(err) != apperror.InvalidState {
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
		query.VersionID = &oldID
		if _, err := resolve(db, query); code(err) != apperror.Conflict {
			t.Fatalf("stale requested origin accepted: %v", err)
		}
		query.VersionID = &activeID
		result, err := resolve(db, query)
		if err != nil || result.Version.ID != activeID {
			t.Fatalf("fixed active origin changed: %+v %v", result, err)
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
	tenantID, assetID, templateID, originID, versionID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	origin := database.Origin{ID: originID, TenantID: tenantID, AssetID: assetID, TemplateID: templateID, ActiveVersionID: &versionID, Version: 1, CreatedAt: now, UpdatedAt: now}
	version := database.OriginVersion{ID: versionID, TenantID: tenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: identity.NewID(), Status: "ACTIVE", IdempotencyKey: "origin", CreatedAt: now, ActivatedAt: &now}
	if err := db.Create(&origin).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&version).Error; err != nil {
		t.Fatal(err)
	}
	for index := 0; index < evidenceCount; index++ {
		item := database.OriginEvidence{ID: identity.NewID(), TenantID: tenantID, OriginVersionID: versionID, MediaID: identity.NewID(), Category: "room-" + number(index/10+1), Description: "view-" + number(index+1), CreatedAt: now.Add(time.Duration(index) * time.Second)}
		if err := db.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, Query{TenantID: tenantID, AssetID: assetID, TemplateID: templateID}, versionID
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
