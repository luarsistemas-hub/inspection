package record_in_app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupRecordsEventNotificationIdempotently(t *testing.T) {
	db := notificationDB(t)
	tenantID, inspectionID, assetID, membershipID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO assets.assets (id, tenant_id, name, address) VALUES (?, ?, ?, ?)", assetID, tenantID, "Casa QA", "Rua Um").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO inspections.inspections (id, tenant_id, asset_id, business_unit_id) VALUES (?, ?, ?, ?)", inspectionID, tenantID, assetID, identity.NewID()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO access.memberships (id, tenant_id, role, status) VALUES (?, ?, ?, ?)", membershipID, tenantID, "TENANT_ADMIN", "ACTIVE").Error; err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]identity.ID{"inspectionId": inspectionID})
	envelope := events.RawEnvelope{ID: identity.NewID(), Type: "inspection.created.v1", OccurredAt: time.Now().UTC(), TenantID: tenantID, Payload: payload}
	handler := Setup()
	if err := handler(context.Background(), db, envelope); err != nil {
		t.Fatalf("record notification: %v", err)
	}
	if err := handler(context.Background(), db, envelope); err != nil {
		t.Fatalf("replay event: %v", err)
	}
	var count int64
	if err := db.Table("notifications.recipient_notifications").Where("tenant_id=? AND recipient_membership_id=?", tenantID, membershipID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("notification count = %d, want 1", count)
	}
	var body string
	if err := db.Table("notifications.recipient_notifications").Select("body").Where("tenant_id=? AND recipient_membership_id=?", tenantID, membershipID).Take(&body).Error; err != nil {
		t.Fatal(err)
	}
	if body == "" || body == "Uma vistoria foi adicionada à sua operação." {
		t.Fatalf("notification context was not included: %q", body)
	}
}

func TestSetupRejectsMalformedAndUnknownEvents(t *testing.T) {
	handler := Setup()
	tenantID := identity.NewID()
	for _, envelope := range []events.RawEnvelope{
		{TenantID: tenantID, Type: "inspection.created.v1", Payload: json.RawMessage("{")},
		{TenantID: tenantID, Type: "unknown.event.v1", Payload: json.RawMessage(`{}`)},
	} {
		if err := handler(context.Background(), &gorm.DB{}, envelope); !errors.Is(err, messaging.ErrPermanent) {
			t.Fatalf("invalid event error = %v, want permanent", err)
		}
	}
}

func TestSetupPropagatesDatabaseFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]identity.ID{"inspectionId": identity.NewID()})
	envelope := events.RawEnvelope{ID: identity.NewID(), Type: "inspection.created.v1", TenantID: identity.NewID(), Payload: payload}
	if err := Setup()(context.Background(), db, envelope); err == nil {
		t.Fatal("expected missing-table error")
	}
}

func notificationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, schema := range []string{"assets", "inspections", "access", "notifications"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`CREATE TABLE assets.assets (id blob PRIMARY KEY, tenant_id blob NOT NULL, name text NOT NULL, address text NOT NULL)`,
		`CREATE TABLE inspections.inspections (id blob PRIMARY KEY, tenant_id blob NOT NULL, asset_id blob NOT NULL, business_unit_id blob NOT NULL, project_id blob)`,
		`CREATE TABLE access.memberships (id blob PRIMARY KEY, tenant_id blob NOT NULL, role text NOT NULL, status text NOT NULL)`,
		`CREATE TABLE access.resource_scopes (id blob PRIMARY KEY, tenant_id blob NOT NULL, membership_id blob NOT NULL, kind text NOT NULL, resource_id blob NOT NULL)`,
		`CREATE TABLE notifications.recipient_notifications (id blob PRIMARY KEY, tenant_id blob NOT NULL, recipient_membership_id blob NOT NULL, event_id blob NOT NULL, kind text NOT NULL, title text NOT NULL, body text NOT NULL, resource_kind text NOT NULL, resource_id blob, created_at datetime NOT NULL, read_at datetime)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestInventoryDraftReadyNotifiesScopedInternalRecipientsOnce(t *testing.T) {
	db := notificationDB(t)
	tenantID, assetID, versionID, originID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	admin, employee, viewer, outsider := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	for _, statement := range []string{
		"ATTACH DATABASE ':memory:' AS origins",
		`CREATE TABLE origins.origins (id blob PRIMARY KEY, tenant_id blob NOT NULL, asset_id blob NOT NULL)`,
		`CREATE TABLE origins.origin_versions (id blob PRIMARY KEY, tenant_id blob NOT NULL, origin_id blob NOT NULL, source_inspection_id blob)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	businessUnit := identity.NewID()
	inserts := []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO assets.assets (id, tenant_id, name, address) VALUES (?, ?, ?, ?)", []any{assetID, tenantID, "Casa QA", "Rua Um"}},
		{"INSERT INTO origins.origins (id, tenant_id, asset_id) VALUES (?, ?, ?)", []any{originID, tenantID, assetID}},
		{"INSERT INTO origins.origin_versions (id, tenant_id, origin_id) VALUES (?, ?, ?)", []any{versionID, tenantID, originID}},
		{"INSERT INTO access.memberships (id, tenant_id, role, status) VALUES (?, ?, 'TENANT_ADMIN', 'ACTIVE')", []any{admin, tenantID}},
		{"INSERT INTO access.memberships (id, tenant_id, role, status) VALUES (?, ?, 'EMPLOYEE', 'ACTIVE')", []any{employee, tenantID}},
		{"INSERT INTO access.memberships (id, tenant_id, role, status) VALUES (?, ?, 'CUSTOMER_VIEWER', 'ACTIVE')", []any{viewer, tenantID}},
		{"INSERT INTO access.memberships (id, tenant_id, role, status) VALUES (?, ?, 'EMPLOYEE', 'ACTIVE')", []any{outsider, tenantID}},
		{"INSERT INTO access.resource_scopes (id, tenant_id, membership_id, kind, resource_id) VALUES (?, ?, ?, 'ASSET', ?)", []any{identity.NewID(), tenantID, employee, assetID}},
		{"INSERT INTO access.resource_scopes (id, tenant_id, membership_id, kind, resource_id) VALUES (?, ?, ?, 'ASSET', ?)", []any{identity.NewID(), tenantID, viewer, assetID}},
		{"INSERT INTO access.resource_scopes (id, tenant_id, membership_id, kind, resource_id) VALUES (?, ?, ?, 'BUSINESS_UNIT', ?)", []any{identity.NewID(), tenantID, outsider, businessUnit}},
	}
	for _, insert := range inserts {
		if err := db.Exec(insert.sql, insert.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	payload, _ := json.Marshal(events.InventoryDraftReadyPayload{OriginVersionID: versionID})
	handler := Setup()
	for i := 0; i < 2; i++ {
		envelope := events.RawEnvelope{ID: identity.NewID(), Type: "inventory.draft_ready.v1", OccurredAt: time.Now().UTC(), TenantID: tenantID, Payload: payload}
		if err := handler(context.Background(), db, envelope); err != nil {
			t.Fatalf("record draft ready: %v", err)
		}
	}
	var rows []struct {
		RecipientMembershipID identity.ID
		ResourceKind          string
		ResourceID            identity.ID
	}
	if err := db.Table("notifications.recipient_notifications").Where("tenant_id=? AND kind='INVENTORY_DRAFT_READY'", tenantID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	recipients := map[identity.ID]bool{}
	for _, row := range rows {
		if row.ResourceKind != "BASELINE_DRAFT" || row.ResourceID != versionID {
			t.Fatalf("notification resource = %s %s", row.ResourceKind, row.ResourceID)
		}
		recipients[row.RecipientMembershipID] = true
	}
	if len(rows) != 2 || !recipients[admin] || !recipients[employee] || recipients[viewer] || recipients[outsider] {
		t.Fatalf("recipients = %v, want the tenant admin and the scoped employee once each", recipients)
	}
}
