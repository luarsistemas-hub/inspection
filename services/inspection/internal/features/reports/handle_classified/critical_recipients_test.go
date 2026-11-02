package handle_classified

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestLoadCriticalRecipientsUsesVerifiedSelectedScopedDashboardMemberships(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, schema := range []string{"notifications", "access", "tenancy", "assets", "inspections", "projects"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`CREATE TABLE notifications.recipient_channels (id blob, tenant_id blob, recipient_membership_id blob, channel text, destination text, verified_at datetime, selected boolean, version integer, created_at datetime, updated_at datetime)`,
		`CREATE TABLE access.memberships (id blob, tenant_id blob, role text, status text)`,
		`CREATE TABLE access.product_entitlements (tenant_id blob, membership_id blob, product text)`,
		`CREATE TABLE access.resource_scopes (tenant_id blob, membership_id blob, kind text, resource_id blob)`,
		`CREATE TABLE tenancy.business_units (id blob, tenant_id blob, status text)`,
		`CREATE TABLE assets.assets (id blob, tenant_id blob, status text)`,
		`CREATE TABLE inspections.inspections (id blob, tenant_id blob, status text)`,
		`CREATE TABLE projects.projects (id blob, tenant_id blob, status text)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}

	tenantID, inspectionID, unitID, assetID, projectID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	inspection := database.Inspection{ID: inspectionID, BusinessUnitID: unitID, AssetID: assetID, ProjectID: &projectID}
	if err := db.Exec("INSERT INTO tenancy.business_units (id,tenant_id,status) VALUES (?,?,?)", unitID, tenantID, "ACTIVE").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO assets.assets (id,tenant_id,status) VALUES (?,?,?)", assetID, tenantID, "ACTIVE").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO inspections.inspections (id,tenant_id,status) VALUES (?,?,?)", inspectionID, tenantID, "COMPLETED").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO projects.projects (id,tenant_id,status) VALUES (?,?,?)", projectID, tenantID, "ACTIVE").Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var admin, manager, unverified, unselected, external, revoked, unscoped, wrongProduct identity.ID
	ids := []*identity.ID{&admin, &manager, &unverified, &unselected, &external, &revoked, &unscoped, &wrongProduct}
	for _, id := range ids {
		*id = identity.NewID()
	}
	members := []identity.ID{admin, manager, unverified, unselected, external, revoked, unscoped, wrongProduct}
	roles := []string{"TENANT_ADMIN", "MANAGER", "MANAGER", "MANAGER", "CUSTOMER_VIEWER", "MANAGER", "MANAGER", "MANAGER"}
	for i, id := range members {
		status := "ACTIVE"
		if id == revoked {
			status = "DISABLED"
		}
		if err := db.Exec("INSERT INTO access.memberships (id,tenant_id,role,status) VALUES (?,?,?,?)", id, tenantID, roles[i], status).Error; err != nil {
			t.Fatal(err)
		}
		if id != wrongProduct {
			if err := db.Exec("INSERT INTO access.product_entitlements (tenant_id,membership_id,product) VALUES (?,?,?)", tenantID, id, "DASHBOARD").Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, id := range []identity.ID{manager, unverified, unselected, external, revoked, wrongProduct} {
		if err := db.Exec("INSERT INTO access.resource_scopes (tenant_id,membership_id,kind,resource_id) VALUES (?,?,?,?)", tenantID, id, "ASSET", assetID).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("INSERT INTO access.resource_scopes (tenant_id,membership_id,kind,resource_id) VALUES (?,?,?,?)", tenantID, unscoped, "ASSET", identity.NewID()).Error; err != nil {
		t.Fatal(err)
	}
	channels := []struct {
		member   identity.ID
		selected bool
		verified *time.Time
	}{
		{admin, true, &now}, {manager, true, &now}, {unverified, true, nil}, {unselected, false, &now}, {external, true, &now}, {revoked, true, &now}, {unscoped, true, &now}, {wrongProduct, true, &now},
	}
	for _, channel := range channels {
		if err := db.Create(&database.RecipientChannel{ID: identity.NewID(), TenantID: tenantID, RecipientMembershipID: channel.member, Channel: "EMAIL", Destination: channel.member.String() + "@example.test", VerifiedAt: channel.verified, Selected: channel.selected}).Error; err != nil {
			t.Fatal(err)
		}
	}

	rows, err := loadCriticalRecipients(context.Background(), db, tenantID, inspection)
	if err != nil {
		t.Fatal(err)
	}
	got := map[identity.ID]bool{}
	for _, row := range rows {
		got[row.RecipientMembershipID] = true
	}
	if len(rows) != 2 || !got[manager] || !got[admin] {
		t.Fatalf("eligible recipients = %#v, want the scoped manager and tenant admin", rows)
	}
	if err := db.Exec("UPDATE tenancy.business_units SET status='ARCHIVED' WHERE id=?", unitID).Error; err != nil {
		t.Fatal(err)
	}
	if rows, err = loadCriticalRecipients(context.Background(), db, tenantID, inspection); err != nil || len(rows) != 0 {
		t.Fatalf("archived business unit recipients=%d err=%v, want none", len(rows), err)
	}
	if err := db.Exec("UPDATE tenancy.business_units SET status='ACTIVE' WHERE id=?", unitID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE assets.assets SET status='ARCHIVED' WHERE id=?", assetID).Error; err != nil {
		t.Fatal(err)
	}
	if rows, err = loadCriticalRecipients(context.Background(), db, tenantID, inspection); err != nil || len(rows) != 0 {
		t.Fatalf("archived asset recipients=%d err=%v, want none", len(rows), err)
	}
}
