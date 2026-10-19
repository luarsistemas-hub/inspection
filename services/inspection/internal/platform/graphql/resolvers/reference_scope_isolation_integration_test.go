//go:build integration

package resolvers

import (
	"context"
	"os"
	"strings"
	"testing"

	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	originlist "inspection/services/inspection/internal/features/origins/list_versions"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
)

func TestOtherTenantCannotReadVideoOriginScopeIT052(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	origin, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	other, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := assetget.Setup(assetget.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	if err := originlist.Setup(originlist.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Bus: bus}}))
	server.SetErrorPresenter(graph.PresentError)
	operator := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: other.TenantID, Principal: requestctx.Principal{IdentityID: other.IdentityID, MembershipID: other.MembershipID, TenantID: other.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "it052"})
	query := `query { originVersions(assetId:"` + origin.AssetID.String() + `") { nodes { id mediaKind status } } }`
	response := executeTask06GraphQL(t, server, query, operator)
	if !strings.Contains(response, `"code":"NOT_FOUND"`) || strings.Contains(response, origin.FrameID.String()) || strings.Contains(response, `"component":"sink"`) || strings.Contains(response, `"environment":"kitchen"`) {
		t.Fatalf("other tenant received origin scope or unexpected error: %s", response)
	}
}
