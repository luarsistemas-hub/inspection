package auth

import (
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTFE_IT_431ArchivedDirectAndInheritedResourceRemainsReadableButNotMutable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"assets", "tenancy"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("CREATE TABLE tenancy.business_units (id blob primary key,tenant_id blob not null,status text not null)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE assets.assets (id blob primary key,tenant_id blob not null,business_unit_id blob not null,status text not null)").Error; err != nil {
		t.Fatal(err)
	}
	tenantID, unitID, assetID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO tenancy.business_units (id,tenant_id,status) VALUES (?,?,?)", unitID, tenantID, "ARCHIVED").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO assets.assets (id,tenant_id,business_unit_id,status) VALUES (?,?,?,?)", assetID, tenantID, unitID, "ACTIVE").Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, grantKind string }{{"direct", "ASSET"}, {"inherited from unit", "BUSINESS_UNIT"}} {
		t.Run(tc.name, func(t *testing.T) {
			grantID := assetID
			if tc.grantKind == "BUSINESS_UNIT" {
				grantID = unitID
			}
			allowed, err := currentDescendant(db, tenantID, tc.grantKind, grantID, requestctx.Scope{Kind: "ASSET", ID: assetID})
			if err != nil || !allowed {
				t.Fatalf("historical read grant allowed=%v err=%v", allowed, err)
			}
			archived, err := archivedTarget(db, tenantID, requestctx.Scope{Kind: "ASSET", ID: assetID})
			if err != nil || !archived {
				t.Fatalf("mutation archive check archived=%v err=%v", archived, err)
			}
		})
	}
}

func TestTFE_IT_431ArchivedProjectIsRejectedByMutationTargetSQL(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"assets", "tenancy", "projects"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		"CREATE TABLE tenancy.business_units (id blob primary key,tenant_id blob not null,status text not null)",
		"CREATE TABLE assets.assets (id blob primary key,tenant_id blob not null,business_unit_id blob not null,status text not null)",
		"CREATE TABLE projects.projects (id blob primary key,tenant_id blob not null,asset_id blob not null,business_unit_id blob not null,status text not null)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, unitID, assetID, projectID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	for _, row := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO tenancy.business_units (id,tenant_id,status) VALUES (?,?,?)", []any{unitID, tenantID, "ACTIVE"}},
		{"INSERT INTO assets.assets (id,tenant_id,business_unit_id,status) VALUES (?,?,?,?)", []any{assetID, tenantID, unitID, "ACTIVE"}},
		{"INSERT INTO projects.projects (id,tenant_id,asset_id,business_unit_id,status) VALUES (?,?,?,?,?)", []any{projectID, tenantID, assetID, unitID, "ARCHIVED"}},
	} {
		if err := db.Exec(row.sql, row.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	archived, err := archivedTarget(db, tenantID, requestctx.Scope{Kind: "PROJECT", ID: projectID})
	if err != nil || !archived {
		t.Fatalf("archived project SQL result archived=%v err=%v", archived, err)
	}
}

func TestTFE_IT_431InspectionNewWorkChecksInspectionAndAncestors(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"assets", "tenancy", "projects", "inspections"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		"CREATE TABLE tenancy.business_units (id blob primary key,tenant_id blob not null,status text not null)",
		"CREATE TABLE assets.assets (id blob primary key,tenant_id blob not null,business_unit_id blob not null,status text not null)",
		"CREATE TABLE projects.projects (id blob primary key,tenant_id blob not null,asset_id blob not null,business_unit_id blob not null,status text not null)",
		"CREATE TABLE inspections.inspections (id blob primary key,tenant_id blob not null,project_id blob,asset_id blob not null,business_unit_id blob not null,status text not null)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, unitID, assetID, projectID, inspectionID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	for _, row := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO tenancy.business_units (id,tenant_id,status) VALUES (?,?,?)", []any{unitID, tenantID, "ACTIVE"}},
		{"INSERT INTO assets.assets (id,tenant_id,business_unit_id,status) VALUES (?,?,?,?)", []any{assetID, tenantID, unitID, "ACTIVE"}},
		{"INSERT INTO projects.projects (id,tenant_id,asset_id,business_unit_id,status) VALUES (?,?,?,?,?)", []any{projectID, tenantID, assetID, unitID, "ACTIVE"}},
		{"INSERT INTO inspections.inspections (id,tenant_id,project_id,asset_id,business_unit_id,status) VALUES (?,?,?,?,?,?)", []any{inspectionID, tenantID, projectID, assetID, unitID, "ACTIVE"}},
	} {
		if err := db.Exec(row.sql, row.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	target := requestctx.Scope{Kind: "INSPECTION", ID: inspectionID}
	if archived, err := archivedTarget(db, tenantID, target); err != nil || archived {
		t.Fatalf("active inspection archived=%v err=%v", archived, err)
	}
	if err := db.Exec("UPDATE projects.projects SET status='ARCHIVED' WHERE id=?", projectID).Error; err != nil {
		t.Fatal(err)
	}
	if archived, err := archivedTarget(db, tenantID, target); err != nil || !archived {
		t.Fatalf("inspection with archived project archived=%v err=%v", archived, err)
	}
}
