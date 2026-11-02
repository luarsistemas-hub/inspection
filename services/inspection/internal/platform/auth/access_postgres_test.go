package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/access/accesstest"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/database/migrations"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

func resolve(t *testing.T, env accesstest.Env, h accesstest.Hierarchy, membershipID identity.ID, kind string, id identity.ID) auth.ScopeDecision {
	t.Helper()
	decision, err := (auth.GORMScopeResolver{DB: env.Runtime}).Resolve(context.Background(), h.TenantID, membershipID, requestctx.Scope{Kind: kind, ID: id})
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

func TestTFE_UT_009BusinessUnitGrantReachesDescendantAssetsProjectsAndInspections(t *testing.T) {
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	member := env.AddMembership(t, h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: h.UnitA})
	for kind, id := range map[string]identity.ID{"BUSINESS_UNIT": h.UnitA, "ASSET": h.AssetA1, "PROJECT": h.ProjectA1, "INSPECTION": h.InspectionA1} {
		decision := resolve(t, env, h, member.ID, kind, id)
		if !decision.Allowed || decision.DirectGrant.Kind != "BUSINESS_UNIT" || decision.DirectGrant.ID != h.UnitA {
			t.Fatalf("%s decision = %+v, want allowed through the BUSINESS_UNIT grant", kind, decision)
		}
	}
	for kind, id := range map[string]identity.ID{"BUSINESS_UNIT": h.UnitB, "ASSET": h.AssetB1, "INSPECTION": h.InspectionB1} {
		if decision := resolve(t, env, h, member.ID, kind, id); decision.Allowed {
			t.Fatalf("sibling %s was reached through UnitA: %+v", kind, decision)
		}
	}
}

func TestTFE_UT_010AssetGrantReachesItsProjectsAndInspectionsOnly(t *testing.T) {
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	member := env.AddMembership(t, h.TenantID, auth.Employee, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "ASSET", ResourceID: h.AssetA1})
	for kind, id := range map[string]identity.ID{"ASSET": h.AssetA1, "PROJECT": h.ProjectA1, "INSPECTION": h.InspectionA1} {
		if decision := resolve(t, env, h, member.ID, kind, id); !decision.Allowed || decision.DirectGrant.Kind != "ASSET" || decision.DirectGrant.ID != h.AssetA1 {
			t.Fatalf("%s decision = %+v, want allowed through the ASSET grant", kind, decision)
		}
	}
	for kind, id := range map[string]identity.ID{"BUSINESS_UNIT": h.UnitA, "ASSET": h.AssetA2, "INSPECTION": h.InspectionA2} {
		if decision := resolve(t, env, h, member.ID, kind, id); decision.Allowed {
			t.Fatalf("%s outside the asset was reached: %+v", kind, decision)
		}
	}
}

func TestTFE_UT_011ProjectGrantReachesOnlyThatProjectsInspections(t *testing.T) {
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	sameAssetOtherProject := identity.NewID()
	env.AddProject(t, h.TenantID, h.UnitA, h.AssetA1, sameAssetOtherProject)
	inOtherProject := identity.NewID()
	env.AddInspection(t, h.TenantID, h.UnitA, h.AssetA1, &sameAssetOtherProject, inOtherProject)
	member := env.AddMembership(t, h.TenantID, auth.Viewer, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "PROJECT", ResourceID: h.ProjectA1})
	for kind, id := range map[string]identity.ID{"PROJECT": h.ProjectA1, "INSPECTION": h.InspectionA1} {
		if decision := resolve(t, env, h, member.ID, kind, id); !decision.Allowed || decision.DirectGrant.Kind != "PROJECT" {
			t.Fatalf("%s decision = %+v, want allowed through the PROJECT grant", kind, decision)
		}
	}
	for kind, id := range map[string]identity.ID{"ASSET": h.AssetA1, "PROJECT": sameAssetOtherProject, "INSPECTION": inOtherProject} {
		if decision := resolve(t, env, h, member.ID, kind, id); decision.Allowed {
			t.Fatalf("%s outside the project was reached: %+v", kind, decision)
		}
	}
	if decision := resolve(t, env, h, member.ID, "INSPECTION", h.InspectionA2); decision.Allowed {
		t.Fatalf("an inspection without a project was reached through a project grant: %+v", decision)
	}
}

func TestTFE_UT_012InspectionGrantReachesOnlyTheExactInspection(t *testing.T) {
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	member := env.AddMembership(t, h.TenantID, auth.Viewer, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "INSPECTION", ResourceID: h.InspectionA1})
	if decision := resolve(t, env, h, member.ID, "INSPECTION", h.InspectionA1); !decision.Allowed || decision.DirectGrant.Kind != "INSPECTION" || decision.DirectGrant.ID != h.InspectionA1 {
		t.Fatalf("exact inspection decision = %+v", decision)
	}
	for kind, id := range map[string]identity.ID{"INSPECTION": h.InspectionA2, "PROJECT": h.ProjectA1, "ASSET": h.AssetA1, "BUSINESS_UNIT": h.UnitA} {
		if decision := resolve(t, env, h, member.ID, kind, id); decision.Allowed {
			t.Fatalf("%s was reached through an INSPECTION grant: %+v", kind, decision)
		}
	}
}

func TestTFE_UT_013HierarchyRowOfAnotherTenantFailsClosed(t *testing.T) {
	env := accesstest.Open(t)
	h, foreign := env.SeedTenant(t), env.SeedTenant(t)
	member := env.AddMembership(t, h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: h.UnitA})
	// A foreign tenant's asset that points at this tenant's business unit must not be reachable.
	stray := identity.NewID()
	env.AddAsset(t, foreign.TenantID, h.UnitA, stray, "Imóvel de outra imobiliária")
	strayInspection := identity.NewID()
	env.AddInspection(t, foreign.TenantID, h.UnitA, stray, nil, strayInspection)
	for kind, id := range map[string]identity.ID{"ASSET": stray, "INSPECTION": strayInspection, "BUSINESS_UNIT": foreign.UnitA, "PROJECT": foreign.ProjectA1} {
		if decision := resolve(t, env, h, member.ID, kind, id); decision.Allowed {
			t.Fatalf("foreign-tenant %s was reached: %+v", kind, decision)
		}
	}
	// And a membership of another tenant never resolves this tenant's resources.
	foreignMember := env.AddMembership(t, foreign.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: h.UnitA})
	if decision, err := (auth.GORMScopeResolver{DB: env.Runtime}).Resolve(context.Background(), h.TenantID, foreignMember.ID, requestctx.Scope{Kind: "ASSET", ID: h.AssetA1}); err != nil || decision.Allowed {
		t.Fatalf("foreign membership decision = %+v err=%v", decision, err)
	}
}

func TestTFE_UT_014ResourceMoveFollowsTheCurrentParentAndRejectsTheOldHierarchy(t *testing.T) {
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	oldOwner := env.AddMembership(t, h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: h.UnitA})
	newOwner := env.AddMembership(t, h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: h.UnitB})
	if !resolve(t, env, h, oldOwner.ID, "ASSET", h.AssetA2).Allowed || resolve(t, env, h, newOwner.ID, "ASSET", h.AssetA2).Allowed {
		t.Fatal("baseline hierarchy is wrong")
	}
	if err := env.Admin.Exec("UPDATE assets.assets SET business_unit_id=? WHERE tenant_id=? AND id=?", h.UnitB, h.TenantID, h.AssetA2).Error; err != nil {
		t.Fatal(err)
	}
	if err := env.Admin.Exec("UPDATE inspections.inspections SET business_unit_id=? WHERE tenant_id=? AND id=?", h.UnitB, h.TenantID, h.InspectionA2).Error; err != nil {
		t.Fatal(err)
	}
	for _, kind := range []struct {
		kind string
		id   identity.ID
	}{{"ASSET", h.AssetA2}, {"INSPECTION", h.InspectionA2}} {
		if resolve(t, env, h, oldOwner.ID, kind.kind, kind.id).Allowed {
			t.Fatalf("old hierarchy still grants moved %s", kind.kind)
		}
		if !resolve(t, env, h, newOwner.ID, kind.kind, kind.id).Allowed {
			t.Fatalf("new hierarchy does not grant moved %s", kind.kind)
		}
	}
}

func TestTFE_UT_015ArchivedTargetIsDeniedForNewWorkButStillReadable(t *testing.T) {
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	member := env.AddMembership(t, h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: h.UnitA})
	env.Archive(t, "assets.assets", h.TenantID, h.AssetA1)
	authorizer := env.Authorizer()
	ctx := accesstest.Context(member, "inspection-dashboard")
	for name, kind := range map[string]struct {
		scope requestctx.Scope
	}{"asset": {requestctx.Scope{Kind: "ASSET", ID: h.AssetA1}}, "project under archived asset": {requestctx.Scope{Kind: "PROJECT", ID: h.ProjectA1}}, "inspection under archived asset": {requestctx.Scope{Kind: "INSPECTION", ID: h.InspectionA1}}} {
		scope := kind.scope
		if _, err := authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: h.TenantID, Product: auth.DashboardProduct, Roles: []string{auth.Manager}, Resource: &scope}); err != nil {
			t.Fatalf("%s: historical read denied: %v", name, err)
		}
		_, err := authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: h.TenantID, Product: auth.DashboardProduct, Roles: []string{auth.Manager}, Mutate: true, NewWork: true, Resource: &scope})
		if code, _, _ := apperror.Public(err); code != apperror.NotFound {
			t.Fatalf("%s: new work on archived hierarchy code=%s err=%v, want NOT_FOUND", name, code, err)
		}
	}
	live := requestctx.Scope{Kind: "ASSET", ID: h.AssetA2}
	if _, err := authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: h.TenantID, Product: auth.DashboardProduct, Roles: []string{auth.Manager}, Mutate: true, NewWork: true, Resource: &live}); err != nil {
		t.Fatalf("new work on a live sibling denied: %v", err)
	}
}

func TestTFE_UT_016CancelledContextPropagatesFromTheScopeRepository(t *testing.T) {
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	member := env.AddMembership(t, h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: h.UnitA})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (auth.GORMScopeResolver{DB: env.Runtime}).Resolve(ctx, h.TenantID, member.ID, requestctx.Scope{Kind: "ASSET", ID: h.AssetA1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve error = %v, want context.Canceled", err)
	}
	_, _, err = (auth.GORMScopeResolver{DB: env.Runtime}).ResolveNewWork(ctx, h.TenantID, member.ID, requestctx.Scope{Kind: "ASSET", ID: h.AssetA1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolveNewWork error = %v, want context.Canceled", err)
	}
}

type countingScopes struct {
	inner auth.GORMScopeResolver
	calls int
}

func (c *countingScopes) Resolve(ctx context.Context, tenantID, membershipID identity.ID, target requestctx.Scope) (auth.ScopeDecision, error) {
	c.calls++
	return c.inner.Resolve(ctx, tenantID, membershipID, target)
}

func TestTFE_UT_006RevokedMembershipAndEntitlementAreReloadedBeforeResourceIO(t *testing.T) {
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	member := env.AddMembership(t, h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: h.UnitA})
	scopes := &countingScopes{inner: auth.GORMScopeResolver{DB: env.Runtime}}
	authorizer := auth.Authorizer{Store: auth.GORMMembershipStore{DB: env.Runtime}, Scopes: scopes}
	ctx := accesstest.Context(member, "inspection-dashboard")
	request := auth.AuthorizationRequest{TenantID: h.TenantID, Product: auth.DashboardProduct, Roles: []string{auth.Manager}, Resource: &requestctx.Scope{Kind: "ASSET", ID: h.AssetA1}}
	if _, err := authorizer.Authorize(ctx, request); err != nil || scopes.calls != 1 {
		t.Fatalf("baseline err=%v resource calls=%d", err, scopes.calls)
	}
	if err := env.Admin.Exec("DELETE FROM access.product_entitlements WHERE tenant_id=? AND membership_id=?", h.TenantID, member.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := authorizer.Authorize(ctx, request); err == nil || scopes.calls != 1 {
		t.Fatalf("revoked entitlement err=%v resource calls=%d, want a denial before resource I/O", err, scopes.calls)
	} else if code, _, _ := apperror.Public(err); code != apperror.Forbidden {
		t.Fatalf("revoked entitlement code=%s", code)
	}
	if err := env.Admin.Exec("INSERT INTO access.product_entitlements (id, tenant_id, membership_id, product) VALUES (?,?,?,'DASHBOARD')", identity.NewID(), h.TenantID, member.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := authorizer.Authorize(ctx, request); err != nil || scopes.calls != 2 {
		t.Fatalf("restored entitlement err=%v resource calls=%d", err, scopes.calls)
	}
	if err := env.Admin.Exec("UPDATE access.memberships SET status='DISABLED' WHERE tenant_id=? AND id=?", h.TenantID, member.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := authorizer.Authorize(ctx, request); err == nil || scopes.calls != 2 {
		t.Fatalf("disabled membership err=%v resource calls=%d, want a denial before resource I/O", err, scopes.calls)
	}
}

func TestTFE_IT_009InactiveTenantFailsClosedForEveryMembershipOnTheNextRequest(t *testing.T) {
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	admin := env.AddMembership(t, h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct, auth.DashboardProduct})
	manager := env.AddMembership(t, h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: h.UnitA})
	authorizer := env.Authorizer()
	adminRequest := auth.AuthorizationRequest{TenantID: h.TenantID, Product: auth.AdminProduct, Roles: []string{auth.TenantAdmin}}
	managerRequest := auth.AuthorizationRequest{TenantID: h.TenantID, Product: auth.DashboardProduct, Roles: []string{auth.Manager}, Resource: &requestctx.Scope{Kind: "ASSET", ID: h.AssetA1}}
	if _, err := authorizer.Authorize(accesstest.Context(admin, "inspection-admin"), adminRequest); err != nil {
		t.Fatalf("baseline admin: %v", err)
	}
	if _, err := authorizer.Authorize(accesstest.Context(manager, "inspection-dashboard"), managerRequest); err != nil {
		t.Fatalf("baseline manager: %v", err)
	}
	if err := env.Admin.Exec("UPDATE tenancy.tenants SET status='INACTIVE' WHERE id=?", h.TenantID).Error; err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"admin": func() error {
			_, err := authorizer.Authorize(accesstest.Context(admin, "inspection-admin"), adminRequest)
			return err
		},
		"manager": func() error {
			_, err := authorizer.Authorize(accesstest.Context(manager, "inspection-dashboard"), managerRequest)
			return err
		},
	} {
		if err := call(); err == nil {
			t.Fatalf("%s kept access after the tenant became inactive", name)
		} else if code, _, _ := apperror.Public(err); code != apperror.Forbidden {
			t.Fatalf("%s code=%s, want FORBIDDEN", name, code)
		}
	}
}

func TestTFE_UT_007MembershipStoreFailureIsWrappedWithoutDatabaseDetails(t *testing.T) {
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	member := env.AddMembership(t, h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct})
	// A closed pool makes the store fail the way a database outage would.
	broken, err := gorm.Open(env.Runtime.Dialector, &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := broken.DB()
	_ = sqlDB.Close()
	_, err = (auth.Authorizer{Store: auth.GORMMembershipStore{DB: broken}}).Authorize(accesstest.Context(member, "inspection-dashboard"), auth.AuthorizationRequest{TenantID: h.TenantID, Product: auth.DashboardProduct, Roles: []string{auth.Manager}})
	code, _, message := apperror.Public(err)
	if code != apperror.Internal || message != "Não foi possível concluir a operação." {
		t.Fatalf("code=%s message=%q, want a generic INTERNAL error", code, message)
	}
}

func TestTFE_IT_229AccessTablesEnforceTenantRowLevelSecurity(t *testing.T) {
	env := accesstest.Open(t)
	h, other := env.SeedTenant(t), env.SeedTenant(t)
	mine := env.AddMembership(t, h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: h.UnitA})
	theirs := env.AddMembership(t, other.TenantID, auth.CustomerViewer, "INVITED", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "ASSET", ResourceID: other.AssetA1})
	for _, owner := range []struct {
		tenant     identity.ID
		membership database.Membership
	}{{other.TenantID, theirs}, {h.TenantID, mine}} {
		if err := env.Admin.Exec("INSERT INTO access.user_invitations (id, tenant_id, membership_id, issuer, subject, email, name, status, expires_at, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,now()+interval '1 day',now(),now())", identity.NewID(), owner.tenant, owner.membership.ID, "https://issuer.test", owner.membership.Subject, owner.membership.Email, "Cliente", "SENT").Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"access.memberships", "access.product_entitlements", "access.resource_scopes", "access.user_invitations", "access.user_invitation_otp_challenges", "reports.publication_policies", "reports.report_publications", "notifications.recipient_channels", "notifications.recipient_notifications"} {
		var flags struct{ Enabled, Forced bool }
		if err := env.Admin.Raw("SELECT relrowsecurity AS enabled, relforcerowsecurity AS forced FROM pg_class WHERE oid = ?::regclass", table).Scan(&flags).Error; err != nil || !flags.Enabled || !flags.Forced {
			t.Fatalf("%s row-level security flags=%+v err=%v", table, flags, err)
		}
	}
	for _, table := range []string{"access.memberships", "access.product_entitlements", "access.resource_scopes", "access.user_invitations"} {
		var visible, foreign int64
		if err := (tenanttx.Runner{DB: env.Runtime}).Within(context.Background(), h.TenantID, func(tx *gorm.DB) error {
			if err := tx.Raw("SELECT count(*) FROM "+table+" WHERE tenant_id=?", h.TenantID).Scan(&visible).Error; err != nil {
				return err
			}
			return tx.Raw("SELECT count(*) FROM "+table+" WHERE tenant_id=?", other.TenantID).Scan(&foreign).Error
		}); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if visible == 0 {
			t.Fatalf("%s hides the tenant's own rows", table)
		}
		if foreign != 0 {
			t.Fatalf("%s leaked %d foreign tenant rows", table, foreign)
		}
	}
	// A write into another tenant's partition is rejected by the policy.
	err := (tenanttx.Runner{DB: env.Runtime}).Within(context.Background(), h.TenantID, func(tx *gorm.DB) error {
		return tx.Exec("INSERT INTO access.product_entitlements (id, tenant_id, membership_id, product) VALUES (?,?,?,'DASHBOARD')", identity.NewID(), other.TenantID, mine.ID).Error
	})
	if err == nil {
		t.Fatal("runtime role wrote an entitlement into another tenant")
	}
}

func TestTFE_IT_008MigrationBackfillGivesTenantAdminBothProductsAndOtherActiveRolesDashboard(t *testing.T) {
	env := accesstest.Open(t)
	var step migrations.Step
	for _, candidate := range migrations.Foundation() {
		if candidate.Version == 20 {
			step = candidate
		}
	}
	if step.Version != 20 {
		t.Fatal("migration 20 not found")
	}
	rollback := errors.New("rollback")
	err := env.Admin.Transaction(func(tx *gorm.DB) error {
		scoped := accesstest.Env{Admin: tx}
		h := scoped.SeedTenant(t)
		// Pre-entitlement state: memberships exist without any entitlement row.
		roles := map[string]string{}
		for _, role := range []string{auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer, auth.CustomerViewer} {
			roles[role] = scoped.AddMembership(t, h.TenantID, role, "ACTIVE", nil).ID.String()
		}
		disabled := scoped.AddMembership(t, h.TenantID, auth.Manager, "DISABLED", nil)
		// Pending deferred guard events would block ALTER TABLE inside this transaction.
		if err := tx.Exec("SET CONSTRAINTS ALL IMMEDIATE").Error; err != nil {
			t.Fatal(err)
		}
		// Replay only the backfill statements: the table and constraint DDL of the
		// step cannot be re-applied once other rows (delegated roles) exist.
		replayed := 0
		for _, statement := range strings.Split(step.SQL, ";\n") {
			if strings.HasPrefix(strings.TrimSpace(statement), "INSERT INTO access.product_entitlements") {
				if err := tx.Exec(statement).Error; err != nil {
					t.Fatalf("migration 20 backfill replay: %v", err)
				}
				replayed++
			}
		}
		if replayed != 2 {
			t.Fatalf("replayed %d backfill statements, want 2", replayed)
		}
		products := func(membershipID string) []string {
			var values []string
			if err := tx.Raw("SELECT product FROM access.product_entitlements WHERE tenant_id=? AND membership_id=? ORDER BY product", h.TenantID, membershipID).Scan(&values).Error; err != nil {
				t.Fatal(err)
			}
			return values
		}
		if got := products(roles[auth.TenantAdmin]); len(got) != 2 || got[0] != auth.AdminProduct || got[1] != auth.DashboardProduct {
			t.Fatalf("TENANT_ADMIN backfill = %v, want ADMIN and DASHBOARD", got)
		}
		for _, role := range []string{auth.Manager, auth.Employee, auth.Viewer, auth.CustomerViewer} {
			if got := products(roles[role]); len(got) != 1 || got[0] != auth.DashboardProduct {
				t.Fatalf("%s backfill = %v, want only DASHBOARD", role, got)
			}
		}
		if got := products(disabled.ID.String()); len(got) != 0 {
			t.Fatalf("a disabled membership received entitlements: %v", got)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("transaction error = %v", err)
	}
}

func TestTFE_IT_198Migration111NormalizesExistingCustomerAdminEntitlementsAndIsReplayable(t *testing.T) {
	env := accesstest.Open(t)
	var step migrations.Step
	for _, candidate := range migrations.Foundation() {
		if candidate.Version == 111 {
			step = candidate
		}
	}
	if step.Version != 111 {
		t.Fatal("migration 111 not found")
	}
	rollback := errors.New("rollback")
	err := env.Admin.Transaction(func(tx *gorm.DB) error {
		scoped := accesstest.Env{Admin: tx}
		h := scoped.SeedTenant(t)
		// Recreate the pre-111 state: a customer that already holds ADMIN next to a valid delegated administrator.
		if err := tx.Exec("DROP TRIGGER IF EXISTS customer_admin_entitlement_guard ON access.product_entitlements").Error; err != nil {
			t.Fatal(err)
		}
		if err := tx.Exec("DROP TRIGGER IF EXISTS customer_role_admin_entitlement_guard ON access.memberships").Error; err != nil {
			t.Fatal(err)
		}
		customer := scoped.AddMembership(t, h.TenantID, auth.CustomerViewer, "ACTIVE", []string{auth.AdminProduct, auth.DashboardProduct})
		delegate := scoped.AddMembership(t, h.TenantID, auth.AccessAdmin, "ACTIVE", []string{auth.AdminProduct})
		for replay := 0; replay < 2; replay++ {
			if err := tx.Exec(step.SQL).Error; err != nil {
				t.Fatalf("migration 111 replay %d: %v", replay, err)
			}
			// Pending deferred guard events fire now, on the normalized state.
			if err := tx.Exec("SET CONSTRAINTS ALL IMMEDIATE").Error; err != nil {
				t.Fatalf("replay %d left a violating state: %v", replay, err)
			}
			if err := tx.Exec("SET CONSTRAINTS ALL DEFERRED").Error; err != nil {
				t.Fatal(err)
			}
		}
		count := func(membershipID, product string) int64 {
			var rows int64
			if err := tx.Raw("SELECT count(*) FROM access.product_entitlements WHERE tenant_id=? AND membership_id=? AND product=?", h.TenantID, membershipID, product).Scan(&rows).Error; err != nil {
				t.Fatal(err)
			}
			return rows
		}
		if count(customer.ID.String(), auth.AdminProduct) != 0 || count(customer.ID.String(), auth.DashboardProduct) != 1 {
			t.Fatal("customer entitlements were not normalized to DASHBOARD only")
		}
		if count(delegate.ID.String(), auth.AdminProduct) != 1 {
			t.Fatal("migration 111 removed ADMIN from a delegated administrator")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("transaction error = %v", err)
	}
}
