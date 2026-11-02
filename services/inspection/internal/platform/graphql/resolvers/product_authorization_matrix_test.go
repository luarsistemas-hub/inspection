package resolvers

import (
	"fmt"
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/access/accesstest"
	"inspection/services/inspection/internal/platform/auth"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"

	"github.com/99designs/gqlgen/graphql/handler"
)

func productMatrixServer(env accesstest.Env) *handler.Server {
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: env.Runtime, Bus: mediator.New(), Authorizer: env.Authorizer()}}))
	server.SetErrorPresenter(graph.PresentError)
	return server
}

// TestTFE_IT_205DataResolversDenyAMembershipWithoutEntitlementForTheTokenProduct
// proves that no data resolver trusts a local role check alone: a MANAGER whose
// token is minted for either product but who holds no entitlement is denied
// everywhere, before any data is read.
func TestTFE_IT_205DataResolversDenyAMembershipWithoutEntitlementForTheTokenProduct(t *testing.T) {
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	manager := env.AddMembership(t, h.TenantID, auth.Manager, "ACTIVE", nil, accesstest.Grant{Kind: "BUSINESS_UNIT", ResourceID: h.UnitA})
	server := productMatrixServer(env)
	inspection, id := h.InspectionA1.String(), identity.NewID().String()
	operations := map[string]string{
		"memberships":            `query { memberships { nodes { id } } }`,
		"businessUnits":          `query { businessUnits { nodes { id } } }`,
		"notificationDeliveries": `query { notificationDeliveries { nodes { id } } }`,
		"publicationPolicy":      `query { publicationPolicy { mode } }`,
		"usageSummary":           `query { usageSummary { requests } }`,
		"videoPreparations":      `query { videoPreparations { nodes { mediaId } } }`,
		"report":                 fmt.Sprintf(`query { report(inspectionId:%q) { id } }`, inspection),
		"reports":                `query { reports { nodes { id } } }`,
		"myNotifications":        `query { myNotifications { nodes { id } } }`,
		"customerTimeline":       `query { customerTimeline { nodes { id } } }`,
		"triageWorkspace":        `query { triageWorkspace { nodes { inspectionId } } }`,
		"triageCase":             fmt.Sprintf(`query { triageCase(inspectionId:%q) { inspectionId } }`, inspection),
		"sensitiveContentBypass": `query { sensitiveContentScreeningBypassed }`,
		"voicePolicy":            `query { voicePolicy { enabled } }`,
		"publishReport":          fmt.Sprintf(`mutation { publishReport(input:{inspectionId:%q, snapshotId:%q, clientMutationId:"c"}) { clientMutationId } }`, inspection, id),
		"markNotificationRead":   fmt.Sprintf(`mutation { markNotificationRead(input:{notificationId:%q, clientMutationId:"c"}) { clientMutationId } }`, id),
		"configurePublication":   `mutation { configurePublicationPolicy(input:{mode:"MANUAL", expectedVersion:0, clientMutationId:"c"}) { clientMutationId } }`,
		"applyLegalHold":         fmt.Sprintf(`mutation { applyLegalHold(input:{inspectionId:%q, reason:"x", clientMutationId:"c"}) { clientMutationId } }`, inspection),
		"recordDeletionRequest":  fmt.Sprintf(`mutation { recordDeletionRequest(input:{inspectionId:%q, reason:"x", clientMutationId:"c"}) { clientMutationId } }`, inspection),
		"declareFalsePositive":   fmt.Sprintf(`mutation { declareSensitiveDetectionFalsePositive(input:{mediaId:%q, reason:"x", clientMutationId:"c"}) { clientMutationId } }`, id),
		"updateTriageCase":       fmt.Sprintf(`mutation { updateTriageCase(input:{inspectionId:%q, expectedVersion:1, action:ASSIGN, clientMutationId:"c"}) { clientMutationId } }`, inspection),
	}
	for _, audience := range []string{"inspection-admin", "inspection-dashboard"} {
		for name, query := range operations {
			raw := executeTask06GraphQL(t, server, query, accesstest.Context(manager, audience))
			response := decodeGraphQL(t, raw)
			switch response.errorCode() {
			case "FORBIDDEN", "UNAUTHENTICATED", "NOT_FOUND":
			case "":
				// A schema-validation error carries no extension code; any other success is a leak.
				if len(response.Errors) == 0 {
					t.Errorf("%s (%s): a membership without entitlements was served: %s", name, audience, raw)
				}
			default:
				t.Errorf("%s (%s): unexpected code %q: %s", name, audience, response.errorCode(), raw)
			}
			if strings.Contains(raw, `"nodes":[`) && !strings.Contains(raw, `"errors"`) {
				t.Errorf("%s (%s): data returned without entitlement: %s", name, audience, raw)
			}
		}
	}
}

// TestTFE_IT_205RevokingAnEntitlementDeniesTheNextRequestForEachProduct covers
// one read and one write per product with the real authorizer and store.
func TestTFE_IT_205RevokingAnEntitlementDeniesTheNextRequestForEachProduct(t *testing.T) {
	env := accesstest.Open(t)
	h := env.SeedTenant(t)
	admin := env.AddMembership(t, h.TenantID, auth.TenantAdmin, "ACTIVE", []string{auth.AdminProduct, auth.DashboardProduct})
	server := productMatrixServer(env)
	cases := []struct {
		name, product, audience, query string
	}{
		{"admin read", auth.AdminProduct, "inspection-admin", `query { memberships { nodes { id } } }`},
		{"admin write", auth.AdminProduct, "inspection-admin", `mutation { configurePublicationPolicy(input:{mode:"MANUAL", expectedVersion:0, clientMutationId:"c"}) { clientMutationId } }`},
		{"dashboard read", auth.DashboardProduct, "inspection-dashboard", `query { myNotifications { nodes { id } } }`},
		{"dashboard write", auth.DashboardProduct, "inspection-dashboard", fmt.Sprintf(`mutation { markNotificationRead(input:{notificationId:%q, clientMutationId:"c"}) { clientMutationId } }`, identity.NewID())},
	}
	for _, tc := range cases {
		ctx := accesstest.Context(admin, tc.audience)
		// Entitled: the request passes authorization (it may still fail later on missing data).
		if code := decodeGraphQL(t, executeTask06GraphQL(t, server, tc.query, ctx)).errorCode(); code == "FORBIDDEN" || code == "UNAUTHENTICATED" {
			t.Fatalf("%s: entitled TENANT_ADMIN was denied (%s)", tc.name, code)
		}
		if err := env.Admin.Exec("DELETE FROM access.product_entitlements WHERE tenant_id=? AND membership_id=? AND product=?", h.TenantID, admin.ID, tc.product).Error; err != nil {
			t.Fatal(err)
		}
		if code := decodeGraphQL(t, executeTask06GraphQL(t, server, tc.query, ctx)).errorCode(); code != "FORBIDDEN" {
			t.Fatalf("%s: code=%q after revoking %s, want FORBIDDEN on the very next request", tc.name, code, tc.product)
		}
		if err := env.Admin.Exec("INSERT INTO access.product_entitlements (id, tenant_id, membership_id, product) VALUES (?,?,?,?)", identity.NewID(), h.TenantID, admin.ID, tc.product).Error; err != nil {
			t.Fatal(err)
		}
	}
}

// productsForRoles returns the product entitlements a membership with any of
// roles would hold, the way bootstrap and invitation grant them.
func productsForRoles(roles []string) []string {
	seen := map[string]bool{}
	var products []string
	for _, role := range roles {
		for _, product := range auth.ProductsForRole(role) {
			if !seen[product] {
				seen[product] = true
				products = append(products, product)
			}
		}
	}
	return products
}
