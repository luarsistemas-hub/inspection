package auth

import (
	"context"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/requestctx"
	"testing"
)

func TestAuthorizationContractsUT017UT018(t *testing.T) {
	tenant, identityID, resource := identity.NewID(), identity.NewID(), identity.NewID()
	principal := requestctx.Principal{IdentityID: identityID, TenantID: tenant, Roles: []string{Manager}, Scopes: []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: resource}}}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: principal})
	if _, err := (Authorizer{}).Authorize(ctx, tenant, []string{Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: resource}, true); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		ctx    context.Context
		tenant identity.ID
		roles  []string
		scope  *requestctx.Scope
		mutate bool
	}{
		{"cross tenant", ctx, identity.NewID(), []string{Manager}, nil, false}, {"wrong scope", ctx, tenant, []string{Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: identity.NewID()}, false}, {"viewer mutate", requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{IdentityID: identityID, TenantID: tenant, Roles: []string{Viewer}}}), tenant, []string{Viewer}, nil, true}, {"missing", context.Background(), tenant, []string{Manager}, nil, false}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (Authorizer{}).Authorize(tc.ctx, tc.tenant, tc.roles, tc.scope, tc.mutate)
			if err == nil {
				t.Fatal("expected denial")
			}
			code, _, _ := apperror.Public(err)
			if code != apperror.Forbidden && code != apperror.NotFound && code != apperror.Unauthenticated {
				t.Fatalf("unexpected code %s", code)
			}
		})
	}
}

func TestProductAuthorizationRequiresAudienceAndEntitlement(t *testing.T) {
	tenant, subject := identity.NewID(), identity.NewID()
	base := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{IdentityID: subject, TenantID: tenant, Roles: []string{TenantAdmin}, ProductEntitlements: []string{AdminProduct}, Product: AdminProduct}})
	if _, err := (Authorizer{}).Authorize(base, AuthorizationRequest{TenantID: tenant, Product: AdminProduct, Roles: []string{TenantAdmin}}); err != nil {
		t.Fatalf("admin access denied: %v", err)
	}
	if _, err := (Authorizer{}).Authorize(base, AuthorizationRequest{TenantID: tenant, Product: DashboardProduct, Roles: []string{TenantAdmin}}); err == nil {
		t.Fatal("missing dashboard entitlement accepted")
	}
	if _, err := (Authorizer{}).Authorize(base, AuthorizationRequest{TenantID: identity.NewID(), Product: AdminProduct, Roles: []string{TenantAdmin}}); err == nil {
		t.Fatal("cross-tenant access accepted")
	}
}

func TestProductAuthorizationRejectsCustomerMutation(t *testing.T) {
	tenant, subject := identity.NewID(), identity.NewID()
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{IdentityID: subject, TenantID: tenant, Roles: []string{CustomerViewer}, ProductEntitlements: []string{DashboardProduct}, Product: DashboardProduct}})
	if _, err := (Authorizer{}).Authorize(ctx, AuthorizationRequest{TenantID: tenant, Product: DashboardProduct, Roles: []string{CustomerViewer}, Mutate: true}); err == nil {
		t.Fatal("customer mutation accepted")
	}
}
