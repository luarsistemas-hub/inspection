package assign_membership_access

import (
	"context"
	"sort"
	"sync"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/access/accesstest"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

type fixture struct {
	env   accesstest.Env
	h     accesstest.Hierarchy
	deps  Dependencies
	admin database.Membership
	ctx   context.Context
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	admin := env.AddMembership(t, h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct, auth.DashboardProduct})
	return fixture{env: env, h: h, deps: Dependencies{DB: env.Runtime, Authorizer: env.Authorizer()}, admin: admin, ctx: accesstest.Context(admin, "inspection-admin")}
}

type state struct {
	Role     string
	Version  int64
	Products []string
	Scopes   []string
}

func (f fixture) state(t *testing.T, membershipID identity.ID) state {
	t.Helper()
	var membership database.Membership
	if err := f.env.Admin.Where("tenant_id=? AND id=?", f.h.TenantID, membershipID).First(&membership).Error; err != nil {
		t.Fatal(err)
	}
	result := state{Role: membership.Role, Version: membership.Version}
	if err := f.env.Admin.Raw("SELECT product FROM access.product_entitlements WHERE tenant_id=? AND membership_id=? ORDER BY product", f.h.TenantID, membershipID).Scan(&result.Products).Error; err != nil {
		t.Fatal(err)
	}
	var scopes []database.ResourceScope
	if err := f.env.Admin.Where("tenant_id=? AND membership_id=?", f.h.TenantID, membershipID).Find(&scopes).Error; err != nil {
		t.Fatal(err)
	}
	for _, scope := range scopes {
		result.Scopes = append(result.Scopes, scope.Kind+":"+scope.ResourceID.String())
	}
	sort.Strings(result.Scopes)
	return result
}

func scopeKey(kind string, id identity.ID) string { return kind + ":" + id.String() }

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

func TestTFE_IT_203AssignMembershipAccessReplacesRoleEntitlementsAndScopesAtomically(t *testing.T) {
	f := newFixture(t)
	target := f.env.AddMembership(t, f.h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: f.h.UnitA}, accesstest.Grant{Kind: "ASSET", ResourceID: f.h.AssetB1})
	result, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, MembershipID: target.ID, Role: auth.Employee, ProductEntitlements: []string{auth.DashboardProduct}, Scopes: []Scope{{Kind: "ASSET", ResourceID: f.h.AssetB1}, {Kind: "PROJECT", ResourceID: f.h.ProjectA1}}, ExpectedVersion: 1})
	if err != nil || result.Version != 2 || result.Role != auth.Employee {
		t.Fatalf("result = %+v err=%v", result, err)
	}
	got := f.state(t, target.ID)
	want := []string{scopeKey("ASSET", f.h.AssetB1), scopeKey("PROJECT", f.h.ProjectA1)}
	sort.Strings(want)
	if got.Role != auth.Employee || got.Version != 2 || !equalStrings(got.Products, []string{auth.DashboardProduct}) || !equalStrings(got.Scopes, want) {
		t.Fatalf("stored state = %+v, want scopes %v", got, want)
	}
	if rows := f.env.Count(t, "audit.events", f.h.TenantID, "action='access.membership_access_assigned' AND target_id=?", target.ID.String()); rows != 1 {
		t.Fatalf("audit rows = %d, want 1", rows)
	}
}

func TestTFE_IT_203AssignMembershipAccessCanGrantAndRevokeEntitlementsForDelegatedAdministrator(t *testing.T) {
	f := newFixture(t)
	target := f.env.AddMembership(t, f.h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: f.h.UnitA})
	if _, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, MembershipID: target.ID, Role: auth.GovernanceAdmin, ProductEntitlements: []string{auth.AdminProduct}, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if got := f.state(t, target.ID); got.Role != auth.GovernanceAdmin || !equalStrings(got.Products, []string{auth.AdminProduct}) || len(got.Scopes) != 0 {
		t.Fatalf("delegated administrator state = %+v", got)
	}
}

func TestTFE_IT_204StaleAssignMembershipAccessReturnsConflictAndLeavesEveryRowUnchanged(t *testing.T) {
	f := newFixture(t)
	target := f.env.AddMembership(t, f.h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: f.h.UnitA})
	if _, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, MembershipID: target.ID, Role: auth.Manager, ProductEntitlements: []string{auth.DashboardProduct}, Scopes: []Scope{{Kind: "BUSINESS_UNIT", ResourceID: f.h.UnitB}}, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	before := f.state(t, target.ID)
	_, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, MembershipID: target.ID, Role: auth.Viewer, ProductEntitlements: []string{auth.DashboardProduct}, Scopes: []Scope{{Kind: "ASSET", ResourceID: f.h.AssetA1}}, ExpectedVersion: 1})
	if code, field, _ := apperror.Public(err); code != apperror.Conflict || field != "expectedVersion" {
		t.Fatalf("code=%s field=%s err=%v", code, field, err)
	}
	if after := f.state(t, target.ID); after.Role != before.Role || after.Version != before.Version || !equalStrings(after.Products, before.Products) || !equalStrings(after.Scopes, before.Scopes) {
		t.Fatalf("stale write changed state: before=%+v after=%+v", before, after)
	}
	for _, missing := range []int64{0, -1} {
		_, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, MembershipID: target.ID, Role: auth.Manager, ExpectedVersion: missing})
		if code, field, _ := apperror.Public(err); code != apperror.InvalidInput || field != "expectedVersion" {
			t.Fatalf("expectedVersion=%d accepted: code=%s field=%s", missing, code, field)
		}
	}
}

func TestTFE_IT_025ConcurrentAssignmentsApplyExactlyOneWholeDecision(t *testing.T) {
	f := newFixture(t)
	target := f.env.AddMembership(t, f.h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: f.h.UnitA})
	commands := []Command{
		{TenantID: f.h.TenantID, MembershipID: target.ID, Role: auth.Viewer, ProductEntitlements: []string{auth.DashboardProduct}, Scopes: []Scope{{Kind: "ASSET", ResourceID: f.h.AssetA1}}, ExpectedVersion: 1},
		{TenantID: f.h.TenantID, MembershipID: target.ID, Role: auth.Employee, ProductEntitlements: []string{auth.DashboardProduct}, Scopes: []Scope{{Kind: "INSPECTION", ResourceID: f.h.InspectionA2}}, ExpectedVersion: 1},
	}
	errs := make([]error, len(commands))
	var wg sync.WaitGroup
	for index := range commands {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[index] = handle(f.ctx, f.deps, commands[index])
		}()
	}
	wg.Wait()
	winners, conflicts, winner := 0, 0, -1
	for index, err := range errs {
		switch {
		case err == nil:
			winners++
			winner = index
		default:
			if code, _, _ := apperror.Public(err); code == apperror.Conflict {
				conflicts++
			} else {
				t.Fatalf("unexpected failure %d: %v", index, err)
			}
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d, want exactly one of each", winners, conflicts)
	}
	got := f.state(t, target.ID)
	want := commands[winner]
	if got.Role != want.Role || got.Version != 2 || len(got.Scopes) != 1 || got.Scopes[0] != scopeKey(want.Scopes[0].Kind, want.Scopes[0].ResourceID) {
		t.Fatalf("final state %+v is not exactly the winning decision %+v", got, want)
	}
}

func TestTFE_IT_205RemovingEntitlementOrScopeDeniesNextProtectedRequestWithoutTokenExpiry(t *testing.T) {
	f := newFixture(t)
	target := f.env.AddMembership(t, f.h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: f.h.UnitA})
	dashboard := accesstest.Context(target, "inspection-dashboard")
	request := func() error {
		_, err := f.deps.Authorizer.Authorize(dashboard, auth.AuthorizationRequest{TenantID: f.h.TenantID, Product: auth.DashboardProduct, Roles: []string{auth.Manager}, Resource: &requestctx.Scope{Kind: "ASSET", ID: f.h.AssetA1}})
		return err
	}
	if err := request(); err != nil {
		t.Fatalf("baseline request denied: %v", err)
	}
	// Scope revoked: the same token is now outside the grant.
	if _, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, MembershipID: target.ID, Role: auth.Manager, ProductEntitlements: []string{auth.DashboardProduct}, Scopes: []Scope{{Kind: "BUSINESS_UNIT", ResourceID: f.h.UnitB}}, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if err := request(); err == nil {
		t.Fatal("revoked scope still authorizes the next request")
	} else if code, _, _ := apperror.Public(err); code != apperror.NotFound {
		t.Fatalf("revoked scope code=%s, want NOT_FOUND", code)
	}
	// Product entitlement revoked: the Dashboard token is refused before any resource lookup.
	if _, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, MembershipID: target.ID, Role: auth.GovernanceAdmin, ProductEntitlements: []string{auth.AdminProduct}, ExpectedVersion: 2}); err != nil {
		t.Fatal(err)
	}
	if err := request(); err == nil {
		t.Fatal("revoked entitlement still authorizes the next request")
	} else if code, _, _ := apperror.Public(err); code != apperror.Forbidden {
		t.Fatalf("revoked entitlement code=%s, want FORBIDDEN", code)
	}
}

func TestTFE_IT_021RejectedAssignmentLeavesRoleVersionEntitlementsAndScopesIntact(t *testing.T) {
	f := newFixture(t)
	other := f.env.SeedTenant(t)
	target := f.env.AddMembership(t, f.h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: f.h.UnitA})
	before := f.state(t, target.ID)
	cases := map[string]Command{
		"cross-tenant scope":     {TenantID: f.h.TenantID, MembershipID: target.ID, Role: auth.Employee, Scopes: []Scope{{Kind: "ASSET", ResourceID: f.h.AssetA1}, {Kind: "BUSINESS_UNIT", ResourceID: other.UnitA}}, ExpectedVersion: 1},
		"unknown role":           {TenantID: f.h.TenantID, MembershipID: target.ID, Role: "ROOT", ExpectedVersion: 1},
		"admin for operational":  {TenantID: f.h.TenantID, MembershipID: target.ID, Role: auth.Manager, ProductEntitlements: []string{auth.AdminProduct}, ExpectedVersion: 1},
		"customer without scope": {TenantID: f.h.TenantID, MembershipID: target.ID, Role: auth.CustomerViewer, ExpectedVersion: 1},
		"customer with admin":    {TenantID: f.h.TenantID, MembershipID: target.ID, Role: auth.CustomerViewer, ProductEntitlements: []string{auth.AdminProduct}, Scopes: []Scope{{Kind: "ASSET", ResourceID: f.h.AssetA1}}, ExpectedVersion: 1},
		"too many scopes":        {TenantID: f.h.TenantID, MembershipID: target.ID, Role: auth.Manager, Scopes: make([]Scope, 101), ExpectedVersion: 1},
	}
	for name, command := range cases {
		if _, err := handle(f.ctx, f.deps, command); err == nil {
			t.Fatalf("%s accepted", name)
		}
		if after := f.state(t, target.ID); after.Role != before.Role || after.Version != before.Version || !equalStrings(after.Products, before.Products) || !equalStrings(after.Scopes, before.Scopes) {
			t.Fatalf("%s changed state: before=%+v after=%+v", name, before, after)
		}
	}
}

func TestTFE_IT_024AssignmentCannotTargetAnotherTenantMembershipOrEscalateBeyondTheGrantor(t *testing.T) {
	f := newFixture(t)
	other := f.env.SeedTenant(t)
	foreign := f.env.AddMembership(t, other.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct})
	if _, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, MembershipID: foreign.ID, Role: auth.Viewer, ExpectedVersion: 1}); err == nil {
		t.Fatal("assignment reached a membership of another tenant")
	} else if code, _, _ := apperror.Public(err); code != apperror.NotFound {
		t.Fatalf("cross-tenant code=%s, want NOT_FOUND", code)
	}
	accessAdmin := f.env.AddMembership(t, f.h.TenantID, auth.AccessAdmin, "ACTIVE", []string{auth.AdminProduct})
	tenantAdmin := f.env.AddMembership(t, f.h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct, auth.DashboardProduct})
	manager := f.env.AddMembership(t, f.h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct})
	delegate := accesstest.Context(accessAdmin, "inspection-admin")
	for name, command := range map[string]Command{
		"grant tenant admin":  {TenantID: f.h.TenantID, MembershipID: manager.ID, Role: auth.TenantAdmin, ExpectedVersion: 1},
		"modify tenant admin": {TenantID: f.h.TenantID, MembershipID: tenantAdmin.ID, Role: auth.Viewer, ExpectedVersion: 1},
		"escalate own access": {TenantID: f.h.TenantID, MembershipID: accessAdmin.ID, Role: auth.AccessAdmin, Scopes: []Scope{{Kind: "BUSINESS_UNIT", ResourceID: f.h.UnitA}}, ExpectedVersion: 1},
	} {
		if _, err := handle(delegate, f.deps, command); err == nil {
			t.Fatalf("%s accepted for an access administrator", name)
		} else if code, _, _ := apperror.Public(err); code != apperror.Forbidden {
			t.Fatalf("%s code=%s, want FORBIDDEN", name, code)
		}
	}
	if got := f.state(t, tenantAdmin.ID); got.Role != auth.TenantAdmin {
		t.Fatalf("tenant administrator was modified: %+v", got)
	}
	// An access administrator may still grant a delegated or operational role.
	if _, err := handle(delegate, f.deps, Command{TenantID: f.h.TenantID, MembershipID: manager.ID, Role: auth.Employee, Scopes: []Scope{{Kind: "ASSET", ResourceID: f.h.AssetA1}}, ExpectedVersion: 1}); err != nil {
		t.Fatalf("access administrator denied a legitimate grant: %v", err)
	}
}

func TestTFE_IT_028AssignmentPreservesScopeOfInvitedMembershipAndRejectsDisabled(t *testing.T) {
	f := newFixture(t)
	invited := f.env.AddMembership(t, f.h.TenantID, auth.CustomerViewer, "INVITED", []string{auth.DashboardProduct})
	if _, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, MembershipID: invited.ID, Role: auth.CustomerViewer, ProductEntitlements: []string{auth.DashboardProduct}, Scopes: []Scope{{Kind: "ASSET", ResourceID: f.h.AssetA1}}, ExpectedVersion: 1}); err != nil {
		t.Fatalf("grant before acceptance rejected: %v", err)
	}
	if got := f.state(t, invited.ID); len(got.Scopes) != 1 {
		t.Fatalf("scope was not preserved for the invited membership: %+v", got)
	}
	disabled := f.env.AddMembership(t, f.h.TenantID, auth.Manager, "DISABLED", []string{auth.DashboardProduct})
	if _, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, MembershipID: disabled.ID, Role: auth.Manager, ExpectedVersion: 1}); err == nil {
		t.Fatal("disabled membership accepted an assignment")
	} else if code, _, _ := apperror.Public(err); code != apperror.InvalidState {
		t.Fatalf("code=%s, want INVALID_STATE", code)
	}
}

func TestTFE_IT_023ExcessiveGrantBatchIsRejectedWithGuidanceAndExistingGrantsRemain(t *testing.T) {
	// No per-tenant volume is defined by the TechSpec; the enforced limit is the
	// per-command batch of 100 explicit scopes (pending product decision).
	f := newFixture(t)
	target := f.env.AddMembership(t, f.h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: f.h.UnitA})
	scopes := make([]Scope, 0, 101)
	for index := 0; index < 101; index++ {
		scopes = append(scopes, Scope{Kind: "ASSET", ResourceID: identity.NewID()})
	}
	_, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, MembershipID: target.ID, Role: auth.Manager, Scopes: scopes, ExpectedVersion: 1})
	code, field, message := apperror.Public(err)
	if code != apperror.InvalidInput || field != "scopes" || message != "Informe no máximo 100 abrangências por vez." {
		t.Fatalf("code=%s field=%s message=%q", code, field, message)
	}
	if got := f.state(t, target.ID); !equalStrings(got.Scopes, []string{scopeKey("BUSINESS_UNIT", f.h.UnitA)}) || got.Version != 1 {
		t.Fatalf("existing grants changed: %+v", got)
	}
}

func TestSetupRejectsMissingDependencies(t *testing.T) {
	if err := Setup(Dependencies{}); err == nil {
		t.Fatal("expected a missing dependency error")
	}
}

func TestTFE_IT_198DatabaseGuardRejectsAdminEntitlementForCustomerInEitherDirection(t *testing.T) {
	f := newFixture(t)
	customer := f.env.AddMembership(t, f.h.TenantID, auth.CustomerViewer, "ACTIVE", []string{auth.DashboardProduct})
	// An ADMIN entitlement for a CUSTOMER_VIEWER cannot be committed.
	err := f.env.Admin.Create(&database.ProductEntitlement{ID: identity.NewID(), TenantID: f.h.TenantID, MembershipID: customer.ID, Product: auth.AdminProduct}).Error
	if err == nil {
		t.Fatal("database accepted ADMIN for CUSTOMER_VIEWER")
	}
	// Turning a membership that holds ADMIN into a customer cannot be committed either.
	manager := f.env.AddMembership(t, f.h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct})
	if err := f.env.Admin.Exec("UPDATE access.memberships SET role='CUSTOMER_VIEWER' WHERE tenant_id=? AND id=?", f.h.TenantID, manager.ID).Error; err == nil {
		t.Fatal("database accepted turning an ADMIN holder into CUSTOMER_VIEWER")
	}
	// The guard is deferred: one transaction may change role and entitlement in either order.
	if err := f.env.Admin.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE access.memberships SET role='CUSTOMER_VIEWER' WHERE tenant_id=? AND id=?", f.h.TenantID, manager.ID).Error; err != nil {
			return err
		}
		return tx.Exec("DELETE FROM access.product_entitlements WHERE tenant_id=? AND membership_id=? AND product='ADMIN'", f.h.TenantID, manager.ID).Error
	}); err != nil {
		t.Fatalf("valid final state rejected: %v", err)
	}
	// Only the customer rows are guarded: other roles keep ADMIN.
	other := f.env.AddMembership(t, f.h.TenantID, auth.AccessAdmin, "ACTIVE", []string{auth.AdminProduct})
	if got := f.state(t, other.ID); !equalStrings(got.Products, []string{auth.AdminProduct}) {
		t.Fatalf("delegated administrator lost ADMIN: %+v", got)
	}
}

func TestTFE_LastTenantAdministratorCannotBeDemotedAndLegacyAliasKeepsTheRules(t *testing.T) {
	f := newFixture(t)
	other := f.env.AddMembership(t, f.h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct, auth.DashboardProduct})
	// The caller (f.admin) and `other` are the only active tenant administrators.
	if _, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, MembershipID: other.ID, Role: auth.Manager, ProductEntitlements: []string{auth.DashboardProduct}, ExpectedVersion: 1}); err != nil {
		t.Fatalf("demoting one of two administrators failed: %v", err)
	}
	// Now the caller is alone: even a second administrator-capable actor cannot demote them.
	delegate := accesstest.Context(f.env.AddMembership(t, f.h.TenantID, auth.AccessAdmin, "ACTIVE", []string{auth.AdminProduct}), "inspection-admin")
	if _, err := handle(delegate, f.deps, Command{TenantID: f.h.TenantID, MembershipID: f.admin.ID, Role: auth.Manager, ExpectedVersion: 1}); apperrorCode(err) != apperror.Forbidden {
		t.Fatalf("access admin demoted the tenant admin: %v", err)
	}
	if got := f.state(t, f.admin.ID); got.Role != auth.TenantAdmin {
		t.Fatalf("last tenant administrator was demoted: %+v", got)
	}
	// The same actor demoting themself is blocked as a self-change only when not a tenant admin;
	// a lone tenant admin is blocked by the last-administrator rule.
	if _, err := handle(f.ctx, f.deps, Command{TenantID: f.h.TenantID, MembershipID: f.admin.ID, Role: auth.Manager, ProductEntitlements: []string{auth.DashboardProduct}, ExpectedVersion: 1}); apperrorCode(err) != apperror.InvalidState {
		t.Fatalf("lone tenant admin demoted themself: %v", err)
	}
	if got := f.state(t, f.admin.ID); got.Role != auth.TenantAdmin || got.Version != 1 {
		t.Fatalf("state after a rejected demotion = %+v", got)
	}
}

func apperrorCode(err error) apperror.Code {
	if err == nil {
		return ""
	}
	code, _, _ := apperror.Public(err)
	return code
}
