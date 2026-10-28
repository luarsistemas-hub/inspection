//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"errors"
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
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	inventorydecide "inspection/services/inspection/internal/features/inventory/decide_items"
	acceptprocessing "inspection/services/inspection/internal/features/invitations/accept_processing"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/features/media/configure_video_policy"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
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

func TestIT045DisabledVideoAdmissionRejectsEnvironmentClip(t *testing.T) {
	fixture := newAPI03FailureFixture(t, "beginVideoCapture")
	requirementKey := "env:" + identity.NewID().String()
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		requirements, _ := json.Marshal([]map[string]any{{"key": requirementKey, "evidenceKind": "VIDEO", "required": true, "expected": true, "minimumMedia": 1, "maximumMedia": 3}})
		return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Updates(map[string]any{"requirements": requirements, "media_kind": "VIDEO", "status": "OPEN"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.AdminDB.Model(&database.VideoAdmission{}).Where("id=?", 1).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	query := `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",requirementKey:"` + requirementKey + `",clientMutationId:"it045-env-disabled"}) { attempt { id } } }`
	response := executeCaptureGraphQL(t, fixture.server, query, fixture.tenantID, fixture.credentials, true)
	if !strings.Contains(response, `"code":"INTEGRATION_DISABLED"`) || strings.Contains(response, `"attempt":{`) {
		t.Fatalf("IT-045 disabled admission response=%s", response)
	}
}

func TestIT043CanceledInspectionMakesCaptureBootstrapUnavailable(t *testing.T) {
	fixture := newAPI03FailureFixture(t, "externalCapture")
	var responsibility database.Responsibility
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.responsibilityID).Take(&responsibility).Error; err != nil {
			return err
		}
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.tenantID, responsibility.InspectionID).Update("status", "CANCELED").Error
	}); err != nil {
		t.Fatal(err)
	}
	response := executeCaptureGraphQL(t, fixture.server, `query { externalCapture { status captureLayout } }`, fixture.tenantID, fixture.credentials, true)
	if !strings.Contains(response, `"code":"INVALID_STATE"`) || strings.Contains(response, `"externalCapture":{`) {
		t.Fatalf("IT-043 canceled inspection bootstrap response=%s", response)
	}
}

func TestUT134DisabledReviewerKeepsCaptureHandoffButCannotDecideInventory(t *testing.T) {
	fixture := newAPI03FailureFixture(t, "externalCapture")
	now := time.Now().UTC()
	reviewerID := identity.NewID()
	reviewerIdentityID := identity.NewID()
	originID, versionID, environmentID, itemID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	var assetID identity.ID
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		var asset database.Asset
		if err := tx.Where("tenant_id=?", fixture.tenantID).Take(&asset).Error; err != nil {
			return err
		}
		assetID = asset.ID
		if err := tx.Create(&database.Membership{ID: reviewerID, TenantID: fixture.tenantID, IdentityID: reviewerIdentityID, Issuer: "ut134", Subject: reviewerID.String(), Role: "EMPLOYEE", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ResourceScope{ID: identity.NewID(), TenantID: fixture.tenantID, MembershipID: reviewerID, Kind: "ASSET", ResourceID: assetID, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.Invitation{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.invitationID).Updates(map[string]any{"reviewer_membership_id": reviewerID, "status": "COMPLETED", "revoked_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.ExternalSession{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.sessionID).Update("revoked_at", now).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Updates(map[string]any{"status": "SUBMITTED", "version": 2}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.tenantID, AssetID: assetID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		purpose, draftState := "MOVE_IN", "READY"
		if err := tx.Create(&database.OriginVersion{ID: versionID, TenantID: fixture.tenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: identity.Ptr(fixture.responsibilityID), Purpose: &purpose, HasInventory: true, DraftState: &draftState, Status: "DRAFT", MediaKind: "PHOTO", IdempotencyKey: "ut134-version", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.InventoryEnvironment{ID: environmentID, TenantID: fixture.tenantID, OriginVersionID: versionID, IdentityID: identity.NewID(), TypeCode: "BEDROOM", Name: "Quarto", Expected: true, Decision: "ACCEPTED", Position: 1, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.InventoryItem{ID: itemID, TenantID: fixture.tenantID, OriginVersionID: versionID, EnvironmentID: environmentID, IdentityID: identity.NewID(), TypeCode: "BED", Name: "Cama", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Membership{}).Where("tenant_id=? AND id=?", fixture.tenantID, reviewerID).Update("status", "DISABLED").Error
	}); err != nil {
		t.Fatal(err)
	}
	response := executeCaptureGraphQL(t, fixture.server, `query { externalCapture { reviewHandoff { inspectionId originVersionId } } }`, fixture.tenantID, fixture.credentials, true)
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"reviewHandoff":{"inspectionId":"`) || !strings.Contains(response, `"originVersionId":"`+versionID.String()+`"`) {
		t.Fatalf("UT-122 completed capture did not return its draft handoff: %s", response)
	}
	if err := inventorydecide.Setup(fixture.bus, inventorycore.Service{DB: fixture.harness.DB, Within: fixture.harness.WithinTenant}); err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fixture.tenantID, Principal: requestctx.Principal{IdentityID: reviewerIdentityID, MembershipID: reviewerID, TenantID: fixture.tenantID, Roles: []string{auth.Employee}, Scopes: []requestctx.Scope{{Kind: "ASSET", ID: assetID}}}})
	resolver := &Resolver{DB: fixture.harness.DB, Bus: fixture.bus, Authorizer: auth.Authorizer{Store: auth.GORMMembershipStore{DB: fixture.harness.DB}}}
	payload, err := resolver.Mutation().DecideInventoryItems(ctx, graph.DecideInventoryItemsInput{OriginVersionID: versionID.String(), Decisions: []*graph.InventoryDecisionInput{{ItemID: itemID.String(), Action: "ACCEPT", ExpectedVersion: 1}}, ClientMutationID: "ut134-disabled-reviewer"})
	if err != nil {
		var appErr *apperror.Error
		if errors.As(err, &appErr) && appErr.Code == apperror.Forbidden {
			return
		}
		t.Fatalf("decideInventoryItems top-level error=%v", err)
	}
	if payload == nil || len(payload.UserErrors) != 1 || payload.UserErrors[0].Code != string(apperror.Forbidden) {
		t.Fatalf("decideInventoryItems as disabled reviewer=%+v, want FORBIDDEN", payload)
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
	return newAPI03FailureFixtureWithOptions(t, operation, harness.Task06FixtureOptions{})
}

func newAPI03FailureFixtureWithOptions(t *testing.T, operation string, options harness.Task06FixtureOptions) api03FailureFixture {
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
	seed, err := h.SeedTask06Fixture(ctx, options)
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
	session := database.ExternalSession{ID: identity.NewID(), TenantID: seed.TenantID, InvitationID: identity.Ptr(invitationID), ResponsibilityID: &seed.ResponsibilityID, SessionDigest: sessionHash[:], CSRFDigest: csrfProof[:], ExpiresAt: now.Add(time.Hour), CreatedAt: now}
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
