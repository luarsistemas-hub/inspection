//go:build integration

package resolvers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
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
	"inspection/services/inspection/internal/platform/testfailure"

	"github.com/99designs/gqlgen/graphql/handler"
)

func TestVV_IT_437InventoryOriginAuthorizationMatrix(t *testing.T) {
	fx := newAPI06Fixture(t)
	query := api06Query(fx, "originVersions", "", "")
	admin := fx.ctx
	viewer := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: requestctx.Principal{IdentityID: fx.seed.IdentityID, MembershipID: fx.seed.MembershipID, TenantID: fx.seed.TenantID, Roles: []string{auth.CustomerViewer}, ProductEntitlements: auth.ProductsForRole(auth.CustomerViewer)}, CorrelationID: "vv-it-437-viewer"})
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

func TestVV_IT_438CurrentOriginOperationsRequireAuthentication(t *testing.T) {
	fx := newAPI06Fixture(t)
	versionID := fx.originVersionID.String()
	for _, query := range []string{
		api06Query(fx, "originVersions", "", ""),
		api06Query(fx, "invalidateOriginVersion", "vv-it-438", versionID),
		api06Query(fx, "inviteOriginCapture", "vv-it-438-invite", ""),
		api06Query(fx, "activateOriginVersion", "vv-it-438-activate", versionID),
		api06Query(fx, "promoteInspectionVideo", "vv-it-438-promote", ""),
	} {
		if response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, context.Background(), "", ""); !strings.Contains(response, "UNAUTHENTICATED") {
			t.Errorf("origin operation without credentials: %s", response)
		}
	}
}

func TestVV_IT_446InvalidateOriginVersionUsesRealResolverAndService(t *testing.T) {
	fx := newAPI06Fixture(t)
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, "invalidateOriginVersion", "vv-it-446-invalidate", fx.originVersionID.String()), fx.ctx, "", "")
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"status":"INVALIDATED"`) {
		t.Fatalf("invalidateOriginVersion did not execute the real transition: %s", response)
	}
}

func TestVV_IT_439CurrentOriginOperationsRequireCapability(t *testing.T) {
	fx := newAPI06Fixture(t)
	viewer := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: requestctx.Principal{IdentityID: fx.seed.IdentityID, MembershipID: fx.seed.MembershipID, TenantID: fx.seed.TenantID, Roles: []string{auth.CustomerViewer}, ProductEntitlements: auth.ProductsForRole(auth.CustomerViewer)}, CorrelationID: "vv-it-439-viewer"})
	for _, query := range []string{
		api06Query(fx, "originVersions", "", ""),
		api06Query(fx, "invalidateOriginVersion", "vv-it-439", fx.originVersionID.String()),
		api06Query(fx, "promoteInspectionVideo", "vv-it-439-promote", ""),
	} {
		if response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, viewer, "", ""); !strings.Contains(response, "FORBIDDEN") {
			t.Errorf("origin operation without capability: %s", response)
		}
	}
}

func TestVV_IT_440ForeignOriginResourcesReturnNotFound(t *testing.T) {
	fx := newAPI06Fixture(t)
	foreign, err := fx.h.SeedInventoryBaseline(context.Background(), harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`query { originVersions(assetId:"` + foreign.Task06Fixture.AssetID.String() + `") { nodes { id } } }`,
		api06Query(fx, "invalidateOriginVersion", "vv-it-440", foreign.OriginVersionID.String()),
		`mutation { promoteInspectionVideo(input:{inspectionId:"` + foreign.Task06Fixture.InspectionID.String() + `",clientMutationId:"vv-it-440-promote"}) { version { id } userErrors { code } } }`,
	} {
		if response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", ""); !strings.Contains(response, "NOT_FOUND") {
			t.Errorf("foreign origin resource should be hidden: %s", response)
		}
	}
}

func TestVV_IT_441CurrentOriginDependencyFailureHasNoSuccessPayload(t *testing.T) {
	fx := newAPI06Fixture(t)
	if err := testfailure.Install(fx.h.DB); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{api06Query(fx, "invalidateOriginVersion", "vv-it-441", fx.originVersionID.String()), api06Query(fx, "promoteInspectionVideo", "vv-it-441-promote", "")} {
		response := executeAPI06Failure(t, fx, query, "dependency")
		if !strings.Contains(response, "DEPENDENCY_UNAVAILABLE") || strings.Contains(response, `"version":{`) {
			t.Fatalf("dependency failure returned success or wrong error: %s", response)
		}
	}
}

func TestVV_IT_442CurrentOriginInternalFailureHidesDetails(t *testing.T) {
	fx := newAPI06Fixture(t)
	if err := testfailure.Install(fx.h.DB); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{api06Query(fx, "invalidateOriginVersion", "vv-it-442", fx.originVersionID.String()), api06Query(fx, "promoteInspectionVideo", "vv-it-442-promote", "")} {
		response := executeAPI06Failure(t, fx, query, "internal")
		if !strings.Contains(response, "INTERNAL") || strings.Contains(response, testfailure.Secret) || strings.Contains(response, `"version":{`) {
			t.Fatalf("internal failure was not generic: %s", response)
		}
	}
}

func executeAPI06Failure(t *testing.T, fx api06Fixture, query, mode string) string {
	t.Helper()
	body := `{"query":` + strconv.Quote(query) + `}`
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	operation := "invalidateOriginVersion"
	if strings.Contains(query, "promoteInspectionVideo") {
		operation = "promoteInspectionVideo"
	}
	req.Header.Set(testfailure.HeaderOperation, operation)
	req.Header.Set(testfailure.HeaderFailure, mode)
	req = testfailure.WithRequest(req.WithContext(fx.ctx), "test")
	w := httptest.NewRecorder()
	fx.server.ServeHTTP(w, req)
	return w.Body.String()
}

type api06Fixture struct {
	fixedOriginGraphQLFixture
	originVersionID identity.ID
}

func newAPI06Fixture(t *testing.T) api06Fixture {
	return newAPI06FixtureWithOptions(t, harness.Task06FixtureOptions{})
}

func newAPI06FixtureWithOptions(t *testing.T, options harness.Task06FixtureOptions) api06Fixture {
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
	baseline, err := h.SeedInventoryBaseline(ctx, options)
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
	meta := requestctx.Metadata{TenantID: seed.TenantID, Principal: requestctx.Principal{IdentityID: seed.IdentityID, MembershipID: seed.MembershipID, TenantID: seed.TenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: auth.ProductsForRole(auth.TenantAdmin)}, CorrelationID: "vv-api06"}
	fx := api06Fixture{fixedOriginGraphQLFixture: fixedOriginGraphQLFixture{ctx: requestctx.WithMetadata(ctx, meta), h: h, seed: seed, server: server, bus: bus, templateID: seed.TemplateID}, originVersionID: baseline.OriginVersionID}
	setHistoricalVideoAdmissionDisabled(t, fx.fixedOriginGraphQLFixture)
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
