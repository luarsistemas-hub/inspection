package auth

import (
	"context"
	"os"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestMembershipContextIT002AndIT035(t *testing.T) {
	adminDSN, runtimeDSN := os.Getenv("INSPECTION_TEST_DATABASE_URL"), os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL")
	if adminDSN == "" || runtimeDSN == "" {
		t.Skip("INSPECTION_TEST_DATABASE_URL and INSPECTION_TEST_RUNTIME_DATABASE_URL not set")
	}
	admin, err := gorm.Open(postgres.Open(adminDSN), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := (database.Migrator{DB: admin}).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	runtimeDB, err := gorm.Open(postgres.Open(runtimeDSN), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	issuer, subject := "https://issuer.example", "subject-"+identity.NewID().String()
	identityID := identity.NewDeterministicID("inspection/oidc-identity", issuer+"\x00"+subject)
	tenants := []identity.ID{identity.NewID(), identity.NewID()}
	memberships := []identity.ID{identity.NewID(), identity.NewID()}
	for i, tenantID := range tenants {
		if err := admin.Create(&database.Tenant{ID: tenantID, TenantID: tenantID, Name: "tenant", Language: "pt-BR", DefaultTimezone: "UTC", Status: "ACTIVE", Version: 1}).Error; err != nil {
			t.Fatal(err)
		}
		if err := admin.Create(&database.Membership{ID: memberships[i], TenantID: tenantID, IdentityID: identityID, Issuer: issuer, Subject: subject, Role: TenantAdmin, Status: "ACTIVE", Version: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	store := GORMMembershipStore{DB: runtimeDB}
	for i, membershipID := range memberships {
		principal, err := store.ResolveMembership(context.Background(), issuer, subject, membershipID)
		if err != nil || principal.TenantID != tenants[i] || principal.MembershipID != membershipID {
			t.Fatalf("membership %d resolved %#v err=%v", i, principal, err)
		}
		if err := (tenanttx.Runner{DB: runtimeDB}).Within(context.Background(), principal.TenantID, func(tx *gorm.DB) error {
			var count int64
			if err := tx.Model(&database.Tenant{}).Where("id=?", tenants[1-i]).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				t.Fatalf("tenant %d saw foreign tenant", i)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.ResolveMembership(context.Background(), issuer, subject, identity.NewID()); err == nil {
		t.Fatal("foreign membership accepted")
	}
}
