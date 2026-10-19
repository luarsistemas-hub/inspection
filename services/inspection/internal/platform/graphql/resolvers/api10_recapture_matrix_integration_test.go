//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	recapturerequest "inspection/services/inspection/internal/features/recapture/request"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/testfailure"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

type api10Fixture struct {
	fixedOriginGraphQLFixture
	componentKey string
	currentMedia identity.ID
}

func TestVV_IT_477SinkRecaptureCreatesRC1WithOneSupplementRequirement(t *testing.T) {
	fx := newAPI10Fixture(t)
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fx.query("vv-it-477", fx.componentKey, fx.currentMedia.String(), "Show sink front", time.Now().UTC().Add(24*time.Hour)), fx.ctx, "", "")
	var payload struct {
		Data struct {
			Request struct {
				Recapture *struct {
					ID, ResponsibilityID, Status string
				} `json:"recapture"`
			} `json:"requestRecapture"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil || payload.Data.Request.Recapture == nil || payload.Data.Request.Recapture.Status != "REQUESTED" || strings.Contains(response, `"errors"`) {
		t.Fatalf("requestRecapture = %s (%v)", response, err)
	}
	responsibilityID, err := identity.ParseID(payload.Data.Request.Recapture.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	var draft database.CaptureDraft
	if err := fx.h.AdminDB.Where("tenant_id=? AND responsibility_id=? AND kind='RECAPTURE'", fx.seed.TenantID, responsibilityID).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	var requirements []struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(draft.Requirements, &requirements); err != nil || len(requirements) != 1 || requirements[0].Key != "video:supplement" {
		t.Fatalf("RC1 requirements = %s (%v), want one video:supplement", draft.Requirements, err)
	}
	var selected []database.RecaptureRequirement
	if err := fx.h.AdminDB.Where("tenant_id=? AND request_id=?", fx.seed.TenantID, payload.Data.Request.Recapture.ID).Find(&selected).Error; err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].RequirementKey != fx.componentKey || selected[0].Reason != "Show sink front" {
		t.Fatalf("RC1 selected scope = %+v", selected)
	}
}

func TestVV_IT_478RequestRecaptureRequiresAuthentication(t *testing.T) {
	fx := newAPI10Fixture(t)
	assertAPI10Rejected(t, fx, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fx.valid("vv-it-478"), context.Background(), "", ""), "UNAUTHENTICATED")
}

func TestVV_IT_479RequestRecaptureRequiresCapability(t *testing.T) {
	fx := newAPI10Fixture(t)
	viewer := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: fx.seed.TenantID, Roles: []string{auth.Viewer}}, CorrelationID: "vv-it-479"})
	assertAPI10Rejected(t, fx, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fx.valid("vv-it-479"), viewer, "", ""), "FORBIDDEN")
}

func TestVV_IT_480ForeignTenantRecaptureResourcesAreNotFound(t *testing.T) {
	fx := newAPI10Fixture(t)
	foreign := newAPI10FixtureWithHarness(t, fx.h)
	deadline := time.Now().UTC().Add(24 * time.Hour)
	cases := map[string]string{
		"inspectionId":    strings.Replace(fx.valid("vv-it-480-inspection"), fx.seed.InspectionID.String(), foreign.seed.InspectionID.String(), 1),
		"originalMediaId": fx.query("vv-it-480-media", fx.componentKey, foreign.currentMedia.String(), "Show sink front", deadline),
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
			assertAPI10Rejected(t, fx, response, "NOT_FOUND")
			for _, leaked := range []string{foreign.seed.TenantID.String(), foreign.componentKey} {
				if strings.Contains(response, leaked) {
					t.Fatalf("foreign recapture metadata leaked: %s", response)
				}
			}
		})
	}
	assertAPI10NoRecapture(t, foreign)
}

func TestVV_IT_481RequestRecaptureAdapterUnavailable(t *testing.T) {
	assertAPI10Failure(t, "dependency", "DEPENDENCY_UNAVAILABLE")
}

func TestVV_IT_482RequestRecaptureInternalErrorHidesSecret(t *testing.T) {
	assertAPI10Failure(t, "internal", "INTERNAL")
}

func TestVV_IT_483UnknownRecaptureComponentIsInvalidInput(t *testing.T) {
	fx := newAPI10Fixture(t)
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fx.query("vv-it-483", "video:"+identity.NewID().String(), fx.currentMedia.String(), "Show sink front", time.Now().UTC().Add(24*time.Hour)), fx.ctx, "", "")
	assertAPI10Rejected(t, fx, response, "INVALID_INPUT")
}

func TestVV_IT_484EmptyRecaptureScopeIsInvalidInput(t *testing.T) {
	fx := newAPI10Fixture(t)
	query := `mutation { requestRecapture(input:{inspectionId:"` + fx.seed.InspectionID.String() + `",items:[],deadlineAt:"` + time.Now().UTC().Add(24*time.Hour).Format(time.RFC3339) + `",clientMutationId:"vv-it-484"}) { recapture { id } userErrors { code } clientMutationId } }`
	assertAPI10Rejected(t, fx, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", ""), "INVALID_INPUT")
}

func TestVV_IT_485InvalidRecaptureDeadlineIsInvalidInput(t *testing.T) {
	fx := newAPI10Fixture(t)
	t.Run("past", func(t *testing.T) {
		assertAPI10Rejected(t, fx, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fx.query("vv-it-485-past", fx.componentKey, fx.currentMedia.String(), "Show sink front", time.Now().UTC().Add(-time.Second)), fx.ctx, "", ""), "INVALID_INPUT")
	})
	t.Run("malformed", func(t *testing.T) {
		query := strings.Replace(fx.valid("vv-it-485-malformed"), `deadlineAt:"`, `deadlineAt:"not-a-date`, 1)
		assertAPI10Rejected(t, fx, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", ""), "INVALID_INPUT")
	})
}

func TestVV_IT_486SixthRecaptureCycleIsInvalidState(t *testing.T) {
	fx := newAPI10Fixture(t)
	now := time.Now().UTC()
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		for cycle := 0; cycle < recapturecore.MaxRecaptureCycles; cycle++ {
			if err := tx.Create(&database.RecaptureRequest{ID: identity.NewID(), TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, ResponsibilityID: identity.NewID(), Status: "SUBMITTED", DeadlineAt: now, IdempotencyKey: "vv-it486-prior-" + string(rune('a'+cycle)), PayloadDigest: strings.Repeat("a", 64), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fx.valid("vv-it-486"), fx.ctx, "", "")
	if !strings.Contains(response, "INVALID_STATE") || strings.Contains(response, `"recapture":{`) {
		t.Fatalf("sixth cycle = %s", response)
	}
	var requests int64
	if err := fx.h.AdminDB.Model(&database.RecaptureRequest{}).Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, fx.seed.InspectionID).Count(&requests).Error; err != nil {
		t.Fatal(err)
	}
	if requests != recapturecore.MaxRecaptureCycles {
		t.Fatalf("sixth cycle created a request: %d", requests)
	}
}

func TestVV_IT_487IneligibleReportScopeIsInvalidState(t *testing.T) {
	fx := newAPI10Fixture(t)
	// Without an active sealed video plan there is no supported report scope.
	if err := fx.h.AdminDB.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.InspectionID).Update("active_video_plan_id", nil).Error; err != nil {
		t.Fatal(err)
	}
	assertAPI10Rejected(t, fx, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fx.valid("vv-it-487"), fx.ctx, "", ""), "INVALID_STATE")
}

func assertAPI10Failure(t *testing.T, failure, code string) {
	t.Helper()
	fx := newAPI10Fixture(t)
	if err := testfailure.Install(fx.h.DB); err != nil {
		t.Fatal(err)
	}
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fx.valid("vv-api10-"+failure), fx.ctx, "requestRecapture", failure)
	assertAPI10Rejected(t, fx, response, code)
	if strings.Contains(response, testfailure.Secret) {
		t.Fatalf("adapter error leaked secret: %s", response)
	}
}

// assertAPI10Rejected requires the error code, a null result and no request.
func assertAPI10Rejected(t *testing.T, fx api10Fixture, response, code string) {
	t.Helper()
	assertAPI06Rejected(t, response, code)
	if strings.Contains(response, `"recapture":{`) {
		t.Fatalf("rejected recapture reported a request: %s", response)
	}
	assertAPI10NoRecapture(t, fx)
}

func assertAPI10NoRecapture(t *testing.T, fx api10Fixture) {
	t.Helper()
	var requests int64
	if err := fx.h.AdminDB.Model(&database.RecaptureRequest{}).Where("tenant_id=? AND inspection_id=? AND status IN ?", fx.seed.TenantID, fx.seed.InspectionID, []string{"REQUESTED", "ACCESSED", "RECEIVED"}).Count(&requests).Error; err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatalf("rejected operation left %d active recaptures", requests)
	}
}

func (fx api10Fixture) query(clientID, key, originalMediaID, reason string, deadline time.Time) string {
	return `mutation { requestRecapture(input:{inspectionId:"` + fx.seed.InspectionID.String() + `",items:[{requirementKey:"` + key + `",originalMediaId:"` + originalMediaID + `",reason:"` + reason + `"}],deadlineAt:"` + deadline.Format(time.RFC3339) + `",clientMutationId:"` + clientID + `"}) { recapture { id responsibilityId status deadlineAt } userErrors { code field } clientMutationId } }`
}

func (fx api10Fixture) valid(clientID string) string {
	return fx.query(clientID, fx.componentKey, fx.currentMedia.String(), "Show sink front", time.Now().UTC().Add(24*time.Hour))
}

func newAPI10Fixture(t *testing.T) api10Fixture {
	t.Helper()
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	h, err := harness.NewFromEnv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return newAPI10FixtureWithHarness(t, h)
}

// newAPI10FixtureWithHarness seeds a completed VIDEO inspection whose active
// sealed plan has one current-only sink component filmed in C1.
func newAPI10FixtureWithHarness(t *testing.T, h *harness.Harness) api10Fixture {
	t.Helper()
	ctx := context.Background()
	seed, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	digest := strings.Repeat("e", 64)
	mediaID, preparationID, frameID, planID, submissionID, componentID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	componentKey := "video:" + componentID.String()
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", seed.TenantID, seed.DraftID).Updates(map[string]any{"kind": "INSPECTION", "media_kind": "VIDEO", "status": "SUBMITTED", "requirements": json.RawMessage(`[{"key":"video:main","section":"inspection","label":"Vídeo","evidenceKind":"VIDEO","required":true,"minimumMedia":1,"maximumMedia":1}]`)}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: seed.TenantID, ResponsibilityID: seed.ResponsibilityID, ObjectKey: "vv-api10/" + mediaID.String() + ".mp4", ContentType: "video/mp4", SHA256: digest, SizeBytes: 10, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "vv-api10-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: seed.TenantID, DraftID: seed.DraftID, ResponsibilityID: seed.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: json.RawMessage(`{"mediaKind":"VIDEO","mediaId":"` + mediaID.String() + `"}`), PayloadDigest: digest, SubmittedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: seed.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: seed.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 7_000_000, SHA256: digest, ObjectKey: "vv-api10/frame/" + frameID.String(), Width: 2, Height: 2, Role: "CURRENT", SizeBytes: 10, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoScopeItem{ID: componentID, TenantID: seed.TenantID, PreparationID: preparationID, FrameID: frameID, EnvironmentCandidate: "Cozinha", ComponentLabel: "Pia", ViewLabel: "frente", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlan{ID: planID, TenantID: seed.TenantID, InspectionID: seed.InspectionID, SubmissionID: submissionID, CurrentPreparationID: preparationID, Generation: 1, PlanDigest: digest, Status: "SEALED", SealedAt: &now, NextAttemptAt: now, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlanPreparation{TenantID: seed.TenantID, PlanID: planID, PreparationID: preparationID, Role: "MAIN", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlanItem{ID: identity.NewID(), TenantID: seed.TenantID, PlanID: planID, ComponentID: componentID, RequirementKey: componentKey, Outcome: "CURRENT_ONLY", InputDigest: digest, OriginFrameIDs: json.RawMessage(`[]`), CurrentFrameIDs: json.RawMessage(`["` + frameID.String() + `"]`), CoreGroups: json.RawMessage(`[]`), CurrentGroups: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", seed.TenantID, seed.InspectionID).Updates(map[string]any{"media_kind": "VIDEO", "active_video_plan_id": planID}).Error
	}); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := participantget.Setup(participantget.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	if err := recapturerequest.Setup(recapturerequest.Dependencies{DB: h.DB, Bus: bus, Service: recapturecore.Service{DB: h.DB}, Notifications: originInviteNotificationStub{}, CaptureBaseURL: "https://capture.example.test", Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Bus: bus}}))
	server.SetErrorPresenter(graph.PresentError)
	admin := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: seed.TenantID, Principal: requestctx.Principal{IdentityID: seed.IdentityID, MembershipID: seed.MembershipID, TenantID: seed.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "vv-api10"})
	return api10Fixture{fixedOriginGraphQLFixture: fixedOriginGraphQLFixture{ctx: admin, h: h, seed: seed, server: server, bus: bus}, componentKey: componentKey, currentMedia: mediaID}
}
