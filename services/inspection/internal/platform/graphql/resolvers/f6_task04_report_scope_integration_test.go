//go:build integration

package resolvers

import (
	"context"
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

// IT-258: a reader with the report capability whose scope does not cover the
// inspection gets NOT_FOUND (existence is not disclosed) for the report, its
// evidence, its download and its publication; a role without the operation
// capability keeps getting FORBIDDEN; a reader whose scope covers it succeeds.
func TestVV_IT_258OutOfScopeReaderGetsNotFoundAndMissingCapabilityStaysForbidden(t *testing.T) {
	fx := newAPI11Fixture(t)
	actor := func(role string, scope requestctx.Scope) context.Context {
		return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, CorrelationID: "vv-it-258", Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: fx.seed.TenantID, Internal: true, Roles: []string{role}, Scopes: []requestctx.Scope{scope}, ProductEntitlements: auth.ProductsForRole(role)}})
	}
	foreign := requestctx.Scope{Kind: "BUSINESS_UNIT", ID: identity.NewID()}
	own := requestctx.Scope{Kind: "BUSINESS_UNIT", ID: fx.seed.BusinessUnitID}
	queries := map[string]string{
		"report":         api11Query(fx, "report", fx.seed.InspectionID, ""),
		"reportDownload": api13Query(fx.snapshotID),
		"publishReport":  api12Query(fx, "publishReport", "vv-it-258-publish", fx.snapshotID, 0),
	}
	for name, query := range queries {
		t.Run(name+"/out of scope", func(t *testing.T) {
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, actor(auth.Manager, foreign), "", "")
			assertAPI11Rejected(t, response, "NOT_FOUND")
			if strings.Contains(response, "FORBIDDEN") || strings.Contains(response, "Você não tem acesso") {
				t.Fatalf("out-of-scope response disclosed that the inspection exists: %s", response)
			}
		})
	}
	t.Run("report/in scope", func(t *testing.T) {
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, queries["report"], actor(auth.Manager, own), "", "")
		if strings.Contains(response, `"errors"`) || !strings.Contains(response, fx.frameID.String()) {
			t.Fatalf("in-scope reader was refused the report: %s", response)
		}
	})
	t.Run("missing capability stays FORBIDDEN", func(t *testing.T) {
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, queries["reportDownload"], actor(auth.OrganizationAdmin, own), "", "")
		assertAPI11Rejected(t, response, "FORBIDDEN")
	})
}
