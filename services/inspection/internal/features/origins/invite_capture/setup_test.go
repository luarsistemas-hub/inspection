package invite_capture

import (
	"context"
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestReviewerMustBeActiveEmployeeScopedToBusinessUnitHelper(t *testing.T) {
	db, tenant, unit, employee := reviewerFixture(t, auth.Employee, true)
	if err := validateReviewer(context.Background(), db, tenant, employee, identity.NewID(), unit); err != nil {
		t.Fatalf("scoped employee rejected: %v", err)
	}
}

func TestReviewerViewerRoleIsRejectedHelper(t *testing.T) {
	db, tenant, unit, viewer := reviewerFixture(t, auth.Viewer, true)
	if err := validateReviewer(context.Background(), db, tenant, viewer, identity.NewID(), unit); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("viewer reviewer returned %v", err)
	}
}

func TestReviewerOutsideBusinessUnitIsRejectedHelper(t *testing.T) {
	db, tenant, unit, employee := reviewerFixture(t, auth.Employee, false)
	if err := validateReviewer(context.Background(), db, tenant, employee, identity.NewID(), unit); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("out-of-scope reviewer returned %v", err)
	}
}

func TestB16ReviewerValidatorAcceptsScopedAssetMembership(t *testing.T) {
	db, tenant, unit, employee := reviewerFixture(t, auth.Employee, false)
	assetID := identity.NewID()
	if err := db.Create(&database.ResourceScope{ID: identity.NewID(), TenantID: tenant, MembershipID: employee, Kind: "ASSET", ResourceID: assetID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := validateReviewer(context.Background(), db, tenant, employee, assetID, unit); err != nil {
		t.Fatalf("asset-scoped reviewer rejected by shared reviewer policy: %v", err)
	}
}

func reviewerFixture(t *testing.T, role string, inScope bool) (*gorm.DB, identity.ID, identity.ID, identity.ID) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS access").Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, model := range []any{&database.Membership{}, &database.ResourceScope{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenant, unit, membershipID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Create(&database.Membership{ID: membershipID, TenantID: tenant, Role: role, Status: "ACTIVE", Issuer: "issuer", Subject: "subject", Name: "Reviewer", Email: "reviewer@example.test"}).Error; err != nil {
		t.Fatal(err)
	}
	scopeUnit := unit
	if !inScope {
		scopeUnit = identity.NewID()
	}
	if err := db.Create(&database.ResourceScope{ID: identity.NewID(), TenantID: tenant, MembershipID: membershipID, Kind: "BUSINESS_UNIT", ResourceID: scopeUnit}).Error; err != nil {
		t.Fatal(err)
	}
	return db, tenant, unit, membershipID
}
