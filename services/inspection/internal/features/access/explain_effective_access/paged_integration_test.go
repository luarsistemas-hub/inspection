package explain_effective_access

import (
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/access/accesstest"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
)

func seedAdmin(t *testing.T, env accesstest.Env, h accesstest.Hierarchy) Dependencies {
	t.Helper()
	return Dependencies{DB: env.Runtime, Authorizer: env.Authorizer(), Scopes: auth.GORMScopeResolver{DB: env.Runtime}}
}

func collect(t *testing.T, deps Dependencies, ctxOwner accesstestContext, membershipID identity.ID, pageSize int) ([]Entry, int) {
	t.Helper()
	var all []Entry
	after, pages := "", 0
	for {
		result, err := handleEffectiveAccess(ctxOwner.ctx, deps, EffectiveAccessQuery{TenantID: ctxOwner.tenantID, MembershipID: membershipID, First: pageSize, After: after})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		all = append(all, result.Descendants.Entries...)
		if !result.Descendants.HasNextPage {
			return all, pages
		}
		after = result.Descendants.EndCursor
		if pages > 1000 {
			t.Fatal("pagination did not terminate")
		}
	}
}

func TestTFE_IT_182EffectiveAccessReturnsDirectGrantAndPagedDescendantsWithInheritedFrom(t *testing.T) {
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	admin := env.AddMembership(t, h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct, auth.DashboardProduct})
	manager := env.AddMembership(t, h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: h.UnitA})
	deps := seedAdmin(t, env, h)
	owner := newOwner(admin, h.TenantID)

	first, err := handleEffectiveAccess(owner.ctx, deps, EffectiveAccessQuery{TenantID: h.TenantID, MembershipID: manager.ID, First: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Grants) != 1 || first.Grants[0].Kind != "BUSINESS_UNIT" || first.Grants[0].ResourceID != h.UnitA || first.Grants[0].Name != "Unidade A" || !first.Grants[0].Grantable() {
		t.Fatalf("explicit grants = %+v", first.Grants)
	}
	if first.Role != auth.Manager || first.Status != "ACTIVE" || first.TenantWide || len(first.ProductEntitlements) != 1 || first.ProductEntitlements[0] != auth.DashboardProduct {
		t.Fatalf("membership summary = %+v", first)
	}
	if len(first.Descendants.Entries) != 2 || !first.Descendants.HasNextPage || first.Descendants.EndCursor == "" {
		t.Fatalf("first page = %+v", first.Descendants)
	}
	all, pages := collect(t, deps, owner, manager.ID, 2)
	if pages != 3 {
		t.Fatalf("pages = %d, want 3 (2+2+1 descendants)", pages)
	}
	want := []struct {
		kind string
		id   identity.ID
	}{{"ASSET", h.AssetA1}, {"ASSET", h.AssetA2}, {"PROJECT", h.ProjectA1}, {"INSPECTION", h.InspectionA1}, {"INSPECTION", h.InspectionA2}}
	if len(all) != len(want) {
		t.Fatalf("descendants = %d (%+v), want %d", len(all), all, len(want))
	}
	got := map[identity.ID]Entry{}
	for _, entry := range all {
		got[entry.ResourceID] = entry
		if entry.InheritedFrom.Kind != "BUSINESS_UNIT" || entry.InheritedFrom.ID != h.UnitA || entry.Historical || !entry.Grantable() {
			t.Fatalf("entry %+v must be inherited from UnitA and current", entry)
		}
	}
	for _, expected := range want {
		if entry, ok := got[expected.id]; !ok || entry.Kind != expected.kind {
			t.Fatalf("missing %s %s in %+v", expected.kind, expected.id, all)
		}
	}
	for _, foreign := range []identity.ID{h.AssetB1, h.InspectionB1, h.UnitB} {
		if _, leaked := got[foreign]; leaked {
			t.Fatalf("descendants leaked sibling business unit resource %s", foreign)
		}
	}
	// Ordering is stable by kind rank then id: assets, then project, then inspections.
	for index := 1; index < len(all); index++ {
		previous, current := all[index-1], all[index]
		if descendantRank(previous.Kind) > descendantRank(current.Kind) || (descendantRank(previous.Kind) == descendantRank(current.Kind) && previous.ResourceID.String() >= current.ResourceID.String()) {
			t.Fatalf("unstable order at %d: %+v then %+v", index, previous, current)
		}
	}
}

func TestTFE_IT_182TenantAdministratorIsTenantWideWithoutExplicitGrants(t *testing.T) {
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	admin := env.AddMembership(t, h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct, auth.DashboardProduct})
	result, err := handleEffectiveAccess(newOwner(admin, h.TenantID).ctx, seedAdmin(t, env, h), EffectiveAccessQuery{TenantID: h.TenantID, MembershipID: admin.ID})
	if err != nil || !result.TenantWide || len(result.Grants) != 0 || len(result.Descendants.Entries) != 0 {
		t.Fatalf("tenant admin result = %+v err=%v", result, err)
	}
}

func TestTFE_IT_183EffectiveAccessUnknownOrCrossTenantMembershipIsNotFound(t *testing.T) {
	env := accesstest.Open(t)
	h, other := env.SeedTenant(t), env.SeedTenant(t)
	admin := env.AddMembership(t, h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct})
	foreign := env.AddMembership(t, other.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: other.UnitA})
	deps := seedAdmin(t, env, h)
	for name, membershipID := range map[string]identity.ID{"unknown": identity.NewID(), "other tenant": foreign.ID} {
		result, err := handleEffectiveAccess(newOwner(admin, h.TenantID).ctx, deps, EffectiveAccessQuery{TenantID: h.TenantID, MembershipID: membershipID})
		code, field, message := apperror.Public(err)
		if code != apperror.NotFound || field != "membershipId" || result.MembershipID != (identity.ID{}) || result.Role != "" || len(result.Grants) != 0 {
			t.Fatalf("%s: code=%s field=%s result=%+v", name, code, field, result)
		}
		if message != "Acesso não encontrado." {
			t.Fatalf("%s: unsafe or untranslated message %q", name, message)
		}
	}
}

func TestTFE_IT_182EffectiveAccessRequiresAdminProductAndAdministrativeRole(t *testing.T) {
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	target := env.AddMembership(t, h.TenantID, auth.Viewer, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: h.UnitA})
	manager := env.AddMembership(t, h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: h.UnitA})
	auditor := env.AddMembership(t, h.TenantID, auth.Auditor, "ACTIVE", []string{auth.AdminProduct})
	adminWithoutProduct := env.AddMembership(t, h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.DashboardProduct})
	deps := seedAdmin(t, env, h)
	for name, caller := range map[string]struct {
		ctxOwner accesstestContext
		allowed  bool
	}{
		"manager":             {newOwner(manager, h.TenantID), false},
		"admin without ADMIN": {newOwner(adminWithoutProduct, h.TenantID), false},
		"auditor reads":       {newOwner(auditor, h.TenantID), true},
	} {
		_, err := handleEffectiveAccess(caller.ctxOwner.ctx, deps, EffectiveAccessQuery{TenantID: h.TenantID, MembershipID: target.ID})
		if code, _, _ := apperror.Public(err); caller.allowed != (err == nil) || (!caller.allowed && code != apperror.Forbidden) {
			t.Fatalf("%s: err=%v code=%s", name, err, code)
		}
	}
	// A dashboard-audience token is rejected even for a tenant administrator that holds ADMIN.
	admin := env.AddMembership(t, h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct, auth.DashboardProduct})
	wrongAudience := accesstestContext{ctx: accesstest.Context(admin, "inspection-dashboard"), tenantID: h.TenantID}
	if _, err := handleEffectiveAccess(wrongAudience.ctx, deps, EffectiveAccessQuery{TenantID: h.TenantID, MembershipID: target.ID}); err == nil {
		t.Fatal("dashboard audience token reached the Admin effective-access query")
	}
}

func TestTFE_IT_184ScopePreviewReturnsCurrentDescendantsInStableOrderAndLabelsArchivedHistorical(t *testing.T) {
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	admin := env.AddMembership(t, h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct})
	deps := seedAdmin(t, env, h)
	owner := newOwner(admin, h.TenantID)
	env.Archive(t, "assets.assets", h.TenantID, h.AssetA2)

	var all []Entry
	after := ""
	for pages := 0; pages < 20; pages++ {
		result, err := handleScopePreview(owner.ctx, deps, ScopePreviewQuery{TenantID: h.TenantID, Kind: "BUSINESS_UNIT", ResourceID: h.UnitA, First: 2, After: after})
		if err != nil {
			t.Fatal(err)
		}
		if result.Target.Kind != "BUSINESS_UNIT" || result.Target.ResourceID != h.UnitA || result.Target.Historical || !result.Target.Grantable() {
			t.Fatalf("target = %+v", result.Target)
		}
		all = append(all, result.Descendants.Entries...)
		if !result.Descendants.HasNextPage {
			break
		}
		after = result.Descendants.EndCursor
	}
	if len(all) != 5 {
		t.Fatalf("descendants = %+v, want 5", all)
	}
	for _, entry := range all {
		archived := entry.ResourceID == h.AssetA2 || entry.ResourceID == h.InspectionA2
		if entry.Historical != archived || entry.Grantable() == archived {
			t.Fatalf("entry %s %s historical=%v grantable=%v, archived=%v", entry.Kind, entry.ResourceID, entry.Historical, entry.Grantable(), archived)
		}
		if entry.InheritedFrom.Kind != "BUSINESS_UNIT" || entry.InheritedFrom.ID != h.UnitA {
			t.Fatalf("entry %+v is not attributed to the previewed grant", entry)
		}
	}
	archivedTarget, err := handleScopePreview(owner.ctx, deps, ScopePreviewQuery{TenantID: h.TenantID, Kind: "ASSET", ResourceID: h.AssetA2})
	if err != nil || !archivedTarget.Target.Historical || archivedTarget.Target.Grantable() {
		t.Fatalf("archived target = %+v err=%v", archivedTarget.Target, err)
	}
}

func TestTFE_IT_185ScopePreviewRejectsUnknownKindAndForeignResourceWithoutDescendants(t *testing.T) {
	env := accesstest.Open(t)
	h, other := env.SeedTenant(t), env.SeedTenant(t)
	admin := env.AddMembership(t, h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct})
	deps := seedAdmin(t, env, h)
	owner := newOwner(admin, h.TenantID)
	cases := map[string]struct {
		query ScopePreviewQuery
		code  apperror.Code
	}{
		"unknown kind":     {ScopePreviewQuery{TenantID: h.TenantID, Kind: "UNKNOWN", ResourceID: h.UnitA}, apperror.InvalidInput},
		"empty resource":   {ScopePreviewQuery{TenantID: h.TenantID, Kind: "BUSINESS_UNIT"}, apperror.InvalidInput},
		"foreign resource": {ScopePreviewQuery{TenantID: h.TenantID, Kind: "BUSINESS_UNIT", ResourceID: other.UnitA}, apperror.NotFound},
		"missing resource": {ScopePreviewQuery{TenantID: h.TenantID, Kind: "ASSET", ResourceID: identity.NewID()}, apperror.NotFound},
		"invalid cursor":   {ScopePreviewQuery{TenantID: h.TenantID, Kind: "BUSINESS_UNIT", ResourceID: h.UnitA, After: "not-a-cursor"}, apperror.PaginationInvalid},
	}
	for name, tc := range cases {
		result, err := handleScopePreview(owner.ctx, deps, tc.query)
		if code, _, _ := apperror.Public(err); code != tc.code || len(result.Descendants.Entries) != 0 || result.Target.Name != "" {
			t.Fatalf("%s: code=%s err=%v result=%+v", name, code, err, result)
		}
	}
}

func TestTFE_IT_029MovedAndArchivedResourcesChangeEffectiveAccessAndArePreviewedBeforeConfirmation(t *testing.T) {
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	admin := env.AddMembership(t, h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct})
	holder := env.AddMembership(t, h.TenantID, auth.Employee, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: h.UnitA})
	deps := seedAdmin(t, env, h)
	owner := newOwner(admin, h.TenantID)

	before, _ := collect(t, deps, owner, holder.ID, 50)
	if !containsEntry(before, h.AssetA1) {
		t.Fatalf("AssetA1 should be reached before the move: %+v", before)
	}
	// Preview the destination before confirming the move: UnitB does not reach AssetA1 yet.
	preview, err := handleScopePreview(owner.ctx, deps, ScopePreviewQuery{TenantID: h.TenantID, Kind: "BUSINESS_UNIT", ResourceID: h.UnitB})
	if err != nil || containsEntry(preview.Descendants.Entries, h.AssetA1) {
		t.Fatalf("preview before move = %+v err=%v", preview.Descendants.Entries, err)
	}
	if err := env.Admin.Exec("UPDATE assets.assets SET business_unit_id=? WHERE tenant_id=? AND id=?", h.UnitB, h.TenantID, h.AssetA1).Error; err != nil {
		t.Fatal(err)
	}
	after, _ := collect(t, deps, owner, holder.ID, 50)
	if containsEntry(after, h.AssetA1) {
		t.Fatalf("holder still reaches moved asset through the old hierarchy: %+v", after)
	}
	moved, err := handleScopePreview(owner.ctx, deps, ScopePreviewQuery{TenantID: h.TenantID, Kind: "BUSINESS_UNIT", ResourceID: h.UnitB})
	if err != nil || !containsEntry(moved.Descendants.Entries, h.AssetA1) {
		t.Fatalf("preview after move = %+v err=%v", moved.Descendants.Entries, err)
	}
}

func TestTFE_IT_030ManyGrantsAndDescendantsPageWithoutDuplicatesOrTenantLeak(t *testing.T) {
	env := accesstest.Open(t)
	h, other := env.SeedTenant(t), env.SeedTenant(t)
	admin := env.AddMembership(t, h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct})
	holder := env.AddMembership(t, h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct},
		accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: h.UnitA}, accesstest.Grant{Kind: "ASSET", ResourceID: h.AssetA1}, accesstest.Grant{Kind: "PROJECT", ResourceID: h.ProjectA1}, accesstest.Grant{Kind: "INSPECTION", ResourceID: h.InspectionA2})
	const extra = 130
	for index := 0; index < extra; index++ {
		env.AddInspection(t, h.TenantID, h.UnitA, h.AssetA1, &h.ProjectA1, identity.NewID())
	}
	env.AddInspection(t, other.TenantID, other.UnitA, other.AssetA1, nil, identity.NewID())
	deps := seedAdmin(t, env, h)
	owner := newOwner(admin, h.TenantID)

	all, pages := collect(t, deps, owner, holder.ID, 40)
	// 2 assets + 1 project + 2 seeded inspections + 130 added.
	if len(all) != 5+extra || pages < 4 {
		t.Fatalf("descendants=%d pages=%d", len(all), pages)
	}
	seen := map[identity.ID]struct{}{}
	for _, entry := range all {
		if _, duplicate := seen[entry.ResourceID]; duplicate {
			t.Fatalf("duplicate entry %s across pages", entry.ResourceID)
		}
		seen[entry.ResourceID] = struct{}{}
		// The most specific explicit ancestor is the one reported.
		if entry.Kind == "INSPECTION" && entry.ResourceID != h.InspectionA2 && entry.InheritedFrom.Kind != "PROJECT" {
			t.Fatalf("inspection %s inherited from %s, want the nearest PROJECT grant", entry.ResourceID, entry.InheritedFrom.Kind)
		}
	}
	if _, leaked := seen[other.AssetA1]; leaked {
		t.Fatal("foreign tenant resource appeared in effective access")
	}
	capped, err := handleEffectiveAccess(owner.ctx, deps, EffectiveAccessQuery{TenantID: h.TenantID, MembershipID: holder.ID, First: 1000})
	if err != nil || len(capped.Descendants.Entries) != MaxPageSize {
		t.Fatalf("page size cap = %d err=%v, want %d", len(capped.Descendants.Entries), err, MaxPageSize)
	}
}

func containsEntry(entries []Entry, id identity.ID) bool {
	for _, entry := range entries {
		if entry.ResourceID == id {
			return true
		}
	}
	return false
}
