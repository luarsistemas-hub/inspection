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

type membershipStoreFunc func(context.Context, identity.ID, identity.ID) (requestctx.Principal, error)

func (f membershipStoreFunc) Resolve(ctx context.Context, tenantID, identityID identity.ID) (requestctx.Principal, error) {
	return f(ctx, tenantID, identityID)
}

type scopeResolverFunc func(context.Context, identity.ID, identity.ID, requestctx.Scope) (ScopeDecision, error)

func (f scopeResolverFunc) Resolve(ctx context.Context, tenantID, membershipID identity.ID, scope requestctx.Scope) (ScopeDecision, error) {
	return f(ctx, tenantID, membershipID, scope)
}

type mutationScopeResolver struct {
	archived  bool
	grantKind string
}

func (r mutationScopeResolver) Resolve(_ context.Context, _, _ identity.ID, target requestctx.Scope) (ScopeDecision, error) {
	return ScopeDecision{Allowed: r.grantKind == target.Kind || r.grantKind == "BUSINESS_UNIT", DirectGrant: requestctx.Scope{Kind: r.grantKind}}, nil
}

func (r mutationScopeResolver) ValidateMutationTarget(context.Context, identity.ID, requestctx.Scope) (bool, error) {
	return r.archived, nil
}

func (r mutationScopeResolver) ResolveNewWork(_ context.Context, _, _ identity.ID, target requestctx.Scope) (ScopeDecision, bool, error) {
	return ScopeDecision{Allowed: r.grantKind == target.Kind || r.grantKind == "BUSINESS_UNIT", Target: target}, r.archived, nil
}

func TestTFE_IT_431ArchivedDirectAndInheritedGrantsBlockMutationsOnly(t *testing.T) {
	tenant, subject, target := identity.NewID(), identity.NewID(), identity.NewID()
	for _, grantKind := range []string{"ASSET", "BUSINESS_UNIT"} {
		t.Run(grantKind, func(t *testing.T) {
			ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{IdentityID: subject, TenantID: tenant, Roles: []string{Manager}}})
			resolver := mutationScopeResolver{archived: true, grantKind: grantKind}
			authorizer := Authorizer{Scopes: resolver}
			scope := requestctx.Scope{Kind: "ASSET", ID: target}
			if _, err := authorizer.Authorize(ctx, AuthorizationRequest{TenantID: tenant, Roles: []string{Manager}, Resource: &scope}); err != nil {
				t.Fatalf("historical read denied for %s grant: %v", grantKind, err)
			}
			if _, err := authorizer.Authorize(ctx, AuthorizationRequest{TenantID: tenant, Roles: []string{Manager}, Resource: &scope, Mutate: true}); err != nil {
				t.Fatalf("existing-work mutation denied for %s grant: %v", grantKind, err)
			}
			if _, err := authorizer.Authorize(ctx, AuthorizationRequest{TenantID: tenant, Roles: []string{Manager}, Resource: &scope, Mutate: true, NewWork: true}); err == nil {
				t.Fatalf("archived resource mutation accepted for %s grant", grantKind)
			} else if code, _, _ := apperror.Public(err); code != apperror.NotFound {
				t.Fatalf("archived mutation code=%s, want NOT_FOUND", code)
			}
		})
	}
}

func TestTFE_IT_431ManagerCanCancelArchivedInspectionAndReadHistory(t *testing.T) {
	tenant, subject := identity.NewID(), identity.NewID()
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{IdentityID: subject, TenantID: tenant, Roles: []string{Manager}}})
	inspection := requestctx.Scope{Kind: "INSPECTION", ID: identity.NewID()}
	authorizer := Authorizer{Scopes: mutationScopeResolver{archived: true, grantKind: "BUSINESS_UNIT"}}
	if _, err := authorizer.Authorize(ctx, AuthorizationRequest{TenantID: tenant, Roles: []string{Manager}, Resource: &inspection}); err != nil {
		t.Fatalf("historical inspection read denied: %v", err)
	}
	if _, err := authorizer.Authorize(ctx, AuthorizationRequest{TenantID: tenant, Roles: []string{Manager}, Resource: &inspection, Mutate: true}); err != nil {
		t.Fatalf("inspection cancellation denied: %v", err)
	}
	if _, err := authorizer.Authorize(ctx, AuthorizationRequest{TenantID: tenant, Roles: []string{Manager}, Resource: &inspection, Mutate: true, NewWork: true}); err == nil {
		t.Fatal("new work accepted in archived inspection scope")
	} else if code, _, _ := apperror.Public(err); code != apperror.NotFound {
		t.Fatalf("new work error code=%s, want NOT_FOUND", code)
	}
}

func TestTFE_UT_002AndUT_006ResolvedMembershipPreservesAudienceAndEnforcesProduct(t *testing.T) {
	tenant, subject := identity.NewID(), identity.NewID()
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{
		IdentityID: subject, TenantID: tenant, Audience: "inspection-admin", Product: AdminProduct,
	}})
	store := membershipStoreFunc(func(context.Context, identity.ID, identity.ID) (requestctx.Principal, error) {
		return requestctx.Principal{IdentityID: subject, TenantID: tenant, Roles: []string{TenantAdmin}, ProductEntitlements: []string{AdminProduct}}, nil
	})
	_, err := (Authorizer{Store: store}).Authorize(ctx, AuthorizationRequest{TenantID: tenant, Product: DashboardProduct, Roles: []string{TenantAdmin}})
	if code, _, _ := apperror.Public(err); code != apperror.Forbidden {
		t.Fatalf("wrong audience/product result=%v code=%s", err, code)
	}
}

func TestTFE_UT_008TenantAdminBypassesResourceScopeForAdminAndDashboard(t *testing.T) {
	tenant, subject, resource := identity.NewID(), identity.NewID(), identity.NewID()
	resolver := scopeResolverFunc(func(context.Context, identity.ID, identity.ID, requestctx.Scope) (ScopeDecision, error) {
		return ScopeDecision{}, nil
	})
	for _, productAudience := range []struct{ product, audience string }{{AdminProduct, "inspection-admin"}, {DashboardProduct, "inspection-dashboard"}} {
		ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{
			IdentityID: subject, TenantID: tenant, Roles: []string{TenantAdmin}, Audience: productAudience.audience, Product: productAudience.product, ProductEntitlements: []string{productAudience.product},
		}})
		if _, err := (Authorizer{Scopes: resolver}).Authorize(ctx, AuthorizationRequest{TenantID: tenant, Product: productAudience.product, Roles: []string{TenantAdmin}, Resource: &requestctx.Scope{Kind: "ASSET", ID: resource}}); err != nil {
			t.Fatalf("tenant admin with %s audience was denied: %v", productAudience.audience, err)
		}
	}

	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{IdentityID: subject, TenantID: tenant, Roles: []string{Manager}, Audience: "inspection-dashboard", Product: DashboardProduct, ProductEntitlements: []string{DashboardProduct}}})
	_, err := (Authorizer{Scopes: resolver}).Authorize(ctx, AuthorizationRequest{TenantID: tenant, Product: DashboardProduct, Roles: []string{Manager}, Resource: &requestctx.Scope{Kind: "ASSET", ID: resource}})
	if code, _, _ := apperror.Public(err); code != apperror.NotFound {
		t.Fatalf("manager without grant result=%v code=%s, want NOT_FOUND", err, code)
	}
}

func TestTFE_UT_008TenantAdminBypassesOnlyTestModeWithoutAudience(t *testing.T) {
	tenant, subject, resource := identity.NewID(), identity.NewID(), identity.NewID()
	resolver := scopeResolverFunc(func(context.Context, identity.ID, identity.ID, requestctx.Scope) (ScopeDecision, error) {
		return ScopeDecision{}, nil
	})
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{IdentityID: subject, TenantID: tenant, Roles: []string{TenantAdmin}}})
	if _, err := (Authorizer{Scopes: resolver}).Authorize(ctx, AuthorizationRequest{TenantID: tenant, Roles: []string{TenantAdmin}, Resource: &requestctx.Scope{Kind: "ASSET", ID: resource}, NewWork: true}); err != nil {
		t.Errorf("test-mode tenant admin without audience was denied: %v", err)
	}
	for _, audience := range []string{"inspection-api", "unknown-audience", "test-auth"} {
		ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{IdentityID: subject, TenantID: tenant, Roles: []string{TenantAdmin}, Audience: audience}})
		if _, err := (Authorizer{Scopes: resolver}).Authorize(ctx, AuthorizationRequest{TenantID: tenant, Roles: []string{TenantAdmin}, Resource: &requestctx.Scope{Kind: "ASSET", ID: resource}}); err == nil {
			t.Errorf("non-internal audience %q bypassed resource scope", audience)
		}
	}
}

func TestTFE_UT_006DisabledMembershipRejectsBeforeResourceIO(t *testing.T) {
	tenant, subject := identity.NewID(), identity.NewID()
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{IdentityID: subject, TenantID: tenant, Audience: "inspection-dashboard", Product: DashboardProduct}})
	resourceCalls := 0
	store := membershipStoreFunc(func(context.Context, identity.ID, identity.ID) (requestctx.Principal, error) {
		return requestctx.Principal{IdentityID: subject, TenantID: tenant, Roles: []string{Manager}, Disabled: true, ProductEntitlements: []string{DashboardProduct}}, nil
	})
	scopes := scopeResolverFunc(func(context.Context, identity.ID, identity.ID, requestctx.Scope) (ScopeDecision, error) {
		resourceCalls++
		return ScopeDecision{Allowed: true}, nil
	})
	_, err := (Authorizer{Store: store, Scopes: scopes}).Authorize(ctx, AuthorizationRequest{TenantID: tenant, Product: DashboardProduct, Roles: []string{Manager}, Resource: &requestctx.Scope{Kind: "ASSET", ID: identity.NewID()}})
	if err == nil || resourceCalls != 0 {
		t.Fatalf("disabled membership err=%v resource calls=%d", err, resourceCalls)
	}
}

func TestTFE_UT_004CrossTenantTargetMapsToNotFoundWithoutRevealingExistence(t *testing.T) {
	tenant, other, subject := identity.NewID(), identity.NewID(), identity.NewID()
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{IdentityID: subject, TenantID: tenant, Roles: []string{TenantAdmin}, Audience: "inspection-admin", Product: AdminProduct, ProductEntitlements: []string{AdminProduct}}})
	_, err := (Authorizer{}).Authorize(ctx, AuthorizationRequest{TenantID: other, Product: AdminProduct, Roles: []string{TenantAdmin}})
	code, _, message := apperror.Public(err)
	if code != apperror.NotFound || message != "Recurso não encontrado." {
		t.Fatalf("cross-tenant target code=%s message=%q, want NOT_FOUND", code, message)
	}
}

func TestTFE_UT_005MutationIsRejectedForViewerAndCustomerViewerRegardlessOfProduct(t *testing.T) {
	tenant, subject := identity.NewID(), identity.NewID()
	for _, role := range []string{Viewer, CustomerViewer} {
		ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{IdentityID: subject, TenantID: tenant, Roles: []string{role}, Audience: "inspection-dashboard", Product: DashboardProduct, ProductEntitlements: []string{DashboardProduct}}})
		_, err := (Authorizer{}).Authorize(ctx, AuthorizationRequest{TenantID: tenant, Product: DashboardProduct, Roles: []string{role}, Mutate: true})
		if code, _, _ := apperror.Public(err); code != apperror.Forbidden {
			t.Fatalf("%s mutation code=%s err=%v, want FORBIDDEN", role, code, err)
		}
		if _, err := (Authorizer{}).Authorize(ctx, AuthorizationRequest{TenantID: tenant, Product: DashboardProduct, Roles: []string{role}}); err != nil {
			t.Fatalf("%s read denied: %v", role, err)
		}
	}
}

func TestTFE_UT_003LegacyCallerIsBoundToTheTokenAudienceEntitlementAndExplicitProductChecksAudience(t *testing.T) {
	tenant, subject := identity.NewID(), identity.NewID()
	ctx := func(audience string, entitlements ...string) context.Context {
		return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{IdentityID: subject, TenantID: tenant, MembershipID: identity.NewID(), Roles: []string{Manager}, Audience: audience, Product: productForAudience(audience), ProductEntitlements: entitlements}})
	}
	legacy := func(c context.Context) error {
		_, err := (Authorizer{}).Authorize(c, tenant, []string{Manager}, nil, false)
		return err
	}
	if err := legacy(ctx("inspection-dashboard", DashboardProduct)); err != nil {
		t.Fatalf("entitled legacy caller denied: %v", err)
	}
	if err := legacy(ctx("inspection-dashboard")); err == nil {
		t.Fatal("a legacy caller kept access after the DASHBOARD entitlement was revoked")
	} else if code, _, _ := apperror.Public(err); code != apperror.Forbidden {
		t.Fatalf("revoked legacy caller code=%s", code)
	}
	// An explicit product also rejects a token minted for the other product.
	_, err := (Authorizer{}).Authorize(ctx("inspection-admin", AdminProduct, DashboardProduct), AuthorizationRequest{TenantID: tenant, Product: DashboardProduct, Roles: []string{Manager}})
	if code, _, _ := apperror.Public(err); code != apperror.Forbidden {
		t.Fatalf("wrong-audience token code=%s err=%v, want FORBIDDEN", code, err)
	}
	// Server-side orchestration is exempt only with the explicit Internal marker.
	system := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{IdentityID: tenant, TenantID: tenant, Roles: []string{TenantAdmin}, Internal: true}})
	if _, err := (Authorizer{}).Authorize(system, AuthorizationRequest{TenantID: tenant, Product: DashboardProduct, Roles: []string{TenantAdmin}}); err != nil {
		t.Fatalf("internal principal denied: %v", err)
	}
	// A principal that merely lacks audience, membership and entitlements is NOT exempt.
	unmarked := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{IdentityID: tenant, TenantID: tenant, Roles: []string{TenantAdmin}}})
	if _, err := (Authorizer{}).Authorize(unmarked, AuthorizationRequest{TenantID: tenant, Product: DashboardProduct, Roles: []string{TenantAdmin}}); err == nil {
		t.Fatal("an unmarked principal without entitlements bypassed the product check")
	}
}
