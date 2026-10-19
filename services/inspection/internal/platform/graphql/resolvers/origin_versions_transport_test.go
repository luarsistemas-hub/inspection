package resolvers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	originclose "inspection/services/inspection/internal/features/origins/close_invitation"
	originlist "inspection/services/inspection/internal/features/origins/list_versions"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
)

func TestOriginVersionReadinessFieldsGraphQLTransport(t *testing.T) {
	tenantID, assetID := identity.NewID(), identity.NewID()
	photoID, videoID := identity.NewID(), identity.NewID()
	queued, retained := "RECEIVED", "RETAINED"
	bus := mediator.New()
	if err := bus.RegisterQuery(assetget.Query{}, func(context.Context, any) (any, error) { return assetcore.View{}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(originlist.Query{}, func(context.Context, any) (any, error) {
		return originlist.Result{
			Versions: []database.OriginVersion{
				{ID: photoID, Status: "ACTIVE", MediaKind: "PHOTO"},
				{ID: videoID, Status: "DRAFT", MediaKind: "VIDEO"},
			},
			Details: map[identity.ID]originlist.Details{
				photoID: {CaptureSubmitted: true, Selectable: true},
				videoID: {PreparationStatus: &queued, SourceAvailability: &retained, Selectable: false},
			},
		}, nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID})
	query := `query { originVersions(assetId:"` + assetID.String() + `") { nodes { id mediaKind preparationStatus captureSubmitted selectable sourceAvailability } } }`
	body := executeOriginVersionGraphQL(t, bus, query, ctx)
	if strings.Contains(body, `"errors"`) || !strings.Contains(body, `"mediaKind":"PHOTO","preparationStatus":null,"captureSubmitted":true,"selectable":true,"sourceAvailability":null`) || !strings.Contains(body, `"mediaKind":"VIDEO","preparationStatus":"RECEIVED","captureSubmitted":false,"selectable":false,"sourceAvailability":"RETAINED"`) {
		t.Fatalf("origin readiness fields were not projected: %s", body)
	}
}

func TestCloseOriginInvitationGraphQLTransport(t *testing.T) {
	tenantID, actorID, versionID, originID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: actorID, TenantID: tenantID, Roles: []string{auth.TenantAdmin}}})
	mutation := `mutation { closeOriginInvitation(input:{versionId:"` + versionID.String() + `",reason:"operator closed",clientMutationId:"close-k1"}) { version { id status selectable } userErrors { code field message } clientMutationId } }`
	body := executeOriginVersionGraphQL(t, mediator.New(), mutation, ctx, func(bus *mediator.Bus) {
		if err := bus.RegisterCommand(originclose.Command{}, func(context.Context, any) (any, error) {
			return database.OriginVersion{ID: versionID, OriginID: originID, VersionNumber: 1, Status: "INVALIDATED", MediaKind: "PHOTO"}, nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := bus.RegisterQuery(originlist.VersionDetailsQuery{}, func(context.Context, any) (any, error) {
			return originlist.Result{Details: map[identity.ID]originlist.Details{versionID: {}}}, nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(body, `"errors"`) || !strings.Contains(body, `"status":"INVALIDATED"`) || !strings.Contains(body, `"userErrors":[]`) || !strings.Contains(body, `"clientMutationId":"close-k1"`) {
		t.Fatalf("unexpected close success response: %s", body)
	}

	invalidState := executeOriginVersionGraphQL(t, mediator.New(), mutation, ctx, func(bus *mediator.Bus) {
		if err := bus.RegisterCommand(originclose.Command{}, func(context.Context, any) (any, error) {
			return nil, apperror.New(apperror.InvalidState, "versionId", "only draft invitations can be closed")
		}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(invalidState, `"errors"`) || !strings.Contains(invalidState, `"version":null`) || !strings.Contains(invalidState, `"code":"INVALID_STATE"`) {
		t.Fatalf("invalid state did not use userErrors: %s", invalidState)
	}
}

func executeOriginVersionGraphQL(t *testing.T, bus *mediator.Bus, query string, ctx context.Context, register ...func(*mediator.Bus)) string {
	t.Helper()
	for _, setup := range register {
		setup(bus)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{Bus: bus}}))
	server.SetErrorPresenter(graph.PresentError)
	request := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+fmt.Sprintf("%q", query)+"}")).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response.Body.String()
}
