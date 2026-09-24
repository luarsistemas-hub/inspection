package auth

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestAuthorizeSuperAdminRequiresConfiguredActiveAdminMembership(t *testing.T) {
	tenantID := identity.NewID()
	principal := requestctx.Principal{
		IdentityID: identity.NewID(), TenantID: tenantID, Issuer: "https://issuer", Subject: "super-admin",
		Product: AdminProduct, ProductEntitlements: []string{AdminProduct}, Roles: []string{TenantAdmin},
	}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: principal})

	if _, err := AuthorizeSuperAdmin(ctx, Authorizer{}, tenantID, principal.Issuer, principal.Subject); err != nil {
		t.Fatalf("configured super admin denied: %v", err)
	}
	if _, err := AuthorizeSuperAdmin(ctx, Authorizer{}, tenantID, principal.Issuer, "another-subject"); err == nil {
		t.Fatal("different subject accepted")
	}
	principal.Roles = []string{Manager}
	ctx = requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: principal})
	if _, err := AuthorizeSuperAdmin(ctx, Authorizer{}, tenantID, principal.Issuer, principal.Subject); err == nil {
		t.Fatal("manager accepted as super admin")
	}
}
