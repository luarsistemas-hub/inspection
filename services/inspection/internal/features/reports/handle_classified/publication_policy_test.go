package handle_classified

import (
	"context"
	"testing"

	"inspection/libs/identity"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestEffectivePublicationPolicyUT025UT027 covers the policy read at
// finalization: synthetic MANUAL version 0 without a row, the stored mode and
// version otherwise, and strict tenant isolation.
func TestEffectivePublicationPolicyUT025UT027(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, statement := range []string{
		"ATTACH DATABASE ':memory:' AS reports",
		"CREATE TABLE reports.publication_policies (id blob PRIMARY KEY, tenant_id blob NOT NULL UNIQUE, mode text NOT NULL, version integer NOT NULL, created_at datetime, updated_at datetime)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenant, other := identity.NewID(), identity.NewID()
	policy, err := effectivePublicationPolicy(context.Background(), db, tenant)
	if err != nil || policy.Mode != "MANUAL" || policy.Version != 0 {
		t.Fatalf("missing policy = %+v (%v), want MANUAL v0", policy, err)
	}
	if err := db.Exec("INSERT INTO reports.publication_policies (id, tenant_id, mode, version) VALUES (?, ?, 'AUTOMATIC', 3)", identity.NewID(), other).Error; err != nil {
		t.Fatal(err)
	}
	if policy, err = effectivePublicationPolicy(context.Background(), db, tenant); err != nil || policy.Mode != "MANUAL" {
		t.Fatalf("another tenant's policy leaked: %+v (%v)", policy, err)
	}
	if policy, err = effectivePublicationPolicy(context.Background(), db, other); err != nil || policy.Mode != "AUTOMATIC" || policy.Version != 3 {
		t.Fatalf("stored policy = %+v (%v), want AUTOMATIC v3", policy, err)
	}
}
