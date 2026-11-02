package resolvers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
)

func TestREO_UT022ParticipantCredentialIsForbiddenFromAdminResolver(t *testing.T) {
	tenantID := identity.NewID()
	responsibilityID := identity.NewID()
	invitations, credentials := captureSessionFixture(t, tenantID, responsibilityID)
	if err := invitations.DB.Exec("ATTACH DATABASE ':memory:' AS tenancy").Error; err != nil {
		t.Fatal(err)
	}
	if err := invitations.DB.AutoMigrate(&database.Tenant{}); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
		t.Fatal(err)
	}
	if err := invitations.DB.Create(&database.Tenant{ID: tenantID, TenantID: tenantID, Name: "Imobiliária REO", Language: "pt-BR", DefaultTimezone: "America/Sao_Paulo", Status: "ACTIVE"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := invitations.DB.Exec("ATTACH DATABASE ':memory:' AS inspections").Error; err != nil {
		t.Fatal(err)
	}
	if err := invitations.DB.AutoMigrate(&database.Responsibility{}); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
		t.Fatal(err)
	}
	if err := invitations.DB.Create(&database.Responsibility{ID: responsibilityID, TenantID: tenantID, InspectionID: identity.NewID(), ParticipantID: identity.NewID(), Status: "PENDING", Version: 1}).Error; err != nil {
		t.Fatalf("persist responsibility for external session: %v", err)
	}
	if _, err := invitations.ValidateSession(context.Background(), credentials.SessionToken, credentials.CSRFToken); err != nil {
		t.Fatalf("fixture did not create a persisted valid responsibility session: %v", err)
	}
	principal := requestctx.Principal{IdentityID: identity.NewID(), MembershipID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.TenantAdmin}, Product: auth.AdminProduct, ProductEntitlements: []string{auth.AdminProduct}}
	metadata := requestctx.Metadata{TenantID: tenantID, Principal: principal}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: invitations.DB, Authorizer: auth.Authorizer{}, Invitations: invitations}}))
	server.SetErrorPresenter(graph.PresentError)
	query := `{"query":"{ tenant { id name } }"}`
	request := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(query))
	request.Header.Set("Content-Type", "application/json")
	ctx := requestctx.WithExternalCredentials(requestctx.WithMetadata(context.Background(), metadata), credentials)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request.WithContext(ctx))
	var denied struct {
		Data struct {
			Tenant *database.Tenant `json:"tenant"`
		} `json:"data"`
		Errors []struct {
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &denied); err != nil {
		t.Fatalf("decode forbidden response: %v: %s", err, response.Body.String())
	}
	if len(denied.Errors) == 0 || denied.Errors[0].Extensions.Code != "FORBIDDEN" || denied.Data.Tenant != nil {
		t.Fatalf("external participant credential was not forbidden with null tenant: %s", response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(query))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(requestctx.WithMetadata(context.Background(), metadata))
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	var allowed struct {
		Data struct {
			Tenant *database.Tenant `json:"tenant"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &allowed); err != nil {
		t.Fatalf("decode positive response: %v: %s", err, response.Body.String())
	}
	if len(allowed.Errors) != 0 || allowed.Data.Tenant == nil || allowed.Data.Tenant.ID != tenantID {
		t.Fatalf("Admin tenant access without external credential did not pass: %s", response.Body.String())
	}
}

func TestREO_UT026CompleteOnboardingHidesInternalFailureFromGraphQL(t *testing.T) {
	resolver := &Resolver{}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: resolver}))
	server.SetErrorPresenter(graph.PresentError)
	request := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":"mutation { completeOnboarding(input:{clientMutationId:\"reo-ut026\"}) { userErrors { code message } clientMutationId } }"}`))
	request.Header.Set("Content-Type", "application/json")
	ctx := requestctx.WithOnboardingCredentials(context.Background(), requestctx.OnboardingCredentials{SessionToken: "verified-session", CSRFToken: "verified-csrf"})
	request = request.WithContext(ctx)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	body := response.Body.String()
	if !strings.Contains(body, `"code":"INTERNAL"`) || !strings.Contains(body, "Não foi possível concluir a operação.") || strings.Contains(body, "missing dependency") {
		t.Fatalf("internal onboarding failure leaked through GraphQL: %s", body)
	}
}
