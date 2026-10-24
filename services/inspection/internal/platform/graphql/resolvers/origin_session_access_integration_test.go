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
	capturebootstrap "inspection/services/inspection/internal/features/capture/bootstrap"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	capturereferences "inspection/services/inspection/internal/features/capture/reference_items"
	acceptprocessing "inspection/services/inspection/internal/features/invitations/accept_processing"
	"inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/features/media/configure_video_policy"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

func TestOriginExternalCaptureRejectsExpiredRevokedAndWrongParticipantSessionsIT037(t *testing.T) {
	for _, scenario := range []struct {
		name, want string
	}{
		{name: "expired", want: "SESSION_EXPIRED"},
		{name: "revoked", want: "SESSION_EXPIRED"},
		{name: "wrong participant", want: "NOT_FOUND"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := newOriginSessionAccessFixture(t, scenario.name)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":"query { externalCapture { kind mediaKind } }"}`))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(requestctx.WithExternalCredentials(req.Context(), fixture.credentials))
			fixture.server.ServeHTTP(w, req)
			if !strings.Contains(w.Body.String(), scenario.want) || strings.Contains(w.Body.String(), `"externalCapture":{`) {
				t.Fatalf("session boundary returned unexpected response: %s", w.Body.String())
			}
		})
	}
}

func TestVV_IT_031OriginConsentUnlocksCaptureWithFifteenMinuteLimit(t *testing.T) {
	fixture := newOriginSessionAccessFixture(t, "active consent")
	if err := fixture.harness.AdminDB.Exec("INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, TRUE, 1, ?, ?, ?, '') ON CONFLICT (id) DO UPDATE SET enabled=TRUE", time.Now().UTC(), "isolated VV IT-031 fixture", "vv-it031").Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).
			Updates(map[string]any{"kind": "ORIGIN", "status": "OPEN", "requirements": json.RawMessage(`[{"key":"video:main","section":"vistoria","label":"Vídeo de origem","evidenceKind":"VIDEO","required":true,"minimumMedia":1,"maximumMedia":1,"captureSourcePolicy":"CAMERA_ONLY","comparisonTarget":"FIXED_ORIGIN"}]`)}).Error
	}); err != nil {
		t.Fatal(err)
	}
	accept := executeCaptureGraphQL(t, fixture.server, `mutation { acceptProcessing(input:{disclosureVersion:"pt-BR-1",photoProcessing:true,videoProcessing:true,aiAnalysis:true,gpsUse:true,clientMutationId:"vv-it031-consent"}) { status userErrors { code } } }`, fixture.tenantID, fixture.credentials, true)
	if strings.Contains(accept, `"errors"`) || !strings.Contains(accept, `"status":"ACCEPTED"`) {
		t.Fatalf("origin video disclosure was not accepted: %s", accept)
	}
	response := executeCaptureGraphQL(t, fixture.server, `query { externalCapture { kind mediaKind policy } }`, fixture.tenantID, fixture.credentials, true)
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"kind":"ORIGIN"`) || !strings.Contains(response, `"mediaKind":"VIDEO"`) || !strings.Contains(response, `"maxDurationSeconds":900`) {
		t.Fatalf("authenticated origin capture did not expose the 15-minute video limit: %s", response)
	}
}

type originSessionAccessFixture struct {
	server           http.Handler
	credentials      requestctx.ExternalCredentials
	harness          *harness.Harness
	tenantID         identity.ID
	responsibilityID identity.ID
}

func newOriginSessionAccessFixture(t *testing.T, scenario string) originSessionAccessFixture {
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
	now := time.Now().UTC()
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", seed.TenantID, seed.ResponsibilityID).
			Updates(map[string]any{"kind": "ORIGIN", "media_kind": "VIDEO"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	pepper := []byte(strings.Repeat("o", 32))
	token, err := security.NewScopedToken(seed.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "it037-csrf"
	hash := security.HashToken(token)
	proof, err := security.CSRFProof(hash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	invitationID := identity.NewID()
	responsibilityID := seed.ResponsibilityID
	expires := now.Add(time.Hour)
	revokedAt := (*time.Time)(nil)
	switch scenario {
	case "expired":
		expires = now.Add(-time.Minute)
	case "revoked":
		revokedAt = &now
	case "wrong participant":
		responsibilityID = identity.NewID()
	}
	invitation := database.Invitation{ID: invitationID, TenantID: seed.TenantID, ResponsibilityID: seed.ResponsibilityID, TokenHash: hash[:], DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: expires, CreatedAt: now}
	if err := h.AdminDB.Create(&invitation).Error; err != nil {
		t.Fatal(err)
	}
	session := database.ExternalSession{ID: identity.NewID(), TenantID: seed.TenantID, InvitationID: identity.Ptr(invitationID), ResponsibilityID: &responsibilityID, SessionDigest: hash[:], CSRFDigest: proof[:], ExpiresAt: expires, RevokedAt: revokedAt, CreatedAt: now}
	if err := h.AdminDB.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	invitations := core.Service{DB: h.DB, Pepper: pepper}
	bus := mediator.New()
	if err := capturebootstrap.Setup(capturebootstrap.Dependencies{Bus: bus, Service: capturecore.Service{DB: h.DB}}); err != nil {
		t.Fatal(err)
	}
	if err := capturereferences.Setup(capturereferences.Dependencies{Bus: bus, DB: h.DB, Store: h.Store}); err != nil {
		t.Fatal(err)
	}
	if err := configure_video_policy.Setup(configure_video_policy.Dependencies{DB: h.DB, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	if err := acceptprocessing.Setup(acceptprocessing.Dependencies{Bus: bus, Service: invitations}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Bus: bus, Invitations: invitations}}))
	server.SetErrorPresenter(graph.PresentError)
	return originSessionAccessFixture{server: server, credentials: requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf}, harness: h, tenantID: seed.TenantID, responsibilityID: seed.ResponsibilityID}
}
