package invite_internal_user

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/access/accesstest"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

type internalFixture struct {
	env    accesstest.Env
	h      accesstest.Hierarchy
	deps   Dependencies
	sender *accesstest.Sender
	ctx    context.Context
}

func newInternalFixture(t *testing.T) internalFixture {
	t.Helper()
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	admin := env.AddMembership(t, h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct, auth.DashboardProduct})
	sender := &accesstest.Sender{}
	deps := Dependencies{DB: env.Runtime, Authorizer: env.Authorizer(), Issuer: "https://issuer.test", AdminOrigin: "https://admin.test", Provider: &accesstest.Provider{}, Sender: sender, Limits: &accesstest.Limits{}}
	return internalFixture{env: env, h: h, deps: deps, sender: sender, ctx: accesstest.Context(admin, "inspection-admin")}
}

func (f internalFixture) products(t *testing.T, membershipID string) []string {
	t.Helper()
	var products []string
	if err := f.env.Admin.Raw("SELECT product FROM access.product_entitlements WHERE tenant_id=? AND membership_id=? ORDER BY product", f.h.TenantID, membershipID).Scan(&products).Error; err != nil {
		t.Fatal(err)
	}
	return products
}

func TestTFE_IT_201InviteInternalUserAcceptsExplicitEntitlementsAndProjectScope(t *testing.T) {
	f := newInternalFixture(t)
	cases := []struct {
		role     string
		products []string
		want     []string
	}{
		{auth.Employee, []string{auth.DashboardProduct}, []string{auth.DashboardProduct}},
		{auth.Manager, nil, []string{auth.DashboardProduct}},
		{auth.AccessAdmin, []string{auth.AdminProduct}, []string{auth.AdminProduct}},
		{auth.TenantAdmin, []string{auth.DashboardProduct, auth.AdminProduct}, []string{auth.AdminProduct, auth.DashboardProduct}},
	}
	for index, tc := range cases {
		result, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, Name: "Pessoa Interna", Email: tc.role + "@example.test", Role: tc.role, ProductEntitlements: tc.products, Scopes: []Scope{{Kind: "PROJECT", ResourceID: f.h.ProjectA1}}, ClientID: "internal-" + tc.role})
		if err != nil || result.InvitationStatus != "SENT" {
			t.Fatalf("case %d (%s): result=%+v err=%v", index, tc.role, result, err)
		}
		got := f.products(t, result.Membership.ID.String())
		if len(got) != len(tc.want) {
			t.Fatalf("%s entitlements = %v, want %v", tc.role, got, tc.want)
		}
		for position := range got {
			if got[position] != tc.want[position] {
				t.Fatalf("%s entitlements = %v, want %v", tc.role, got, tc.want)
			}
		}
		if rows := f.env.Count(t, "access.resource_scopes", f.h.TenantID, "membership_id=? AND kind='PROJECT' AND resource_id=?", result.Membership.ID, f.h.ProjectA1); rows != 1 {
			t.Fatalf("%s PROJECT scope rows = %d, want 1", tc.role, rows)
		}
	}
}

func TestTFE_IT_202InviteInternalUserRejectsAdminForNonAdministrativeRolesButAcceptsDelegatedAdministrators(t *testing.T) {
	// ADR-003 supersedes "ADMIN only for TENANT_ADMIN": delegated administrative
	// roles are Admin-entitled; operational and customer roles never are.
	f := newInternalFixture(t)
	for _, role := range []string{auth.Manager, auth.Employee, auth.Viewer, auth.CustomerViewer} {
		_, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, Name: "Pessoa", Email: "negado-" + role + "@example.test", Role: role, ProductEntitlements: []string{auth.AdminProduct}, ClientID: "deny-" + role})
		if code, field, _ := apperror.Public(err); code != apperror.InvalidInput || field != "productEntitlements" {
			t.Fatalf("%s accepted ADMIN: code=%s field=%s err=%v", role, code, field, err)
		}
	}
	if rows := f.env.Count(t, "access.memberships", f.h.TenantID, "email LIKE 'negado-%'"); rows != 0 {
		t.Fatalf("rejected invitations left %d memberships", rows)
	}
	for _, role := range []string{auth.OrganizationAdmin, auth.AccessAdmin, auth.ParticipationAdmin, auth.InspectionConfigAdmin, auth.GovernanceAdmin, auth.Auditor} {
		if _, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, Name: "Delegado", Email: "ok-" + role + "@example.test", Role: role, ProductEntitlements: []string{auth.AdminProduct}, ClientID: "ok-" + role}); err != nil {
			t.Fatalf("delegated %s denied ADMIN: %v", role, err)
		}
	}
}

func TestTFE_IT_021InviteInternalUserRejectsCrossTenantScopeAndTenantAdminDelegationByAccessAdmin(t *testing.T) {
	f := newInternalFixture(t)
	other := f.env.SeedTenant(t)
	_, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, Name: "Pessoa", Email: "cross@example.test", Role: auth.Manager, Scopes: []Scope{{Kind: "BUSINESS_UNIT", ResourceID: other.UnitA}}, ClientID: "cross"})
	if code, field, _ := apperror.Public(err); code != apperror.InvalidInput || field != "scopes" {
		t.Fatalf("cross-tenant scope: code=%s field=%s err=%v", code, field, err)
	}
	if rows := f.env.Count(t, "access.memberships", f.h.TenantID, "email='cross@example.test'"); rows != 0 {
		t.Fatalf("rejected invitation left %d memberships", rows)
	}
	accessAdmin := f.env.AddMembership(t, f.h.TenantID, auth.AccessAdmin, "ACTIVE", []string{auth.AdminProduct})
	_, err = handle(accesstest.Context(accessAdmin, "inspection-admin"), f.deps, Command{TenantID: f.h.TenantID, Name: "Pessoa", Email: "admin2@example.test", Role: auth.TenantAdmin, ClientID: "tenant-admin"})
	if code, _, _ := apperror.Public(err); code != apperror.Forbidden {
		t.Fatalf("access administrator granted TENANT_ADMIN: code=%s err=%v", code, err)
	}
}

func TestTFE_IT_022InternalMembershipWithoutScopeReadsNoScopedResource(t *testing.T) {
	// The Admin UI may invite a member first and grant scope afterwards. Such a
	// membership is not data-bearing: only a current explicit grant opens a resource.
	f := newInternalFixture(t)
	result, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, Name: "Sem Escopo", Email: "semescopo@example.test", Role: auth.Manager, ClientID: "no-scope"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.env.Admin.Exec("UPDATE access.memberships SET status='ACTIVE' WHERE tenant_id=? AND id=?", f.h.TenantID, result.Membership.ID).Error; err != nil {
		t.Fatal(err)
	}
	_, err = f.deps.Authorizer.Authorize(accesstest.Context(result.Membership, "inspection-dashboard"), auth.AuthorizationRequest{
		TenantID: f.h.TenantID, Product: auth.DashboardProduct, Roles: []string{auth.Manager},
		Resource: scopeOf("ASSET", f.h.AssetA1),
	})
	if code, _, _ := apperror.Public(err); code != apperror.NotFound {
		t.Fatalf("member without scope reached an asset: code=%s err=%v", code, err)
	}
}

func scopeOf(kind string, id identity.ID) *requestctx.Scope {
	return &requestctx.Scope{Kind: kind, ID: id}
}

func TestTFE_IT_027InternalInvitationIsIdempotentPerClientMutationId(t *testing.T) {
	f := newInternalFixture(t)
	command := Command{TenantID: f.h.TenantID, Name: "Pessoa Interna", Email: "idem@example.test", Role: auth.Manager, Scopes: []Scope{{Kind: "ASSET", ResourceID: f.h.AssetA1}}, ClientID: "internal-idem"}
	first, err := handle(f.ctx, f.deps, command)
	if err != nil || first.InvitationStatus != "SENT" {
		t.Fatalf("first = %+v err=%v", first, err)
	}
	replay, err := handle(f.ctx, f.deps, command)
	if err != nil || replay.Membership.ID != first.Membership.ID || len(f.sender.Sent) != 1 {
		t.Fatalf("replay = %+v err=%v mails=%d", replay, err, len(f.sender.Sent))
	}
	other := command
	other.Role = auth.Employee
	if _, err := handle(f.ctx, f.deps, other); err == nil {
		t.Fatal("the key was reused with another role")
	} else if code, field, _ := apperror.Public(err); code != apperror.Conflict || field != "clientMutationId" {
		t.Fatalf("same key with another role: code=%s field=%s err=%v", code, field, err)
	}
	if rows := f.env.Count(t, "access.memberships", f.h.TenantID, "email='idem@example.test'"); rows != 1 {
		t.Fatalf("memberships = %d, want 1", rows)
	}
}

func TestTFE_InternalInvitationIsRateLimitedBeforeAnySideEffect(t *testing.T) {
	f := newInternalFixture(t)
	f.deps.Limits = &accesstest.Limits{Deny: true}
	_, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, Name: "Pessoa", Email: "limite@example.test", Role: auth.Manager, ClientID: "rate"})
	if code, _, _ := apperror.Public(err); code != apperror.RateLimited {
		t.Fatalf("code=%s err=%v, want RATE_LIMITED", code, err)
	}
	if rows := f.env.Count(t, "access.memberships", f.h.TenantID, "email='limite@example.test'"); rows != 0 || len(f.sender.Sent) != 0 {
		t.Fatalf("a throttled invitation left %d memberships and %d mails", rows, len(f.sender.Sent))
	}
}
