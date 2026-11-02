//go:build integration

package resolvers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	verifyotp "inspection/services/inspection/internal/features/invitations/verify_otp"
	deliverystatus "inspection/services/inspection/internal/features/onboarding/delivery_status"
	onboardingsession "inspection/services/inspection/internal/features/onboarding/session"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/ratelimit"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"
	"inspection/services/inspection/internal/platform/tenanttx"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

type reoAllowOTPAttempts struct{}

func (reoAllowOTPAttempts) AllowSend(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}
func (reoAllowOTPAttempts) AllowResend(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}
func (reoAllowOTPAttempts) AllowAttempt(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}

func TestREO_IT_016DelegateOTPResultCannotReadAdminOnboardingData(t *testing.T) {
	fixture := newAPI03FailureFixture(t, "externalCapture")
	pepper := []byte(strings.Repeat("p", 32))
	code := "654321"
	codeHMAC, err := security.HashOTP(code, pepper)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := fixture.harness.AdminDB.Model(&database.Invitation{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.invitationID).Update("delivery_intents", []byte(`[{"channel":"EMAIL","destination":"delegate@example.test","verified":true}]`)).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.AdminDB.Create(&database.OTPChallenge{ID: identity.NewID(), TenantID: fixture.tenantID, InvitationID: fixture.invitationID, CodeHMAC: codeHMAC[:], Attempts: 0, SendCount: 1, LastSentAt: now, ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	bus := fixture.bus
	invitations := invitationcore.Service{DB: fixture.harness.DB, Pepper: pepper, Limits: reoAllowOTPAttempts{}}
	if err := verifyotp.Setup(verifyotp.Dependencies{Bus: bus, Service: invitations}); err != nil {
		t.Fatal(err)
	}
	ownerLocator, ownerCSRF := "reo-owner-locator-"+identity.NewID().String(), "reo-owner-csrf-"+identity.NewID().String()
	ownerPepper := []byte(strings.Repeat("o", 32))
	ownerDigest := sha256.Sum256([]byte(ownerLocator))
	ownerProof, err := security.CSRFProof(ownerDigest, ownerCSRF, ownerPepper)
	if err != nil {
		t.Fatal(err)
	}
	ownerNow, ownerSessionID, ownerRequestID := time.Now().UTC(), identity.NewID(), identity.NewID()
	ownerSession := database.OnboardingSession{ID: ownerSessionID, TenantID: &fixture.tenantID, Email: "reo-owner@example.test", OwnerName: "Responsável", OwnerSubject: "reo-owner", SessionLocatorDigest: ownerDigest[:], State: onboardingsession.StateVerified, CurrentStep: onboardingsession.StepReady, DefinitionVersion: 4, Version: 4, ExpiresAt: ownerNow.Add(time.Hour), CSRFDigest: ownerProof[:], CreatedAt: ownerNow, UpdatedAt: ownerNow}
	if err := fixture.harness.AdminDB.Create(&ownerSession).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.AdminDB.Create(&database.OnboardingRequest{ID: ownerRequestID, TenantID: fixture.tenantID, SessionID: ownerSessionID, Status: "SUBMITTED", IdempotencyKey: "reo-it016-owner", CreatedAt: ownerNow, UpdatedAt: ownerNow}).Error; err != nil {
		t.Fatal(err)
	}
	statusService := deliverystatus.Service{DB: fixture.harness.DB}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{Bus: bus, DB: fixture.harness.DB, Invitations: invitations, Onboarding: onboardingsession.Service{DB: fixture.harness.DB, Pepper: ownerPepper}, OnboardingDeliveryStatus: statusService}}))
	server.SetErrorPresenter(graph.PresentError)
	mutation := `mutation { verifyInvitationOtp(input:{linkToken:"` + fixture.credentials.SessionToken + `",code:"` + code + `",clientMutationId:"reo-it016"}) { status csrfToken } }`
	mutationBody, _ := json.Marshal(map[string]string{"query": mutation})
	mutationResponse := httptest.NewRecorder()
	mutationRequest := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(string(mutationBody)))
	mutationRequest.Header.Set("Content-Type", "application/json")
	mutationRequest = mutationRequest.WithContext(requestctx.WithResponseWriter(mutationRequest.Context(), mutationResponse))
	server.ServeHTTP(mutationResponse, mutationRequest)
	if mutationResponse.Code != http.StatusOK || !strings.Contains(mutationResponse.Body.String(), `"status":"ACTIVE"`) {
		t.Fatalf("delegate OTP mutation did not establish an external session: %s", mutationResponse.Body.String())
	}
	var mutationPayload struct {
		Data struct {
			VerifyInvitationOTP struct {
				CSRFToken string `json:"csrfToken"`
			} `json:"verifyInvitationOtp"`
		} `json:"data"`
	}
	if err := json.Unmarshal(mutationResponse.Body.Bytes(), &mutationPayload); err != nil {
		t.Fatal(err)
	}
	cookies := mutationResponse.Result().Cookies()
	var sessionCookie string
	for _, cookie := range cookies {
		if cookie.Name == "inspection_external" {
			sessionCookie = cookie.Value
		}
	}
	if sessionCookie == "" || mutationPayload.Data.VerifyInvitationOTP.CSRFToken == "" {
		t.Fatalf("delegate OTP response omitted its session credential: body=%s cookies=%v", mutationResponse.Body.String(), cookies)
	}
	queryResponse := httptest.NewRecorder()
	ownerRequest := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":"query { onboardingStatus { state requestId inspectionId nextAction originStatus deliveryStatus } }"}`))
	ownerRequest.Header.Set("Content-Type", "application/json")
	ownerRequest = ownerRequest.WithContext(requestctx.WithOnboardingCredentials(ownerRequest.Context(), requestctx.OnboardingCredentials{SessionToken: ownerLocator, CSRFToken: ownerCSRF}))
	server.ServeHTTP(queryResponse, ownerRequest)
	ownerBody := queryResponse.Body.String()
	if queryResponse.Code != http.StatusOK || !strings.Contains(ownerBody, `"requestId":"`+ownerRequestID.String()+`"`) || !strings.Contains(ownerBody, `"state":"SUBMITTED"`) || !strings.Contains(ownerBody, `"originStatus":"NOT_REQUIRED"`) || !strings.Contains(ownerBody, `"deliveryStatus":"NOT_STARTED"`) {
		t.Fatalf("owner cookie did not receive expected onboarding status: %s", ownerBody)
	}
	queryResponse = httptest.NewRecorder()
	queryRequest := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":"query { onboardingStatus { state requestId inspectionId responsibleEmail } }"}`))
	queryRequest.Header.Set("Content-Type", "application/json")
	queryContext := requestctx.WithExternalCredentials(queryRequest.Context(), requestctx.ExternalCredentials{SessionToken: sessionCookie, CSRFToken: mutationPayload.Data.VerifyInvitationOTP.CSRFToken})
	server.ServeHTTP(queryResponse, queryRequest.WithContext(queryContext))
	responseBody := queryResponse.Body.String()
	if queryResponse.Code != http.StatusOK || !strings.Contains(responseBody, `"onboardingStatus":null`) {
		t.Fatalf("external delegate credential received Admin onboarding fields: %s", responseBody)
	}
	queryResponse = httptest.NewRecorder()
	captureRequest := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":"query { externalCapture { responsibilityId } }"}`))
	captureRequest.Header.Set("Content-Type", "application/json")
	captureRequest = captureRequest.WithContext(requestctx.WithExternalCredentials(captureRequest.Context(), requestctx.ExternalCredentials{SessionToken: sessionCookie, CSRFToken: mutationPayload.Data.VerifyInvitationOTP.CSRFToken}))
	server.ServeHTTP(queryResponse, captureRequest)
	if queryResponse.Code != http.StatusOK || !strings.Contains(queryResponse.Body.String(), `"responsibilityId":"`+fixture.responsibilityID.String()+`"`) {
		t.Fatalf("delegate did not receive its externalCapture responsibility: %s", queryResponse.Body.String())
	}
	otherTenant := identity.NewID()
	if err := fixture.harness.AdminDB.Create(&database.Tenant{ID: otherTenant, TenantID: otherTenant, Name: "REO foreign tenant", Language: "pt-BR", DefaultTimezone: "America/Sao_Paulo", Status: "ACTIVE"}).Error; err != nil {
		t.Fatal(err)
	}
	var visibleRequests int64
	if err := (tenanttx.Runner{DB: fixture.harness.DB}).Within(context.Background(), otherTenant, func(tx *gorm.DB) error {
		return tx.Table("onboarding.requests").Count(&visibleRequests).Error
	}); err != nil {
		t.Fatal(err)
	}
	if visibleRequests != 0 {
		t.Fatalf("RLS exposed onboarding rows under another tenant context: %d", visibleRequests)
	}
}
