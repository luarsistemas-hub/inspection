//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	capturebeginvideo "inspection/services/inspection/internal/features/capture/begin_video_capture"
	capturebootstrap "inspection/services/inspection/internal/features/capture/bootstrap"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	capturereferences "inspection/services/inspection/internal/features/capture/reference_items"
	acceptprocessing "inspection/services/inspection/internal/features/invitations/accept_processing"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/features/media/configure_video_policy"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"
	"inspection/services/inspection/internal/platform/testfailure"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

func TestAPI03DependencyFailuresIT402(t *testing.T) {
	for _, operation := range api03FailureOperations() {
		t.Run(operation.name, func(t *testing.T) {
			fixture := newAPI03FailureFixture(t, operation.name)
			response := executeAPI03Failure(t, fixture, operation, "dependency")
			if !strings.Contains(response, "DEPENDENCY_UNAVAILABLE") || strings.Contains(response, operation.successPayload) {
				t.Fatalf("%s did not return DEPENDENCY_UNAVAILABLE without success data: %s", operation.name, response)
			}
		})
	}
}

func TestAPI03InternalFailuresIT403(t *testing.T) {
	for _, operation := range api03FailureOperations() {
		t.Run(operation.name, func(t *testing.T) {
			fixture := newAPI03FailureFixture(t, operation.name)
			response := executeAPI03Failure(t, fixture, operation, "internal")
			if !strings.Contains(response, "INTERNAL") || strings.Contains(response, operation.successPayload) || strings.Contains(response, testfailure.Secret) {
				t.Fatalf("%s did not hide its internal error and return no success data: %s", operation.name, response)
			}
		})
	}
}

type api03FailureFixture struct {
	server           http.Handler
	bus              *mediator.Bus
	tenantID         identity.ID
	identityID       identity.ID
	membershipID     identity.ID
	responsibilityID identity.ID
	invitationID     identity.ID
	sessionID        identity.ID
	harness          *harness.Harness
	credentials      requestctx.ExternalCredentials
}

type api03FailureOperation struct {
	name, query, successPayload string
}

func api03FailureOperations() []api03FailureOperation {
	return []api03FailureOperation{
		{name: "externalCapture", query: `query { externalCapture { status mediaKind } }`, successPayload: `"externalCapture":{`},
		{name: "acceptProcessing", query: `mutation { acceptProcessing(input:{disclosureVersion:"pt-BR-1",photoProcessing:true,videoProcessing:true,aiAnalysis:true,gpsUse:true,clientMutationId:"api03-failure"}) { status } }`, successPayload: `"acceptProcessing":{`},
		{name: "beginVideoCapture", query: `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"api03-failure"}) { attempt { id } } }`, successPayload: `"attempt":{`},
	}
}

func newAPI03FailureFixture(t *testing.T, operation string) api03FailureFixture {
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
	seed, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		updates := map[string]any{"media_kind": "VIDEO"}
		if operation != "externalCapture" {
			updates["status"] = "OPEN"
		}
		return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", seed.TenantID, seed.ResponsibilityID).Updates(updates).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.VideoAdmission{}).Where("id=?", 1).Updates(map[string]any{"enabled": true, "version": 1}).Error; err != nil {
		t.Fatal(err)
	}
	var admission database.VideoAdmission
	if err := h.AdminDB.Where("id=?", 1).First(&admission).Error; err != nil {
		if err := h.AdminDB.Create(&database.VideoAdmission{ID: 1, Enabled: true, Version: 1, ChangedAt: time.Now().UTC()}).Error; err != nil {
			t.Fatal(err)
		}
	}

	pepper := []byte(strings.Repeat("p", 32))
	token, err := security.NewScopedToken(seed.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "api03-csrf-proof"
	sessionHash := security.HashToken(token)
	csrfProof, err := security.CSRFProof(sessionHash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	invitationID := identity.NewID()
	invitation := database.Invitation{ID: invitationID, TenantID: seed.TenantID, ResponsibilityID: seed.ResponsibilityID, TokenHash: sessionHash[:], DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	session := database.ExternalSession{ID: identity.NewID(), TenantID: seed.TenantID, InvitationID: invitationID, ResponsibilityID: seed.ResponsibilityID, SessionDigest: sessionHash[:], CSRFDigest: csrfProof[:], ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := h.AdminDB.Create(&invitation).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	acceptance := database.ProcessingAcceptance{ID: identity.NewID(), TenantID: seed.TenantID, ResponsibilityID: seed.ResponsibilityID, DisclosureVersion: invitationcore.PrivacyDisclosureVersion, PhotoProcessing: true, VideoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}
	if err := h.AdminDB.Create(&acceptance).Error; err != nil {
		t.Fatal(err)
	}

	if err := testfailure.Install(h.DB); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	invitations := invitationcore.Service{DB: h.DB, Pepper: pepper}
	if err := capturebootstrap.Setup(capturebootstrap.Dependencies{Bus: bus, Service: capturecore.Service{DB: h.DB}}); err != nil {
		t.Fatal(err)
	}
	if err := capturereferences.Setup(capturereferences.Dependencies{Bus: bus, DB: h.DB, Store: h.Store}); err != nil {
		t.Fatal(err)
	}
	if err := acceptprocessing.Setup(acceptprocessing.Dependencies{Bus: bus, Service: invitations}); err != nil {
		t.Fatal(err)
	}
	if err := capturebeginvideo.Setup(capturebeginvideo.Dependencies{DB: h.DB, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	if err := configure_video_policy.Setup(configure_video_policy.Dependencies{DB: h.DB, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Bus: bus, Invitations: invitations}}))
	server.SetErrorPresenter(graph.PresentError)
	return api03FailureFixture{server: server, bus: bus, tenantID: seed.TenantID, identityID: seed.IdentityID, membershipID: seed.MembershipID, responsibilityID: seed.ResponsibilityID, invitationID: invitationID, sessionID: session.ID, harness: h, credentials: requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf}}
}

func executeAPI03Failure(t *testing.T, fixture api03FailureFixture, operation api03FailureOperation, mode string) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+quote(operation.query)+`}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(testfailure.HeaderOperation, operation.name)
	req.Header.Set(testfailure.HeaderFailure, mode)
	ctx := requestctx.WithExternalCredentials(req.Context(), fixture.credentials)
	req = testfailure.WithRequest(req.WithContext(ctx), "test")
	fixture.server.ServeHTTP(w, req)
	return w.Body.String()
}
