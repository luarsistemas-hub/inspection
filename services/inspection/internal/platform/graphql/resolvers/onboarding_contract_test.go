package resolvers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	real_estate_catalog "inspection/services/inspection/internal/features/onboarding/real_estate_catalog"
	onboardingsession "inspection/services/inspection/internal/features/onboarding/session"
	"inspection/services/inspection/internal/platform/apperror"
	graph "inspection/services/inspection/internal/platform/graphql"

	"github.com/99designs/gqlgen/graphql/handler"
)

func TestIT031AndIT032OnboardingDefinitionGraphQLBoundary(t *testing.T) {
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{}}))
	server.SetErrorPresenter(graph.PresentError)

	request := func(segment string) string {
		w := httptest.NewRecorder()
		body := `{"query":"{ onboardingDefinition(segment:\"` + segment + `\"){ schemaVersion version segment originModes { key templateKey } steps { key fields { key label } } } }"}`
		req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		server.ServeHTTP(w, req.WithContext(context.Background()))
		return w.Body.String()
	}

	supported := request("REAL_ESTATE")
	if !strings.Contains(supported, `"schemaVersion":1`) || !strings.Contains(supported, "CHECKLIST_ONLY") || !strings.Contains(supported, "FIXED_ORIGIN") {
		t.Fatalf("supported definition response: %s", supported)
	}
	unsupported := request("UNKNOWN")
	if !strings.Contains(unsupported, "NOT_FOUND") || strings.Contains(unsupported, `"steps"`) {
		t.Fatalf("unsupported definition response: %s", unsupported)
	}
}

func TestOnboardingValidationPayloadProjectsActivationFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		field string
		code  apperror.Code
	}{
		{name: "invalid code", err: apperror.New(apperror.InvalidInput, "code", "invalid code"), field: "code", code: apperror.InvalidInput},
		{name: "rate limited", err: apperror.New(apperror.RateLimited, "", "retry later"), code: apperror.RateLimited},
		{name: "expired session", err: apperror.New(apperror.SessionExpired, "session", "session expired"), field: "session", code: apperror.SessionExpired},
		{name: "conflict", err: apperror.New(apperror.Conflict, "activation", "activation is already in progress"), field: "activation", code: apperror.Conflict},
		{name: "password policy", err: apperror.New(apperror.InvalidInput, "password", "password does not meet policy"), field: "password", code: apperror.InvalidInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := onboardingValidationPayload(tc.err, "mutation-1")
			if err != nil || payload == nil || len(payload.UserErrors) != 1 {
				t.Fatalf("failure was not projected: payload=%#v err=%v", payload, err)
			}
			got := payload.UserErrors[0]
			if got.Code != string(tc.code) || got.Message != tc.err.Error() || payload.ClientMutationID != "mutation-1" {
				t.Fatalf("unexpected user error: %#v", got)
			}
			if tc.field == "" && got.Field != nil || tc.field != "" && (got.Field == nil || *got.Field != tc.field) {
				t.Fatalf("unexpected field: %#v", got.Field)
			}
		})
	}
}

func TestOnboardingSessionWithoutCookieReturnsNull(t *testing.T) {
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{}}))
	server.SetErrorPresenter(graph.PresentError)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":"{ onboardingSession { id } }"}`))
	req.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(w, req.WithContext(context.Background()))

	response := w.Body.String()
	if !strings.Contains(response, `"onboardingSession":null`) || strings.Contains(response, `"errors"`) {
		t.Fatalf("anonymous onboarding session response: %s", response)
	}
}

func TestMapOnboardingSessionIncludesVersionedDefinition(t *testing.T) {
	value := mapOnboardingSession(onboardingsession.Session{State: "IDENTITY_VERIFIED", CurrentStep: "AGENCY", Version: 2})

	if value == nil || value.Definition["schemaVersion"] != 1 || value.Definition["version"] != 1 {
		t.Fatalf("session definition versions: %#v", value.Definition)
	}
	if value.Definition["segment"] != "REAL_ESTATE" || value.Definition["segmentVersion"] != "real-estate-v1" {
		t.Fatalf("session definition segment: %#v", value.Definition)
	}
	if len(value.Definition["steps"].([]real_estate_catalog.Step)) == 0 || len(value.Definition["templates"].([]string)) != 2 {
		t.Fatalf("session definition content: %#v", value.Definition)
	}
}

func TestOnboardingAgencyProvisioningKeyIsStablePerSessionVersion(t *testing.T) {
	first := onboardingAgencyProvisioningKey("session-token", 2)
	if first != onboardingAgencyProvisioningKey("session-token", 2) {
		t.Fatal("same onboarding session/version must share the provisioning key")
	}
	if first == onboardingAgencyProvisioningKey("session-token", 3) {
		t.Fatal("different checkpoint versions must not share the provisioning key")
	}
	if first == onboardingAgencyProvisioningKey("other-session-token", 2) {
		t.Fatal("different onboarding sessions must not share the provisioning key")
	}
}
