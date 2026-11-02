package resolvers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/access/accesstest"
	assignaccess "inspection/services/inspection/internal/features/access/assign_membership_access"
	explainaccess "inspection/services/inspection/internal/features/access/explain_effective_access"
	invitecustomer "inspection/services/inspection/internal/features/access/invite_customer_user"
	inviteinternal "inspection/services/inspection/internal/features/access/invite_internal_user"
	"inspection/services/inspection/internal/platform/auth"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"

	"github.com/99designs/gqlgen/graphql/handler"
)

type accessGraphQL struct {
	env    accesstest.Env
	h      accesstest.Hierarchy
	server *handler.Server
	sender *accesstest.Sender
}

func newAccessGraphQL(t *testing.T) accessGraphQL {
	t.Helper()
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	authorizer := env.Authorizer()
	bus := mediator.New()
	sender := &accesstest.Sender{}
	provider := &accesstest.Provider{}
	setups := []error{
		explainaccess.Setup(explainaccess.Dependencies{DB: env.Runtime, Bus: bus, Authorizer: authorizer, Scopes: auth.GORMScopeResolver{DB: env.Runtime}}),
		assignaccess.Setup(assignaccess.Dependencies{DB: env.Runtime, Bus: bus, Authorizer: authorizer}),
		invitecustomer.Setup(invitecustomer.Dependencies{DB: env.Runtime, Bus: bus, Authorizer: authorizer, Issuer: "https://issuer.test", AdminOrigin: "https://admin.test", Provider: provider, Sender: sender, Limits: &accesstest.Limits{}}),
		inviteinternal.Setup(inviteinternal.Dependencies{DB: env.Runtime, Bus: bus, Authorizer: authorizer, Issuer: "https://issuer.test", AdminOrigin: "https://admin.test", Provider: provider, Sender: sender, Limits: &accesstest.Limits{}}),
	}
	for _, err := range setups {
		if err != nil {
			t.Fatal(err)
		}
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: env.Runtime, Bus: bus, Authorizer: authorizer}}))
	server.SetErrorPresenter(graph.PresentError)
	return accessGraphQL{env: env, h: h, server: server, sender: sender}
}

type graphqlResponse struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []struct {
		Message    string         `json:"message"`
		Extensions map[string]any `json:"extensions"`
	} `json:"errors"`
}

func decodeGraphQL(t *testing.T, raw string) graphqlResponse {
	t.Helper()
	var decoded graphqlResponse
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("response %q: %v", raw, err)
	}
	return decoded
}

func (r graphqlResponse) errorCode() string {
	if len(r.Errors) == 0 {
		return ""
	}
	code, _ := r.Errors[0].Extensions["code"].(string)
	return code
}

func TestTFE_IT_182IT_184EffectiveAccessAndScopePreviewThroughGraphQL(t *testing.T) {
	g := newAccessGraphQL(t)
	admin := g.env.AddMembership(t, g.h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct, auth.DashboardProduct})
	holder := g.env.AddMembership(t, g.h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: g.h.UnitA})
	ctx := accesstest.Context(admin, "inspection-admin")
	query := fmt.Sprintf(`query { effectiveAccess(membershipId:%q, first:2) { membershipId role status version tenantWide productEntitlements grants { kind resourceId name historical grantable } descendants { nodes { kind resourceId inheritedFrom { kind resourceId } historical grantable } pageInfo { hasNextPage endCursor } } } }`, holder.ID)
	response := decodeGraphQL(t, executeTask06GraphQL(t, g.server, query, ctx))
	if len(response.Errors) != 0 {
		t.Fatalf("effectiveAccess errors: %+v", response.Errors)
	}
	var access struct {
		Role       string
		Version    int
		TenantWide bool
		Products   []string `json:"productEntitlements"`
		Grants     []struct {
			Kind, ResourceID, Name string
			Grantable              bool
		}
		Descendants struct {
			Nodes []struct {
				Kind          string
				InheritedFrom struct{ Kind, ResourceID string }
			}
			PageInfo struct {
				HasNextPage bool
				EndCursor   string
			}
		}
	}
	if err := json.Unmarshal(response.Data["effectiveAccess"], &access); err != nil {
		t.Fatal(err)
	}
	if access.Role != auth.Manager || access.TenantWide || len(access.Products) != 1 || len(access.Grants) != 1 || access.Grants[0].Name != "Unidade A" || !access.Grants[0].Grantable {
		t.Fatalf("effectiveAccess = %+v", access)
	}
	if len(access.Descendants.Nodes) != 2 || !access.Descendants.PageInfo.HasNextPage || access.Descendants.PageInfo.EndCursor == "" {
		t.Fatalf("first descendants page = %+v", access.Descendants)
	}
	for _, node := range access.Descendants.Nodes {
		if node.InheritedFrom.Kind != "BUSINESS_UNIT" || node.InheritedFrom.ResourceID != g.h.UnitA.String() {
			t.Fatalf("node %+v is not attributed to the grant", node)
		}
	}
	next := fmt.Sprintf(`query { effectiveAccess(membershipId:%q, first:100, after:%q) { descendants { nodes { resourceId } pageInfo { hasNextPage } } } }`, holder.ID, access.Descendants.PageInfo.EndCursor)
	rest := decodeGraphQL(t, executeTask06GraphQL(t, g.server, next, ctx))
	var tail struct {
		Descendants struct {
			Nodes    []struct{ ResourceID string }
			PageInfo struct{ HasNextPage bool }
		}
	}
	if err := json.Unmarshal(rest.Data["effectiveAccess"], &tail); err != nil || tail.Descendants.PageInfo.HasNextPage || len(tail.Descendants.Nodes) != 3 {
		t.Fatalf("second page = %s err=%v, want the remaining 3 descendants", rest.Data["effectiveAccess"], err)
	}
	preview := executeTask06GraphQL(t, g.server, fmt.Sprintf(`query { scopePreview(kind:"ASSET", resourceId:%q) { target { kind name historical grantable } descendants { nodes { kind inheritedFrom { kind } } } } }`, g.h.AssetA1), ctx)
	if !strings.Contains(preview, `"name":"Imóvel A1"`) || !strings.Contains(preview, `"kind":"PROJECT"`) || !strings.Contains(preview, `"kind":"INSPECTION"`) || strings.Contains(preview, `"kind":"ASSET"},`) {
		t.Fatalf("scopePreview = %s", preview)
	}
}

func TestTFE_IT_183IT_185AccessQueriesReturnStableSafeErrors(t *testing.T) {
	g := newAccessGraphQL(t)
	other := g.env.SeedTenant(t)
	admin := g.env.AddMembership(t, g.h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct})
	foreign := g.env.AddMembership(t, other.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct})
	manager := g.env.AddMembership(t, g.h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct})
	adminCtx, managerCtx := accesstest.Context(admin, "inspection-admin"), accesstest.Context(manager, "inspection-dashboard")
	for name, tc := range map[string]struct {
		query, code string
		ctx         bool
	}{
		"cross-tenant membership": {fmt.Sprintf(`query { effectiveAccess(membershipId:%q) { membershipId role } }`, foreign.ID), "NOT_FOUND", true},
		"unknown membership":      {fmt.Sprintf(`query { effectiveAccess(membershipId:%q) { membershipId role } }`, identity.NewID()), "NOT_FOUND", true},
		"malformed membership":    {`query { effectiveAccess(membershipId:"x") { membershipId } }`, "INVALID_INPUT", true},
		"unknown kind":            {fmt.Sprintf(`query { scopePreview(kind:"UNKNOWN", resourceId:%q) { target { kind } } }`, g.h.UnitA), "INVALID_INPUT", true},
		"foreign resource":        {fmt.Sprintf(`query { scopePreview(kind:"BUSINESS_UNIT", resourceId:%q) { target { kind } } }`, other.UnitA), "NOT_FOUND", true},
		"manager is forbidden":    {fmt.Sprintf(`query { effectiveAccess(membershipId:%q) { membershipId } }`, manager.ID), "FORBIDDEN", false},
		"manager preview":         {fmt.Sprintf(`query { scopePreview(kind:"BUSINESS_UNIT", resourceId:%q) { target { kind } } }`, g.h.UnitA), "FORBIDDEN", false},
	} {
		ctx := adminCtx
		if !tc.ctx {
			ctx = managerCtx
		}
		response := decodeGraphQL(t, executeTask06GraphQL(t, g.server, tc.query, ctx))
		if response.errorCode() != tc.code {
			t.Fatalf("%s: code=%q errors=%+v data=%v", name, response.errorCode(), response.Errors, response.Data)
		}
		if strings.Contains(response.Errors[0].Message, foreign.ID.String()) || strings.Contains(response.Errors[0].Message, "SQLSTATE") {
			t.Fatalf("%s: unsafe error message %q", name, response.Errors[0].Message)
		}
	}
}

func TestTFE_IT_197IT_198InviteCustomerUserThroughGraphQL(t *testing.T) {
	g := newAccessGraphQL(t)
	other := g.env.SeedTenant(t)
	admin := g.env.AddMembership(t, g.h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct, auth.DashboardProduct})
	ctx := accesstest.Context(admin, "inspection-admin")
	mutation := func(email, scopes, extra string) graphqlResponse {
		return decodeGraphQL(t, executeTask06GraphQL(t, g.server, fmt.Sprintf(`mutation { inviteCustomerUser(input:{name:"Cliente", email:%q, scopes:[%s], clientMutationId:"m-%s" %s}) { membership { id role status invitationStatus productEntitlements scopes { kind resourceId } } userErrors { code field message } clientMutationId } }`, email, scopes, email, extra), ctx))
	}
	type payload struct {
		Membership *struct {
			ID, Role, Status, InvitationStatus string
			ProductEntitlements                []string
			Scopes                             []struct{ Kind, ResourceID string }
		}
		UserErrors []struct{ Code, Field, Message string }
	}
	read := func(response graphqlResponse) payload {
		t.Helper()
		if len(response.Errors) != 0 {
			t.Fatalf("unexpected GraphQL errors: %+v", response.Errors)
		}
		var decoded payload
		if err := json.Unmarshal(response.Data["inviteCustomerUser"], &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	created := read(mutation("cliente@example.test", fmt.Sprintf(`{kind:"ASSET", resourceId:%q}`, g.h.AssetA1), ""))
	if created.Membership == nil || len(created.UserErrors) != 0 || created.Membership.Role != auth.CustomerViewer || created.Membership.Status != "INVITED" || created.Membership.InvitationStatus != "SENT" ||
		len(created.Membership.ProductEntitlements) != 1 || created.Membership.ProductEntitlements[0] != auth.DashboardProduct || len(created.Membership.Scopes) != 1 {
		t.Fatalf("created = %+v", created)
	}
	rejected := map[string]payload{
		"empty scope":  read(mutation("vazio@example.test", ``, "")),
		"cross tenant": read(mutation("outro@example.test", fmt.Sprintf(`{kind:"BUSINESS_UNIT", resourceId:%q}`, other.UnitA), "")),
		"admin":        read(mutation("admin@example.test", fmt.Sprintf(`{kind:"ASSET", resourceId:%q}`, g.h.AssetA1), `productEntitlements:["ADMIN"]`)),
		"bad email":    read(mutation("sem-arroba", fmt.Sprintf(`{kind:"ASSET", resourceId:%q}`, g.h.AssetA1), "")),
	}
	wantMessage := map[string]string{
		"empty scope":  "Selecione ao menos uma abrangência",
		"cross tenant": "não pertence a esta imobiliária",
		"admin":        "não pode receber acesso",
		"bad email":    "e-mail válido",
	}
	for name, result := range rejected {
		if result.Membership != nil || len(result.UserErrors) != 1 || result.UserErrors[0].Code != "INVALID_INPUT" || !strings.Contains(result.UserErrors[0].Message, wantMessage[name]) {
			t.Fatalf("%s: payload = %+v, want a Portuguese user error containing %q", name, result, wantMessage[name])
		}
	}
	if rows := g.env.Count(t, "access.memberships", g.h.TenantID, "role='CUSTOMER_VIEWER'"); rows != 1 {
		t.Fatalf("customer memberships = %d, want only the valid invitation", rows)
	}
}

func TestTFE_IT_203IT_204AssignMembershipAccessThroughGraphQL(t *testing.T) {
	g := newAccessGraphQL(t)
	admin := g.env.AddMembership(t, g.h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct, auth.DashboardProduct})
	target := g.env.AddMembership(t, g.h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: g.h.UnitA})
	ctx := accesstest.Context(admin, "inspection-admin")
	mutation := func(version int) graphqlResponse {
		return decodeGraphQL(t, executeTask06GraphQL(t, g.server, fmt.Sprintf(`mutation { assignMembershipAccess(input:{membershipId:%q, role:"EMPLOYEE", productEntitlements:["DASHBOARD"], scopes:[{kind:"PROJECT", resourceId:%q}], expectedVersion:%d, clientMutationId:"assign-%d"}) { membership { id role version productEntitlements scopes { kind } } userErrors { code field message } clientMutationId } }`, target.ID, g.h.ProjectA1, version, version), ctx))
	}
	ok := mutation(1)
	if len(ok.Errors) != 0 || !strings.Contains(string(ok.Data["assignMembershipAccess"]), `"role":"EMPLOYEE"`) || !strings.Contains(string(ok.Data["assignMembershipAccess"]), `"version":2`) || !strings.Contains(string(ok.Data["assignMembershipAccess"]), `"kind":"PROJECT"`) {
		t.Fatalf("assign = %s errors=%+v", ok.Data["assignMembershipAccess"], ok.Errors)
	}
	stale := mutation(1)
	if len(stale.Errors) != 0 {
		t.Fatalf("stale write must be a user error, got GraphQL errors %+v", stale.Errors)
	}
	var payload struct {
		Membership *struct{ ID string }
		UserErrors []struct{ Code, Field, Message string }
	}
	if err := json.Unmarshal(stale.Data["assignMembershipAccess"], &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Membership != nil || len(payload.UserErrors) != 1 || payload.UserErrors[0].Code != "CONFLICT" || payload.UserErrors[0].Field != "expectedVersion" || !strings.Contains(payload.UserErrors[0].Message, "Atualize") {
		t.Fatalf("stale payload = %+v", payload)
	}
	missing := executeTask06GraphQL(t, g.server, fmt.Sprintf(`mutation { assignMembershipAccess(input:{membershipId:%q, role:"EMPLOYEE", productEntitlements:["DASHBOARD"], scopes:[]}) { userErrors { code } } }`, target.ID), ctx)
	if !strings.Contains(missing, "expectedVersion") && !strings.Contains(missing, "clientMutationId") {
		t.Fatalf("a missing expectedVersion must be rejected by the schema: %s", missing)
	}
}

func TestTFE_IT_201IT_202InviteInternalUserEntitlementsThroughGraphQL(t *testing.T) {
	g := newAccessGraphQL(t)
	admin := g.env.AddMembership(t, g.h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct, auth.DashboardProduct})
	ctx := accesstest.Context(admin, "inspection-admin")
	invite := func(email, role, products string) graphqlResponse {
		return decodeGraphQL(t, executeTask06GraphQL(t, g.server, fmt.Sprintf(`mutation { inviteInternalUser(input:{name:"Pessoa", email:%q, role:%q, scopes:[{kind:"PROJECT", resourceId:%q}], productEntitlements:%s, clientMutationId:"i-%s"}) { membership { role invitationStatus productEntitlements scopes { kind } } clientMutationId } }`, email, role, g.h.ProjectA1, products, email), ctx))
	}
	accepted := invite("gestor@example.test", "MANAGER", `["DASHBOARD"]`)
	if len(accepted.Errors) != 0 || !strings.Contains(string(accepted.Data["inviteInternalUser"]), `"productEntitlements":["DASHBOARD"]`) || !strings.Contains(string(accepted.Data["inviteInternalUser"]), `"kind":"PROJECT"`) {
		t.Fatalf("accepted = %s errors=%+v", accepted.Data["inviteInternalUser"], accepted.Errors)
	}
	denied := invite("gestor-admin@example.test", "MANAGER", `["ADMIN"]`)
	if denied.errorCode() != "INVALID_INPUT" {
		t.Fatalf("MANAGER with ADMIN was accepted: %+v %s", denied.Errors, denied.Data)
	}
	if rows := g.env.Count(t, "access.memberships", g.h.TenantID, "email='gestor-admin@example.test'"); rows != 0 {
		t.Fatalf("rejected invitation left %d memberships", rows)
	}
}

// TestTFE_LegacyAssignRoleScopesIsAnAliasUnderTheSameRules covers the
// privilege-escalation paths of the legacy mutation: a manager widening their
// own scope, a manager or access administrator demoting a tenant administrator,
// and a write without expectedVersion.
func TestTFE_LegacyAssignRoleScopesIsAnAliasUnderTheSameRules(t *testing.T) {
	g := newAccessGraphQL(t)
	tenantAdmin := g.env.AddMembership(t, g.h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct, auth.DashboardProduct})
	g.env.AddMembership(t, g.h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct, auth.DashboardProduct})
	manager := g.env.AddMembership(t, g.h.TenantID, auth.Manager, "ACTIVE", []string{auth.DashboardProduct}, accesstest.Grant{Kind: "ASSET", ResourceID: g.h.AssetA1})
	accessAdmin := g.env.AddMembership(t, g.h.TenantID, auth.AccessAdmin, "ACTIVE", []string{auth.AdminProduct})
	mutation := func(ctx accessCtx, membershipID, role, version, scope string) graphqlResponse {
		return decodeGraphQL(t, executeTask06GraphQL(t, g.server, fmt.Sprintf(`mutation { assignRoleScopes(input:{membershipId:%q, role:%q, scopes:[%s] %s, clientMutationId:"legacy"}) { membership { id role version } userErrors { code field message } } }`, membershipID, role, scope, version), ctx.ctx))
	}
	managerAudience, adminAudience := accessCtx{accesstest.Context(manager, "inspection-dashboard")}, accessCtx{accesstest.Context(accessAdmin, "inspection-admin")}
	wide := fmt.Sprintf(`{kind:"BUSINESS_UNIT", resourceId:%q}`, g.h.UnitA)
	// A manager widening their own scope: the dashboard token has no Admin product.
	if r := mutation(managerAudience, manager.ID.String(), auth.Manager, `expectedVersion:1`, wide); r.errorCode() != "FORBIDDEN" {
		t.Fatalf("manager widened their own scope: %+v %s", r.Errors, r.Data)
	}
	// A manager or an access administrator demoting a tenant administrator.
	for name, ctx := range map[string]accessCtx{"manager": managerAudience, "access admin": adminAudience} {
		if r := mutation(ctx, tenantAdmin.ID.String(), auth.Viewer, `expectedVersion:1`, ""); r.errorCode() != "FORBIDDEN" {
			t.Fatalf("%s demoted a tenant administrator: %+v %s", name, r.Errors, r.Data)
		}
	}
	// expectedVersion is mandatory.
	r := mutation(adminAudience, manager.ID.String(), auth.Employee, ``, wide)
	if !strings.Contains(string(r.Data["assignRoleScopes"]), `"INVALID_INPUT"`) || !strings.Contains(string(r.Data["assignRoleScopes"]), "expectedVersion") {
		t.Fatalf("a write without expectedVersion was accepted: %+v %s", r.Errors, r.Data)
	}
	if got := g.env.Count(t, "access.memberships", g.h.TenantID, "id=? AND role='TENANT_ADMIN'", tenantAdmin.ID); got != 1 {
		t.Fatal("the tenant administrator was changed")
	}
	// The legitimate path still works and replaces role and scopes.
	ok := mutation(adminAudience, manager.ID.String(), auth.Employee, `expectedVersion:1`, wide)
	if !strings.Contains(string(ok.Data["assignRoleScopes"]), `"role":"EMPLOYEE"`) || !strings.Contains(string(ok.Data["assignRoleScopes"]), `"version":2`) {
		t.Fatalf("legitimate assignment failed: %+v %s", ok.Errors, ok.Data)
	}
}

type accessCtx struct{ ctx context.Context }
