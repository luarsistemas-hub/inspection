package listidentitymemberships

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/access/accesstest"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
)

const issuer = "https://issuer.test"

func collect(t *testing.T, db accesstest.Env, subject string, pageSize int) ([]Summary, int) {
	t.Helper()
	var all []Summary
	after, pages := "", 0
	for {
		result, err := handle(context.Background(), db.Runtime, Query{Issuer: issuer, Subject: subject, First: pageSize, After: after})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		all = append(all, result.Nodes...)
		if !result.HasNextPage {
			return all, pages
		}
		after = result.EndCursor
		if pages > 50 {
			t.Fatal("pagination did not terminate")
		}
	}
}

func TestTFE_IT_003IdentityWithManyTenantsListsOnlyItsOwnMembershipsWithoutImplicitCrossTenantAccess(t *testing.T) {
	env := accesstest.Open(t)
	subject := "many-tenants-" + identity.NewID().String()
	other := "someone-else-" + identity.NewID().String()
	const tenants = 120
	owned := map[identity.ID]identity.ID{}
	var firstTenant accesstest.Hierarchy
	for index := 0; index < tenants; index++ {
		h := env.SeedTenant(t)
		if index == 0 {
			firstTenant = h
		}
		membership := env.AddMembershipFor(t, h.TenantID, subject, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: h.UnitA})
		owned[membership.ID] = h.TenantID
		env.AddMembershipFor(t, h.TenantID, other, auth.Viewer, "ACTIVE", []string{auth.DashboardProduct})
	}
	all, pages := collect(t, env, subject, 50)
	if len(all) != tenants || pages != 3 {
		t.Fatalf("memberships=%d pages=%d, want %d memberships in 3 pages", len(all), pages, tenants)
	}
	seen := map[identity.ID]struct{}{}
	for _, summary := range all {
		tenantID, mine := owned[summary.MembershipID]
		if !mine || summary.TenantID != tenantID {
			t.Fatalf("summary %+v does not belong to the identity", summary)
		}
		if _, duplicate := seen[summary.MembershipID]; duplicate {
			t.Fatalf("duplicate membership %s across pages", summary.MembershipID)
		}
		seen[summary.MembershipID] = struct{}{}
		if summary.TenantName == "" || summary.Role != auth.Manager || len(summary.ProductEntitlements) != 1 || summary.ProductEntitlements[0] != auth.DashboardProduct {
			t.Fatalf("selector summary lacks the state needed to choose a tenant: %+v", summary)
		}
	}
	if _, err := handle(context.Background(), env.Runtime, Query{Issuer: issuer, Subject: subject, First: 101}); err == nil {
		t.Fatal("a page larger than 100 was accepted")
	} else if code, _, _ := apperror.Public(err); code != apperror.InvalidInput {
		t.Fatalf("oversized page code=%s", code)
	}
	// Choosing one tenant grants nothing in another: its principal is bound to that tenant only.
	var tenantAMembership identity.ID
	for membershipID, tenantID := range owned {
		if tenantID == firstTenant.TenantID {
			tenantAMembership = membershipID
		}
	}
	principal, err := (auth.GORMMembershipStore{DB: env.Runtime}).ResolveMembership(context.Background(), issuer, subject, tenantAMembership)
	if err != nil || principal.TenantID != firstTenant.TenantID {
		t.Fatalf("resolved principal = %+v err=%v", principal, err)
	}
	var anotherTenant identity.ID
	for _, tenantID := range owned {
		if tenantID != firstTenant.TenantID {
			anotherTenant = tenantID
			break
		}
	}
	ctx := accesstestContext(principal)
	if _, err := env.Authorizer().Authorize(ctx, auth.AuthorizationRequest{TenantID: anotherTenant, Product: auth.DashboardProduct, Roles: []string{auth.Manager}}); err == nil {
		t.Fatal("a principal selected for one tenant reached another tenant")
	} else if code, _, _ := apperror.Public(err); code != apperror.NotFound {
		t.Fatalf("cross-tenant code=%s, want NOT_FOUND", code)
	}
	if empty, err := handle(context.Background(), env.Runtime, Query{Issuer: issuer, Subject: "unknown-" + identity.NewID().String(), First: 25}); err != nil || len(empty.Nodes) != 0 {
		t.Fatalf("unknown identity result=%+v err=%v", empty, err)
	}
}

func TestTFE_IT_010ManyScopedMembershipsExposeOnlyCurrentEffectiveState(t *testing.T) {
	env := accesstest.Open(t)
	subject := "scoped-" + identity.NewID().String()
	type expectation struct{ membership, tenant identity.ID }
	active, disabled, inactiveTenant := expectation{}, expectation{}, expectation{}
	for index := 0; index < 30; index++ {
		h := env.SeedTenant(t)
		grants := []accesstest.Grant{{Kind: "BUSINESS_UNIT", ResourceID: h.UnitA}, {Kind: "ASSET", ResourceID: h.AssetB1}, {Kind: "INSPECTION", ResourceID: h.InspectionA2}}
		switch index {
		case 0:
			m := env.AddMembershipFor(t, h.TenantID, subject, auth.Employee, "DISABLED", []string{auth.DashboardProduct}, grants...)
			disabled = expectation{m.ID, h.TenantID}
		case 1:
			m := env.AddMembershipFor(t, h.TenantID, subject, auth.Employee, "ACTIVE", []string{auth.DashboardProduct}, grants...)
			inactiveTenant = expectation{m.ID, h.TenantID}
			if err := env.Admin.Exec("UPDATE tenancy.tenants SET status='INACTIVE' WHERE id=?", h.TenantID).Error; err != nil {
				t.Fatal(err)
			}
		case 2:
			m := env.AddMembershipFor(t, h.TenantID, subject, auth.Employee, "ACTIVE", []string{auth.DashboardProduct}, grants...)
			active = expectation{m.ID, h.TenantID}
		default:
			env.AddMembershipFor(t, h.TenantID, subject, auth.Employee, "ACTIVE", []string{auth.DashboardProduct}, grants...)
		}
	}
	all, _ := collect(t, env, subject, 10)
	if len(all) != 30 {
		t.Fatalf("memberships=%d, want 30", len(all))
	}
	for _, summary := range all {
		switch summary.MembershipID {
		case disabled.membership:
			if summary.MembershipStatus != "DISABLED" {
				t.Fatalf("disabled membership state = %+v", summary)
			}
		case inactiveTenant.membership:
			if summary.TenantStatus == "ACTIVE" {
				t.Fatalf("inactive tenant is shown as active: %+v", summary)
			}
		case active.membership:
			if summary.MembershipStatus != "ACTIVE" || summary.TenantStatus != "ACTIVE" {
				t.Fatalf("active membership state = %+v", summary)
			}
		}
	}
	// Selecting a disabled membership or inactive tenant fails closed on the next protected request.
	for name, target := range map[string]expectation{"disabled membership": disabled, "inactive tenant": inactiveTenant} {
		principal, err := (auth.GORMMembershipStore{DB: env.Runtime}).ResolveMembership(context.Background(), issuer, subject, target.membership)
		if err != nil || !principal.Disabled {
			t.Fatalf("%s: principal=%+v err=%v, want a disabled principal", name, principal, err)
		}
	}
}
