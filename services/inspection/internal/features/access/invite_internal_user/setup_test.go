package invite_internal_user

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestHandleRejectsUnknownRoleBeforeDatabaseAccess(t *testing.T) {
	_, err := handle(adminContext(), Dependencies{}, Command{TenantID: testTenant, Name: "Ada", Email: "ada@example.test", Role: "ROOT", ClientID: "mutation"})
	if err == nil {
		t.Fatal("expected unknown role error")
	}
}

func TestHandleRejectsInvalidScopeBeforeDatabaseAccess(t *testing.T) {
	_, err := handle(adminContext(), Dependencies{}, Command{TenantID: testTenant, Name: "Ada", Email: "ada@example.test", Role: "MANAGER", ClientID: "mutation", Scopes: []Scope{{Kind: "UNKNOWN", ResourceID: identity.NewID()}}})
	if code, field, _ := apperror.Public(err); code != apperror.InvalidInput || field != "scopes" {
		t.Fatalf("code=%s field=%s err=%v", code, field, err)
	}
}

func TestTFE_IT_202HandleRejectsAdminEntitlementForOperationalRoleBeforeDatabaseAccess(t *testing.T) {
	for _, role := range []string{"MANAGER", "EMPLOYEE", "VIEWER", "CUSTOMER_VIEWER"} {
		_, err := handle(adminContext(), Dependencies{}, Command{TenantID: testTenant, Name: "Ada", Email: "ada@example.test", Role: role, ClientID: "mutation", ProductEntitlements: []string{"ADMIN"}})
		if code, field, _ := apperror.Public(err); code != apperror.InvalidInput || field != "productEntitlements" {
			t.Fatalf("%s code=%s field=%s err=%v", role, code, field, err)
		}
	}
}

// adminContext is a verified Admin token of a tenant administrator; the
// authorizer has no store, so no database is needed for validation tests.
func adminContext() context.Context {
	return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: testTenant, Principal: requestctx.Principal{IdentityID: identity.NewID(), MembershipID: identity.NewID(), TenantID: testTenant, Roles: []string{"TENANT_ADMIN"}, Audience: "inspection-admin", Product: "ADMIN", ProductEntitlements: []string{"ADMIN", "DASHBOARD"}}})
}

var testTenant = identity.NewID()
