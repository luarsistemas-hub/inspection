package resolvers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"inspection/libs/identity"
	originlist "inspection/services/inspection/internal/features/origins/list_versions"
	originpromotevideo "inspection/services/inspection/internal/features/origins/promote_inspection_video"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
)

func TestPromoteInspectionVideoGraphQLEnvelope(t *testing.T) {
	inspectionID, tenantID := identity.NewID(), identity.NewID()
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID})
	mutation := `mutation { promoteInspectionVideo(input:{inspectionId:"` + inspectionID.String() + `",clientMutationId:"promote-k1"}) { version { id mediaKind preparationStatus selectable sourceAvailability } userErrors { code field message } clientMutationId } }`

	t.Run("success", func(t *testing.T) {
		versionID := identity.NewID()
		response := executePromoteInspectionVideo(t, mutation, ctx, func(context.Context, any) (any, error) {
			return database.OriginVersion{ID: versionID, MediaKind: "VIDEO", Status: "ACTIVE", VersionNumber: 2}, nil
		})
		if strings.Contains(response, `"errors"`) || !strings.Contains(response, versionID.String()) || !strings.Contains(response, `"preparationStatus":"READY"`) || !strings.Contains(response, `"selectable":true`) || !strings.Contains(response, `"sourceAvailability":"RETAINED"`) || !strings.Contains(response, `"userErrors":[]`) || !strings.Contains(response, `"clientMutationId":"promote-k1"`) {
			t.Fatalf("unexpected successful promotion response: %s", response)
		}
	})

	for _, tc := range []struct {
		name, code, field, message string
	}{
		{"business state", string(apperror.InvalidState), "preparation", "video preparation is not ready"},
		{"inaccessible inspection", string(apperror.NotFound), "inspectionId", "inspection not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := apperror.New(apperror.Code(tc.code), tc.field, tc.message)
			response := executePromoteInspectionVideo(t, mutation, ctx, func(context.Context, any) (any, error) { return nil, err })
			if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"version":null`) || !strings.Contains(response, `"code":"`+tc.code+`"`) || !strings.Contains(response, `"field":"`+tc.field+`"`) || !strings.Contains(response, tc.message) || !strings.Contains(response, `"clientMutationId":"promote-k1"`) {
				t.Fatalf("business failure did not use the mutation envelope: %s", response)
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
		{"internal", fmt.Errorf("database adapter failed: qa-secret-123"), "INTERNAL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := executePromoteInspectionVideo(t, mutation, ctx, func(context.Context, any) (any, error) { return nil, tc.err })
			if !strings.Contains(response, tc.code) || strings.Contains(response, `"version":{`) || strings.Contains(response, "qa-secret-123") {
				t.Fatalf("unexpected safe error response: %s", response)
			}
		})
	}
}

func executePromoteInspectionVideo(t *testing.T, query string, ctx context.Context, send func(context.Context, any) (any, error)) string {
	t.Helper()
	bus := mediator.New()
	if err := bus.RegisterCommand(originpromotevideo.Command{}, send); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(originlist.VersionDetailsQuery{}, func(_ context.Context, raw any) (any, error) {
		query := raw.(originlist.VersionDetailsQuery)
		status, availability := "READY", "RETAINED"
		return originlist.Result{Details: map[identity.ID]originlist.Details{query.VersionID: {PreparationStatus: &status, Selectable: true, SourceAvailability: &availability}}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{Bus: bus}}))
	server.SetErrorPresenter(graph.PresentError)
	request := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+fmt.Sprintf("%q", query)+`}`)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response.Body.String()
}
