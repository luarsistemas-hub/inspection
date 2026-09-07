package upsert_business_unit

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupRequiresDependencies(t *testing.T) {
	if err := Setup(Dependencies{}); err == nil {
		t.Fatal("expected missing dependency error")
	}
}

func TestHandleRejectsInvalidCreateBeforeDatabaseAccess(t *testing.T) {
	_, err := handle(context.Background(), Dependencies{}, Command{TenantID: identity.NewID(), Code: "", Name: "unit", IdempotencyKey: "mutation", ExpectedTenantVersion: 1})
	if err == nil {
		t.Fatal("expected invalid code error")
	}
}

func TestSetupRegistersCommand(t *testing.T) {
	bus := mediator.New()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Setup(Dependencies{DB: db, Bus: bus}); err != nil {
		t.Fatal(err)
	}
}
