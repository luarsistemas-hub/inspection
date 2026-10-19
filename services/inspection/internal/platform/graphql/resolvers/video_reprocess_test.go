package resolvers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"inspection/libs/identity"
	mediavideoprep "inspection/services/inspection/internal/features/media/manage_video_preparation"
	"inspection/services/inspection/internal/platform/apperror"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
)

func TestReprocessVideoPreparationGraphQLEnvelope(t *testing.T) {
	tenantID, mediaID := identity.NewID(), identity.NewID()
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, CorrelationID: "reprocess-transport"})
	mutation := `mutation { reprocessVideoPreparation(input:{mediaId:"` + mediaID.String() + `",expectedPreparationVersion:4,profileDigest:"deployed-profile",clientMutationId:"reprocess-k1"}) { preparation { preparationId } userErrors { code field message } clientMutationId } }`
	preparationID := identity.NewID()
	response := executeReprocessVideoPreparation(t, mutation, ctx, func(context.Context, any) (any, error) {
		return mediavideoprep.Status{PreparationID: preparationID, MediaID: mediaID, Stage: "RECEIVED", PreparationVersion: 1}, nil
	})
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, preparationID.String()) || !strings.Contains(response, `"userErrors":[]`) || !strings.Contains(response, `"clientMutationId":"reprocess-k1"`) {
		t.Fatalf("unexpected successful response: %s", response)
	}

	for _, tc := range []struct {
		name, code, field, message string
	}{
		{"active work", string(apperror.InvalidState), "mediaId", "preparation is active"},
		{"stale version", string(apperror.Conflict), "expectedPreparationVersion", "preparation changed"},
		{"unknown profile", string(apperror.InvalidInput), "profileDigest", "unknown profile"},
		{"foreign media", string(apperror.NotFound), "mediaId", "video preparation not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := apperror.New(apperror.Code(tc.code), tc.field, tc.message)
			body := executeReprocessVideoPreparation(t, mutation, ctx, func(context.Context, any) (any, error) { return nil, err })
			if strings.Contains(body, `"errors"`) || !strings.Contains(body, `"preparation":null`) || !strings.Contains(body, `"code":"`+tc.code+`"`) || !strings.Contains(body, `"field":"`+tc.field+`"`) || !strings.Contains(body, tc.message) {
				t.Fatalf("business failure did not use a null-result userErrors envelope: %s", body)
			}
		})
	}

	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"unauthenticated", apperror.New(apperror.Unauthenticated, "", "authentication required"), "UNAUTHENTICATED"},
		{"forbidden", apperror.New(apperror.Forbidden, "", "access denied"), "FORBIDDEN"},
		{"dependency unavailable", apperror.New(apperror.DependencyUnavailable, "database", "database unavailable"), "DEPENDENCY_UNAVAILABLE"},
		{"internal", fmt.Errorf("database adapter failed: qa-secret-123"), "INTERNAL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := executeReprocessVideoPreparation(t, mutation, ctx, func(context.Context, any) (any, error) { return nil, tc.err })
			if !strings.Contains(body, tc.code) || strings.Contains(body, `"preparation":{`) || strings.Contains(body, "qa-secret-123") {
				t.Fatalf("unexpected protected failure response: %s", body)
			}
		})
	}
	unauthenticated := executeReprocessVideoPreparation(t, mutation, context.Background(), func(context.Context, any) (any, error) {
		t.Fatal("unauthenticated request reached the command bus")
		return nil, nil
	})
	if !strings.Contains(unauthenticated, "UNAUTHENTICATED") || strings.Contains(unauthenticated, `"reprocessVideoPreparation":{`) {
		t.Fatalf("unauthenticated mutation was not rejected: %s", unauthenticated)
	}
}

func executeReprocessVideoPreparation(t *testing.T, query string, ctx context.Context, send func(context.Context, any) (any, error)) string {
	t.Helper()
	bus := mediator.New()
	if err := bus.RegisterCommand(mediavideoprep.ReprocessCommand{}, send); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{Bus: bus}}))
	server.SetErrorPresenter(graph.PresentError)
	request := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+fmt.Sprintf("%q", query)+"}")).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response.Body.String()
}
