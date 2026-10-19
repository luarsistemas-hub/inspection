package resolvers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"inspection/libs/identity"
	reanalyzevideoinsp "inspection/services/inspection/internal/features/analysis/reanalyze_video_inspection"
	"inspection/services/inspection/internal/platform/apperror"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
)

func TestReanalyzeVideoInspectionGraphQLEnvelope(t *testing.T) {
	tenantID, inspectionID, planID := identity.NewID(), identity.NewID(), identity.NewID()
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, CorrelationID: "reanalyze-transport"})
	mutation := `mutation { reanalyzeVideoInspection(input:{inspectionId:"` + inspectionID.String() + `",expectedPlanId:"` + planID.String() + `",clientMutationId:"reanalyze-k1"}) { planId generation userErrors { code field message } clientMutationId } }`
	createdPlan := identity.NewID()
	response := executeReanalyzeVideoInspection(t, mutation, ctx, func(context.Context, any) (any, error) {
		return reanalyzevideoinsp.Result{PlanID: createdPlan, Generation: 2}, nil
	})
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, createdPlan.String()) || !strings.Contains(response, `"generation":2`) || !strings.Contains(response, `"userErrors":[]`) || !strings.Contains(response, `"clientMutationId":"reanalyze-k1"`) {
		t.Fatalf("unexpected successful response: %s", response)
	}

	for _, tc := range []struct {
		name string
		code apperror.Code
	}{
		{"stale active plan", apperror.Conflict},
		{"active generation", apperror.InvalidState},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := executeReanalyzeVideoInspection(t, mutation, ctx, func(context.Context, any) (any, error) {
				return nil, apperror.New(tc.code, "expectedPlanId", "video plan cannot be reanalyzed")
			})
			if strings.Contains(body, `"errors"`) || !strings.Contains(body, `"planId":null`) || !strings.Contains(body, `"generation":null`) || !strings.Contains(body, `"code":"`+string(tc.code)+`"`) {
				t.Fatalf("business error did not use null result and userErrors: %s", body)
			}
		})
	}
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"forbidden", apperror.New(apperror.Forbidden, "", "access denied"), "FORBIDDEN"},
		{"foreign inspection", apperror.New(apperror.NotFound, "inspectionId", "inspection not found"), "NOT_FOUND"},
		{"dependency unavailable", apperror.New(apperror.DependencyUnavailable, "database", "database unavailable"), "DEPENDENCY_UNAVAILABLE"},
		{"internal adapter failure", fmt.Errorf("database adapter failed: qa-secret-123"), "INTERNAL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := executeReanalyzeVideoInspection(t, mutation, ctx, func(context.Context, any) (any, error) { return nil, tc.err })
			if !strings.Contains(body, tc.code) || strings.Contains(body, `"planId":"`) || strings.Contains(body, "qa-secret-123") {
				t.Fatalf("unexpected protected failure response: %s", body)
			}
		})
	}
	unauthenticated := executeReanalyzeVideoInspection(t, mutation, context.Background(), func(context.Context, any) (any, error) {
		t.Fatal("unauthenticated request reached the command bus")
		return nil, nil
	})
	if !strings.Contains(unauthenticated, "UNAUTHENTICATED") || strings.Contains(unauthenticated, `"reanalyzeVideoInspection":{`) {
		t.Fatalf("unauthenticated mutation was not rejected: %s", unauthenticated)
	}
}

func executeReanalyzeVideoInspection(t *testing.T, query string, ctx context.Context, send func(context.Context, any) (any, error)) string {
	t.Helper()
	bus := mediator.New()
	if err := bus.RegisterCommand(reanalyzevideoinsp.Command{}, send); err != nil {
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
