package resolvers

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/access/accesstest"
	assignaccess "inspection/services/inspection/internal/features/access/assign_membership_access"
	invitecustomer "inspection/services/inspection/internal/features/access/invite_customer_user"
	inviteinternal "inspection/services/inspection/internal/features/access/invite_internal_user"
	listaudit "inspection/services/inspection/internal/features/audit/list_events"
	"inspection/services/inspection/internal/platform/auth"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

// TestTFE_IT_224CaptureExternalSessionCannotCallAnyOIDCOperation proves that a
// request which authenticates only with the external Capture session cookie has
// no OIDC authority: every Admin and Dashboard operation is denied before any
// tenant data is read, and the two authority models are never merged.
func TestTFE_IT_224CaptureExternalSessionCannotCallAnyOIDCOperation(t *testing.T) {
	// The slices authorize before touching their (placeholder) database.
	bus, placeholderDB := mediator.New(), new(gorm.DB)
	authorizer := auth.Authorizer{}
	for _, err := range []error{
		inviteinternal.Setup(inviteinternal.Dependencies{DB: placeholderDB, Bus: bus, Authorizer: authorizer, Issuer: "https://issuer.test", AdminOrigin: "https://admin.test", Provider: &accesstest.Provider{}, Sender: &accesstest.Sender{}, Limits: &accesstest.Limits{}}),
		invitecustomer.Setup(invitecustomer.Dependencies{DB: placeholderDB, Bus: bus, Authorizer: authorizer, Issuer: "https://issuer.test", AdminOrigin: "https://admin.test", Provider: &accesstest.Provider{}, Sender: &accesstest.Sender{}, Limits: &accesstest.Limits{}}),
		assignaccess.Setup(assignaccess.Dependencies{DB: placeholderDB, Bus: bus, Authorizer: authorizer}),
		listaudit.Setup(listaudit.Dependencies{DB: placeholderDB, Bus: bus, Authorizer: authorizer}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{Bus: bus, Authorizer: authorizer}}))
	server.SetErrorPresenter(graph.PresentError)
	// Exactly what cmd/inspection-api/main.go installs for a request without a
	// bearer token: zeroed metadata, plus the external session credentials.
	zeroed := requestctx.WithMetadata(context.Background(), requestctx.Metadata{CorrelationID: "capture-session", StartedAt: time.Now().UTC()})
	external := requestctx.WithExternalCredentials(zeroed, requestctx.ExternalCredentials{SessionToken: strings.Repeat("a", 64), CSRFToken: "csrf"})
	id := identity.NewID().String()
	operations := map[string]string{
		"me":                 `query { me { identityId } }`,
		"tenant":             `query { tenant { id } }`,
		"memberships":        `query { memberships { nodes { id } } }`,
		"businessUnits":      `query { businessUnits { nodes { id } } }`,
		"auditEvents":        `query { auditEvents { nodes { id } } }`,
		"effectiveAccess":    fmt.Sprintf(`query { effectiveAccess(membershipId:%q) { role } }`, id),
		"scopePreview":       fmt.Sprintf(`query { scopePreview(kind:"ASSET", resourceId:%q) { target { kind } } }`, id),
		"dashboardSummary":   `query { dashboardSummary { total } }`,
		"inspections":        `query { inspections { nodes { id } } }`,
		"customerPortfolio":  `query { customerPortfolio { nodes { assetId } } }`,
		"myNotifications":    `query { myNotifications { nodes { id } } }`,
		"publicationPolicy":  `query { publicationPolicy { mode } }`,
		"inviteInternalUser": `mutation { inviteInternalUser(input:{name:"A", email:"a@example.test", role:"MANAGER", scopes:[], clientMutationId:"c"}) { clientMutationId } }`,
		"inviteCustomerUser": fmt.Sprintf(`mutation { inviteCustomerUser(input:{name:"A", email:"a@example.test", scopes:[{kind:"ASSET", resourceId:%q}], clientMutationId:"c"}) { clientMutationId } }`, id),
		"assignAccess":       fmt.Sprintf(`mutation { assignMembershipAccess(input:{membershipId:%q, role:"MANAGER", productEntitlements:["DASHBOARD"], scopes:[], expectedVersion:1, clientMutationId:"c"}) { clientMutationId } }`, id),
		"createBusinessUnit": `mutation { createBusinessUnit(input:{code:"x", name:"x", expectedTenantVersion:1, clientMutationId:"c"}) { clientMutationId } }`,
	}
	for name, query := range operations {
		raw := executeTask06GraphQL(t, server, query, external)
		response := decodeGraphQL(t, raw)
		code := response.errorCode()
		if code != "UNAUTHENTICATED" && code != "FORBIDDEN" {
			t.Errorf("%s: code=%q, want UNAUTHENTICATED or FORBIDDEN: %s", name, code, raw)
		}
		for _, value := range response.Data {
			if string(value) != "null" {
				t.Errorf("%s: protected data returned to an external session: %s", name, value)
			}
		}
	}
}

// TestTFE_IT_224AnonymousZeroedMetadataIsUnauthenticatedForAccessContext covers
// a request without a bearer token or cookie, for which the HTTP boundary still
// installs zeroed metadata.
func TestTFE_IT_224AnonymousZeroedMetadataIsUnauthenticatedForAccessContext(t *testing.T) {
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{Bus: mediator.New()}}))
	server.SetErrorPresenter(graph.PresentError)
	zeroed := requestctx.WithMetadata(context.Background(), requestctx.Metadata{CorrelationID: "anonymous", StartedAt: time.Now().UTC()})
	id := identity.NewID().String()
	for name, query := range map[string]string{
		"me":              `query { me { identityId } }`,
		"effectiveAccess": fmt.Sprintf(`query { effectiveAccess(membershipId:%q) { role } }`, id),
		"scopePreview":    fmt.Sprintf(`query { scopePreview(kind:"ASSET", resourceId:%q) { target { kind } } }`, id),
	} {
		if code := decodeGraphQL(t, executeTask06GraphQL(t, server, query, zeroed)).errorCode(); code != "UNAUTHENTICATED" {
			t.Errorf("%s: code=%q, want UNAUTHENTICATED", name, code)
		}
	}
}
