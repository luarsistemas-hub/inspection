package remind_deadlines

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupRequiresDatabase(t *testing.T) {
	if _, err := Setup(Dependencies{}); err != gorm.ErrInvalidDB {
		t.Fatalf("setup error = %v, want invalid database", err)
	}
}

func TestNotificationsAreIdempotentAndPaginateEveryDueInspection(t *testing.T) {
	db := deadlineDB(t)
	tenantID, assetID, membershipID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO assets.assets (id, tenant_id, name, address) VALUES (?, ?, ?, ?)", assetID, tenantID, "Casa QA", "Rua Um").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO access.memberships (id, tenant_id, role, status) VALUES (?, ?, ?, ?)", membershipID, tenantID, "TENANT_ADMIN", "ACTIVE").Error; err != nil {
		t.Fatal(err)
	}
	unitID := identity.NewID()
	const total = 101
	now := time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC)
	for i := 0; i < total; i++ {
		inspectionID := identity.NewID()
		deadline := now.Add(time.Duration(i+1) * time.Minute)
		if err := db.Exec("INSERT INTO inspections.inspections (id, tenant_id, asset_id, business_unit_id, status, deadline_at) VALUES (?, ?, ?, ?, 'IN_PROGRESS', ?)", inspectionID, tenantID, assetID, unitID, deadline).Error; err != nil {
			t.Fatal(err)
		}
	}
	operation, err := Setup(Dependencies{
		DB: db,
		Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
			return fn(db.WithContext(ctx))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := operation(context.Background(), tenantID, now); err != nil {
		t.Fatalf("create deadline notifications: %v", err)
	}
	if err := operation(context.Background(), tenantID, now); err != nil {
		t.Fatalf("repeat deadline notifications: %v", err)
	}
	var count int64
	if err := db.Model(&database.RecipientNotification{}).Where("tenant_id=? AND recipient_membership_id=?", tenantID, membershipID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != total {
		t.Fatalf("notification count = %d, want %d", count, total)
	}
}

func TestSetupPropagatesDatabaseErrors(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := Setup(Dependencies{DB: db, Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return fn(db.WithContext(ctx))
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := operation(context.Background(), identity.NewID(), time.Now().UTC()); err == nil {
		t.Fatal("expected missing-table error")
	}
}

func deadlineDB(t *testing.T) *gorm.DB {
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
		`CREATE TABLE inspections.inspections (id blob PRIMARY KEY, tenant_id blob NOT NULL, asset_id blob NOT NULL, business_unit_id blob NOT NULL, project_id blob, status text NOT NULL, deadline_at datetime NOT NULL)`,
		`CREATE TABLE access.memberships (id blob PRIMARY KEY, tenant_id blob NOT NULL, role text NOT NULL, status text NOT NULL)`,
		`CREATE TABLE access.resource_scopes (id blob PRIMARY KEY, tenant_id blob NOT NULL, membership_id blob NOT NULL, kind text NOT NULL, resource_id blob NOT NULL)`,
		`CREATE TABLE notifications.recipient_notifications (id blob PRIMARY KEY, tenant_id blob NOT NULL, recipient_membership_id blob NOT NULL, event_id blob NOT NULL, kind text NOT NULL, title text NOT NULL, body text NOT NULL, resource_kind text NOT NULL, resource_id blob, created_at datetime NOT NULL, read_at datetime, UNIQUE (tenant_id, recipient_membership_id, event_id, kind))`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}
