//go:build integration

package resolvers

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/features/origins/invalidate_version"
	"inspection/services/inspection/internal/features/origins/list_versions"
	originpromotevideo "inspection/services/inspection/internal/features/origins/promote_inspection_video"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
)

func TestVV_IT_437InventoryOriginAuthorizationMatrix(t *testing.T) {
	fx := newAPI06Fixture(t)
	query := api06Query(fx, "originVersions", "", "")
	admin := fx.ctx
	viewer := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: requestctx.Principal{IdentityID: fx.seed.IdentityID, MembershipID: fx.seed.MembershipID, TenantID: fx.seed.TenantID, Roles: []string{auth.CustomerViewer}}, CorrelationID: "vv-it-437-viewer"})
	if response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, context.Background(), "", ""); !strings.Contains(response, "UNAUTHENTICATED") {
		t.Fatalf("unauthenticated inventory list: %s", response)
	}
	if response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, viewer, "", ""); !strings.Contains(response, "FORBIDDEN") {
		t.Fatalf("customer inventory list: %s", response)
	}
	if response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, admin, "", ""); strings.Contains(response, `"errors"`) || !strings.Contains(response, `"hasInventory":true`) {
		t.Fatalf("authorized inventory list: %s", response)
	}
}

type api06Fixture struct {
	fixedOriginGraphQLFixture
}

func newAPI06Fixture(t *testing.T) api06Fixture {
	t.Helper()
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	baseline, err := h.SeedInventoryBaseline(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	seed := baseline.Task06Fixture
	bus := mediator.New()
	if err := assetget.Setup(assetget.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	if err := invalidate_version.Setup(invalidate_version.Dependencies{DB: h.DB, Bus: bus, Service: &invalidate_version.Service{DB: h.DB}, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	if err := list_versions.Setup(list_versions.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Within: h.WithinTenant}); err != nil {
		t.Fatal(err)
	}
	if err := originpromotevideo.Setup(originpromotevideo.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Bus: bus}}))
	server.SetErrorPresenter(graph.PresentError)
	meta := requestctx.Metadata{TenantID: seed.TenantID, Principal: requestctx.Principal{IdentityID: seed.IdentityID, MembershipID: seed.MembershipID, TenantID: seed.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "vv-api06"}
	fx := api06Fixture{fixedOriginGraphQLFixture{ctx: requestctx.WithMetadata(ctx, meta), h: h, seed: seed, server: server, bus: bus, templateID: seed.TemplateID}}
	setFixedVideoAdmission(t, fx.fixedOriginGraphQLFixture)
	return fx
}

func api06Query(fx api06Fixture, operation, clientID, versionID string) string {
	version := `version { id status mediaKind videoPreparationId preparationStatus sourceAvailability } userErrors { code field } clientMutationId`
	switch operation {
	case "inviteOriginCapture":
		return `mutation { inviteOriginCapture(input:{assetId:"` + fx.seed.AssetID.String() + `",participantId:"` + fx.seed.ParticipantID.String() + `",expiresAt:"` + time.Now().UTC().Add(time.Hour).Format(time.RFC3339) + `",clientMutationId:"` + clientID + `"}) { invitationId originVersionId status userErrors { code field } clientMutationId } }`
	case "activateOriginVersion", "invalidateOriginVersion":
		return `mutation { ` + operation + `(input:{versionId:"` + versionID + `",clientMutationId:"` + clientID + `"}) { ` + version + ` } }`
	case "originVersions":
		return `query { originVersions(assetId:"` + fx.seed.AssetID.String() + `") { nodes { id status hasInventory mediaKind videoPreparationId preparationStatus sourceAvailability } } }`
	case "promoteInspectionVideo":
		return `mutation { promoteInspectionVideo(input:{inspectionId:"` + fx.seed.InspectionID.String() + `",clientMutationId:"` + clientID + `"}) { ` + version + ` } }`
	}
	panic("unknown API06 operation " + operation)
}

func assertAPI06Rejected(t *testing.T, response, code string) {
	t.Helper()
	if !strings.Contains(response, `"code":"`+code+`"`) && !strings.Contains(response, code) {
		t.Fatalf("expected %s rejection, got %s", code, response)
	}
}

type originInviteNotificationStub struct{}

func (originInviteNotificationStub) Send(context.Context, notificationcore.Notification) (notificationcore.NotificationResult, error) {
	return notificationcore.NotificationResult{}, nil
}
