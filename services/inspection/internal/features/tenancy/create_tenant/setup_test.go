package create_tenant

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type sqliteTenantRunner struct{ db *gorm.DB }

func (r sqliteTenantRunner) Within(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
	return r.db.WithContext(ctx).Transaction(fn)
}

func TestSetupRequiresDependencies(t *testing.T) {
	if err := Setup(Dependencies{}); err == nil {
		t.Fatal("expected missing dependency error")
	}
}

func TestHandleUT008BootstrapReplayProvisionsOneFixedAdminMembership(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:create_tenant_replay?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, schema := range []string{"tenancy", "access"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`CREATE TABLE tenancy.bootstrap_requests (id text PRIMARY KEY, tenant_id text NOT NULL, subject_key text NOT NULL, idempotency_key text NOT NULL, payload_digest text NOT NULL, result blob NOT NULL, created_at datetime NOT NULL)`,
		`CREATE TABLE tenancy.tenants (id text PRIMARY KEY, tenant_id text NOT NULL, name text NOT NULL, language text NOT NULL, default_timezone text NOT NULL, status text NOT NULL, version integer NOT NULL, created_at datetime NOT NULL, updated_at datetime NOT NULL)`,
		`CREATE TABLE tenancy.business_units (id text PRIMARY KEY, tenant_id text NOT NULL, code text NOT NULL, name text NOT NULL, status text NOT NULL, version integer NOT NULL, idempotency_key text NOT NULL, created_at datetime NOT NULL, updated_at datetime NOT NULL)`,
		`CREATE TABLE access.memberships (id text PRIMARY KEY, tenant_id text NOT NULL, identity_id text NOT NULL, issuer text NOT NULL, subject text NOT NULL, role text NOT NULL, status text NOT NULL, version integer NOT NULL, created_at datetime NOT NULL, updated_at datetime NOT NULL)`,
		`CREATE TABLE access.product_entitlements (id text PRIMARY KEY, tenant_id text NOT NULL, membership_id text NOT NULL, product text NOT NULL, created_at datetime NOT NULL)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}

	now := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	deps := Dependencies{
		DB:                db,
		Bus:               mediator.New(),
		Now:               func() time.Time { return now },
		NewID:             identity.NewID,
		Runner:            sqliteTenantRunner{db: db},
		SuperAdminIssuer:  "https://issuer.example",
		SuperAdminSubject: "fixed-admin-subject",
	}
	command := Command{
		Name: "Tenant", Language: "pt-BR", Timezone: "UTC", BusinessUnitCode: "HQ", BusinessUnitName: "Headquarters",
		Issuer: "https://issuer.example", Subject: "bootstrap-subject", IdempotencyKey: "bootstrap-replay",
	}
	first, err := handle(context.Background(), deps, command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := handle(context.Background(), deps, command)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("replay result changed: first=%#v second=%#v", first, second)
	}

	var memberships, entitlements int64
	if err := db.Model(&database.Membership{}).Where("tenant_id=?", first.TenantID).Count(&memberships).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.ProductEntitlement{}).Where("tenant_id=? AND membership_id=?", first.TenantID, first.MembershipID).Count(&entitlements).Error; err != nil {
		t.Fatal(err)
	}
	if memberships != 1 || entitlements != 2 {
		t.Fatalf("bootstrap created memberships=%d entitlements=%d, want 1 and 2", memberships, entitlements)
	}
	var membership database.Membership
	if err := db.First(&membership, "id=?", first.MembershipID).Error; err != nil {
		t.Fatal(err)
	}
	if membership.Issuer != deps.SuperAdminIssuer || membership.Subject != deps.SuperAdminSubject || membership.Role != "TENANT_ADMIN" || membership.Status != "ACTIVE" {
		t.Fatalf("fixed admin membership = %#v", membership)
	}
}
