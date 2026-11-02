package invite_customer_user

import (
	"context"
	"errors"
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/access/accesstest"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

type fixture struct {
	env      accesstest.Env
	h        accesstest.Hierarchy
	deps     Dependencies
	provider *accesstest.Provider
	sender   *accesstest.Sender
	limits   *accesstest.Limits
	ctx      context.Context
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	admin := env.AddMembership(t, h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct, auth.DashboardProduct})
	provider, sender, limits := &accesstest.Provider{}, &accesstest.Sender{}, &accesstest.Limits{}
	deps := Dependencies{DB: env.Runtime, Authorizer: env.Authorizer(), Issuer: "https://issuer.test", AdminOrigin: "https://admin.test", Provider: provider, Sender: sender, Limits: limits}
	return fixture{env: env, h: h, deps: deps, provider: provider, sender: sender, limits: limits, ctx: accesstest.Context(admin, "inspection-admin")}
}

func (f fixture) command(email string, scopes ...Scope) Command {
	return Command{TenantID: f.h.TenantID, Name: "  Cliente Teste  ", Email: email, Scopes: scopes, ClientID: "mutation-" + email}
}

func (f fixture) assertNothingCreated(t *testing.T) {
	t.Helper()
	if got := f.env.Count(t, "access.memberships", f.h.TenantID, "role='CUSTOMER_VIEWER'"); got != 0 {
		t.Fatalf("customer memberships = %d after a rejected invitation", got)
	}
	if got := f.env.Count(t, "access.user_invitations", f.h.TenantID, ""); got != 0 {
		t.Fatalf("invitations = %d after a rejected invitation", got)
	}
	if got := f.env.Count(t, "access.product_entitlements", f.h.TenantID, "membership_id NOT IN (SELECT id FROM access.memberships WHERE tenant_id=? AND role<>'CUSTOMER_VIEWER')", f.h.TenantID); got != 0 {
		t.Fatalf("customer entitlements = %d after a rejected invitation", got)
	}
	if got := f.env.Count(t, "access.resource_scopes", f.h.TenantID, "membership_id NOT IN (SELECT id FROM access.memberships WHERE tenant_id=? AND role<>'CUSTOMER_VIEWER')", f.h.TenantID); got != 0 {
		t.Fatalf("customer scopes = %d after a rejected invitation", got)
	}
	if got := f.env.Count(t, "audit.events", f.h.TenantID, "action='access.customer_invited'"); got != 0 {
		t.Fatalf("audit events = %d for a rejected invitation", got)
	}
	if len(f.sender.Sent) != 0 {
		t.Fatalf("activation e-mail sent for a rejected invitation: %v", f.sender.Sent)
	}
}

func TestTFE_UT_017CustomerInvitationNormalizesEmailAndCreatesDashboardOnlyMembership(t *testing.T) {
	f := newFixture(t)
	result, err := handle(f.ctx, f.deps, f.command("  Cliente@Example.TEST ", Scope{Kind: "ASSET", ResourceID: f.h.AssetA1}))
	if err != nil {
		t.Fatal(err)
	}
	if result.InvitationStatus != "SENT" || result.Membership.Role != auth.CustomerViewer || result.Membership.Email != "cliente@example.test" || result.Membership.Name != "Cliente Teste" || result.Membership.Status != "INVITED" {
		t.Fatalf("result = %+v", result)
	}
	var products []string
	if err := f.env.Admin.Raw("SELECT product FROM access.product_entitlements WHERE tenant_id=? AND membership_id=?", f.h.TenantID, result.Membership.ID).Scan(&products).Error; err != nil {
		t.Fatal(err)
	}
	if len(products) != 1 || products[0] != auth.DashboardProduct {
		t.Fatalf("customer entitlements = %v, want only DASHBOARD", products)
	}
	if got := f.env.Count(t, "access.resource_scopes", f.h.TenantID, "membership_id=? AND kind='ASSET' AND resource_id=?", result.Membership.ID, f.h.AssetA1); got != 1 {
		t.Fatalf("explicit scope rows = %d, want 1", got)
	}
	if got := f.env.Count(t, "audit.events", f.h.TenantID, "action='access.customer_invited' AND target_id=?", result.Membership.ID.String()); got != 1 {
		t.Fatalf("audit rows = %d, want 1", got)
	}
	if len(f.sender.Sent) != 1 || f.sender.Sent[0] != "cliente@example.test" {
		t.Fatalf("activation e-mails = %v", f.sender.Sent)
	}
}

func TestTFE_UT_018IT_022CustomerInvitationWithoutScopesIsRejectedAndCreatesNothing(t *testing.T) {
	f := newFixture(t)
	_, err := handle(f.ctx, f.deps, f.command("sem-escopo@example.test"))
	if code, field, _ := apperror.Public(err); code != apperror.InvalidInput || field != "scopes" {
		t.Fatalf("code=%s field=%s err=%v", code, field, err)
	}
	if f.provider.Calls != 0 {
		t.Fatalf("identity provider called %d times for an invalid invitation", f.provider.Calls)
	}
	f.assertNothingCreated(t)
}

func TestTFE_UT_019CustomerInvitationRejectsAdminEntitlement(t *testing.T) {
	f := newFixture(t)
	command := f.command("admin@example.test", Scope{Kind: "BUSINESS_UNIT", ResourceID: f.h.UnitA})
	command.ProductEntitlements = []string{auth.AdminProduct}
	_, err := handle(f.ctx, f.deps, command)
	if code, field, _ := apperror.Public(err); code != apperror.InvalidInput || field != "productEntitlements" {
		t.Fatalf("code=%s field=%s err=%v", code, field, err)
	}
	f.assertNothingCreated(t)
}

func TestTFE_IT_198InvalidEmailCrossTenantScopeAndAdminEntitlementRejectAtomically(t *testing.T) {
	f := newFixture(t)
	other := f.env.SeedTenant(t)
	cases := map[string]Command{
		"invalid email":    f.command("not-an-email", Scope{Kind: "ASSET", ResourceID: f.h.AssetA1}),
		"unknown kind":     f.command("kind@example.test", Scope{Kind: "TENANT", ResourceID: f.h.AssetA1}),
		"cross tenant":     f.command("tenant@example.test", Scope{Kind: "BUSINESS_UNIT", ResourceID: other.UnitA}),
		"one bad of many":  f.command("many@example.test", Scope{Kind: "ASSET", ResourceID: f.h.AssetA1}, Scope{Kind: "ASSET", ResourceID: identity.NewID()}),
		"duplicate scopes": f.command("dup@example.test", Scope{Kind: "ASSET", ResourceID: f.h.AssetA1}, Scope{Kind: "ASSET", ResourceID: f.h.AssetA1}),
	}
	admin := f.command("admin@example.test", Scope{Kind: "ASSET", ResourceID: f.h.AssetA1})
	admin.ProductEntitlements = []string{auth.AdminProduct, auth.DashboardProduct}
	cases["admin entitlement"] = admin
	for name, command := range cases {
		if _, err := handle(f.ctx, f.deps, command); err == nil {
			t.Fatalf("%s: invitation accepted", name)
		} else if code, _, _ := apperror.Public(err); code != apperror.InvalidInput {
			t.Fatalf("%s: code=%s err=%v", name, code, err)
		}
		f.assertNothingCreated(t)
	}
}

func TestTFE_UT_020RepeatedCustomerInvitationReturnsExistingOutcomeWithoutSecondEmail(t *testing.T) {
	f := newFixture(t)
	command := f.command("repete@example.test", Scope{Kind: "ASSET", ResourceID: f.h.AssetA1})
	first, err := handle(f.ctx, f.deps, command)
	if err != nil || first.InvitationStatus != "SENT" {
		t.Fatalf("first = %+v err=%v", first, err)
	}
	second, err := handle(f.ctx, f.deps, command)
	if err != nil || second.Membership.ID != first.Membership.ID || second.InvitationStatus != "SENT" {
		t.Fatalf("second = %+v err=%v", second, err)
	}
	if len(f.sender.Sent) != 1 {
		t.Fatalf("e-mails sent = %d, want 1", len(f.sender.Sent))
	}
	if got := f.env.Count(t, "access.memberships", f.h.TenantID, "email='repete@example.test'"); got != 1 {
		t.Fatalf("memberships = %d, want 1", got)
	}
}

func TestTFE_IT_197IT_026CustomerInvitationStaysPendingAndRetryableAfterDeliveryFailure(t *testing.T) {
	f := newFixture(t)
	f.sender.Err = errors.New("smtp unavailable")
	command := f.command("falha@example.test", Scope{Kind: "PROJECT", ResourceID: f.h.ProjectA1})
	failed, err := handle(f.ctx, f.deps, command)
	if err != nil || failed.InvitationStatus != "FAILED" || failed.Membership.Status != "INVITED" {
		t.Fatalf("failed delivery = %+v err=%v", failed, err)
	}
	var stored struct{ Status, InvitationStatus string }
	if err := f.env.Admin.Raw("SELECT status, invitation_status FROM access.memberships WHERE tenant_id=? AND id=?", f.h.TenantID, failed.Membership.ID).Scan(&stored).Error; err != nil || stored.Status != "INVITED" || stored.InvitationStatus != "FAILED" {
		t.Fatalf("stored membership = %+v err=%v", stored, err)
	}
	f.sender.Err = nil
	retried, err := handle(f.ctx, f.deps, command)
	if err != nil || retried.InvitationStatus != "SENT" || retried.Membership.ID != failed.Membership.ID {
		t.Fatalf("retry = %+v err=%v", retried, err)
	}
	if got := f.env.Count(t, "access.memberships", f.h.TenantID, "email='falha@example.test'"); got != 1 {
		t.Fatalf("memberships after retry = %d, want 1", got)
	}
	if got := f.env.Count(t, "access.user_invitations", f.h.TenantID, "email='falha@example.test'"); got != 1 {
		t.Fatalf("invitations after retry = %d, want 1", got)
	}
}

func TestTFE_ProviderFailureCreatesNothingAndReplayProvisionsOneSubjectAndMembership(t *testing.T) {
	f := newFixture(t)
	f.provider.Err = accesstest.ErrProvider
	command := f.command("provedor@example.test", Scope{Kind: "ASSET", ResourceID: f.h.AssetA1})
	_, err := handle(f.ctx, f.deps, command)
	if code, _, _ := apperror.Public(err); code != apperror.DependencyUnavailable {
		t.Fatalf("code=%s err=%v, want DEPENDENCY_UNAVAILABLE", code, err)
	}
	f.assertNothingCreated(t)
	f.provider.Err = nil
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := handle(f.ctx, f.deps, command); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.env.Count(t, "access.memberships", f.h.TenantID, "email='provedor@example.test'"); got != 1 {
		t.Fatalf("memberships after replay = %d, want 1", got)
	}
}

func TestTFE_IT_200ExistingActiveCustomerReturnsExistingOutcomeWithoutNewMembershipOrEmail(t *testing.T) {
	f := newFixture(t)
	command := f.command("ativo@example.test", Scope{Kind: "ASSET", ResourceID: f.h.AssetA1})
	invited, err := handle(f.ctx, f.deps, command)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.env.Admin.Exec("UPDATE access.memberships SET status='ACTIVE', invitation_status='ACCEPTED' WHERE tenant_id=? AND id=?", f.h.TenantID, invited.Membership.ID).Error; err != nil {
		t.Fatal(err)
	}
	sentBefore := len(f.sender.Sent)
	again, err := handle(f.ctx, f.deps, command)
	if err != nil || again.InvitationStatus != "ACCEPTED" || again.Membership.ID != invited.Membership.ID {
		t.Fatalf("again = %+v err=%v", again, err)
	}
	if len(f.sender.Sent) != sentBefore {
		t.Fatalf("an e-mail was sent for an already active customer")
	}
	if got := f.env.Count(t, "access.memberships", f.h.TenantID, "email='ativo@example.test'"); got != 1 {
		t.Fatalf("memberships = %d, want 1", got)
	}
}

func TestTFE_IT_028PendingCustomerKeepsScopeButReadsNoDataBeforeActivation(t *testing.T) {
	f := newFixture(t)
	invited, err := handle(f.ctx, f.deps, f.command("pendente@example.test", Scope{Kind: "ASSET", ResourceID: f.h.AssetA1}))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.env.Count(t, "access.resource_scopes", f.h.TenantID, "membership_id=?", invited.Membership.ID); got != 1 {
		t.Fatalf("pending scope rows = %d, want the scope preserved", got)
	}
	_, err = f.deps.Authorizer.Authorize(accesstest.Context(invited.Membership, "inspection-dashboard"), auth.AuthorizationRequest{
		TenantID: f.h.TenantID, Product: auth.DashboardProduct, Roles: []string{auth.CustomerViewer},
		Resource: &requestctx.Scope{Kind: "ASSET", ID: f.h.AssetA1},
	})
	if code, _, _ := apperror.Public(err); code != apperror.Forbidden {
		t.Fatalf("pending customer authorization code=%s err=%v, want FORBIDDEN before activation", code, err)
	}
	if err := f.env.Admin.Exec("UPDATE access.memberships SET status='ACTIVE' WHERE tenant_id=? AND id=?", f.h.TenantID, invited.Membership.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.deps.Authorizer.Authorize(accesstest.Context(invited.Membership, "inspection-dashboard"), auth.AuthorizationRequest{
		TenantID: f.h.TenantID, Product: auth.DashboardProduct, Roles: []string{auth.CustomerViewer},
		Resource: &requestctx.Scope{Kind: "ASSET", ID: f.h.AssetA1},
	}); err != nil {
		t.Fatalf("activated customer denied its granted asset: %v", err)
	}
}

func TestTFE_IT_024CustomerInvitationRequiresAdminProductAndAdministrativeRole(t *testing.T) {
	f := newFixture(t)
	manager := f.env.AddMembership(t, f.h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: f.h.UnitA})
	command := f.command("negado@example.test", Scope{Kind: "ASSET", ResourceID: f.h.AssetA1})
	if _, err := handle(accesstest.Context(manager, "inspection-dashboard"), f.deps, command); err == nil {
		t.Fatal("manager invited a customer")
	} else if code, _, _ := apperror.Public(err); code != apperror.Forbidden {
		t.Fatalf("code=%s err=%v", code, err)
	}
	f.assertNothingCreated(t)
}

func TestSetupRejectsMissingDependencies(t *testing.T) {
	if err := Setup(Dependencies{}); err == nil {
		t.Fatal("expected a missing dependency error")
	}
}

func TestTFE_IT_027SameClientMutationIdAndPayloadIsADurableReplay(t *testing.T) {
	f := newFixture(t)
	command := f.command("replay@example.test", Scope{Kind: "ASSET", ResourceID: f.h.AssetA1})
	first, err := handle(f.ctx, f.deps, command)
	if err != nil || first.InvitationStatus != "SENT" {
		t.Fatalf("first = %+v err=%v", first, err)
	}
	providerCalls, limiterCalls := f.provider.Calls, len(f.limits.Keys)
	second, err := handle(f.ctx, f.deps, command)
	if err != nil || second.Membership.ID != first.Membership.ID || second.InvitationStatus != "SENT" {
		t.Fatalf("replay = %+v err=%v", second, err)
	}
	if f.provider.Calls != providerCalls || len(f.limits.Keys) != limiterCalls || len(f.sender.Sent) != 1 {
		t.Fatalf("a replay called the provider (%d), the limiter (%d) or sent mail (%d)", f.provider.Calls-providerCalls, len(f.limits.Keys)-limiterCalls, len(f.sender.Sent))
	}
	if got := f.env.Count(t, "access.invitation_receipts", f.h.TenantID, "client_mutation_id=? AND invitation_status='SENT'", command.ClientID); got != 1 {
		t.Fatalf("durable receipts = %d, want 1", got)
	}
}

func TestTFE_IT_027SameClientMutationIdWithDifferentPayloadIsConflict(t *testing.T) {
	f := newFixture(t)
	command := f.command("conflito@example.test", Scope{Kind: "ASSET", ResourceID: f.h.AssetA1})
	if _, err := handle(f.ctx, f.deps, command); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Command){
		"another scope": func(c *Command) { c.Scopes = []Scope{{Kind: "ASSET", ResourceID: f.h.AssetA2}} },
		"another name":  func(c *Command) { c.Name = "Outro Nome" },
	} {
		changed := command
		mutate(&changed)
		_, err := handle(f.ctx, f.deps, changed)
		if code, field, _ := apperror.Public(err); code != apperror.Conflict || field != "clientMutationId" {
			t.Fatalf("%s: code=%s field=%s err=%v", name, code, field, err)
		}
	}
	if got := f.env.Count(t, "access.resource_scopes", f.h.TenantID, "resource_id=?", f.h.AssetA2); got != 0 {
		t.Fatalf("a conflicting replay changed the scopes: %d rows", got)
	}
	// The key is also bound to the tenant: another operation cannot reuse it.
	other := command
	other.Email = "outra-pessoa@example.test"
	if _, err := handle(f.ctx, f.deps, other); err == nil {
		t.Fatal("the key was accepted for another recipient")
	}
}

func TestTFE_InvitationsAreRateLimitedPerTenantAndPerRecipientBeforeAnySideEffect(t *testing.T) {
	f := newFixture(t)
	command := f.command("limite@example.test", Scope{Kind: "ASSET", ResourceID: f.h.AssetA1})
	f.limits.Deny = true
	_, err := handle(f.ctx, f.deps, command)
	if code, _, _ := apperror.Public(err); code != apperror.RateLimited {
		t.Fatalf("code=%s err=%v, want RATE_LIMITED", code, err)
	}
	if f.provider.Calls != 0 {
		t.Fatal("the provider was called for a throttled invitation")
	}
	f.assertNothingCreated(t)
	if len(f.limits.Keys) == 0 || !strings.HasPrefix(f.limits.Keys[0], "access-invite:tenant:"+f.h.TenantID.String()) {
		t.Fatalf("limiter keys = %v, want the tenant key first", f.limits.Keys)
	}
	f.limits.Deny, f.limits.Fails = false, errors.New("dragonfly down")
	if _, err := handle(f.ctx, f.deps, command); err == nil {
		t.Fatal("limiter outage was ignored")
	} else if code, _, _ := apperror.Public(err); code != apperror.DependencyUnavailable {
		t.Fatalf("limiter outage code=%s", code)
	}
	f.limits.Fails, f.limits.Keys = nil, nil
	if _, err := handle(f.ctx, f.deps, command); err != nil {
		t.Fatal(err)
	}
	emailKey := "access-invite:email:" + f.h.TenantID.String() + ":limite@example.test"
	if len(f.limits.Keys) != 2 || f.limits.Keys[1] != emailKey {
		t.Fatalf("limiter keys = %v, want tenant then recipient", f.limits.Keys)
	}
}

func TestTFE_ReplayThatRewritesPendingAccessIsAudited(t *testing.T) {
	f := newFixture(t)
	first := f.command("auditoria@example.test", Scope{Kind: "ASSET", ResourceID: f.h.AssetA1})
	created, err := handle(f.ctx, f.deps, first)
	if err != nil {
		t.Fatal(err)
	}
	second := f.command("auditoria@example.test", Scope{Kind: "ASSET", ResourceID: f.h.AssetA2})
	second.ClientID = "another-key"
	if _, err := handle(f.ctx, f.deps, second); err != nil {
		t.Fatal(err)
	}
	if got := f.env.Count(t, "access.resource_scopes", f.h.TenantID, "membership_id=? AND resource_id=?", created.Membership.ID, f.h.AssetA2); got != 1 {
		t.Fatalf("replacement scope rows = %d, want 1", got)
	}
	if got := f.env.Count(t, "audit.events", f.h.TenantID, "action='access.customer_invited' AND target_id=?", created.Membership.ID.String()); got != 2 {
		t.Fatalf("audit rows = %d, want the invitation and the rewrite", got)
	}
	if got := f.env.Count(t, "audit.events", f.h.TenantID, "action='access.customer_invited' AND target_id=? AND reason LIKE 'replay;%'", created.Membership.ID.String()); got != 1 {
		t.Fatalf("replay audit rows = %d, want 1", got)
	}
}
