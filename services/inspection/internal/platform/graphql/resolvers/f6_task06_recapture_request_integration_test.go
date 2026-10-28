//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	capturebootstrap "inspection/services/inspection/internal/features/capture/bootstrap"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	capturereferences "inspection/services/inspection/internal/features/capture/reference_items"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/features/media/configure_video_policy"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	recapturerequest "inspection/services/inspection/internal/features/recapture/request"
	"inspection/services/inspection/internal/features/reports/publication"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

func TestVV_IT_222SinkRecaptureCreatesFocusedSupplementScope(t *testing.T) {
	fx := newAPI10Fixture(t)
	deadline := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fx.query("vv-it-222", fx.componentKey, fx.currentMedia.String(), "Show sink front", deadline), fx.ctx, "", "")
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"deadlineAt":"`+deadline.Format(time.RFC3339)+`"`) {
		t.Fatalf("requestRecapture response = %s; expected successful request with T0+1d deadline", response)
	}
	var payload struct {
		Data struct {
			Request struct {
				Recapture struct{ ID, ResponsibilityID, Status string } `json:"recapture"`
			} `json:"requestRecapture"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil || payload.Data.Request.Recapture.Status != "REQUESTED" {
		t.Fatalf("requestRecapture = %s (%v)", response, err)
	}
	var draft database.CaptureDraft
	if err := fx.h.AdminDB.Where("tenant_id=? AND responsibility_id=? AND kind='RECAPTURE'", fx.seed.TenantID, payload.Data.Request.Recapture.ResponsibilityID).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	var requirements []struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(draft.Requirements, &requirements); err != nil || len(requirements) != 1 || requirements[0].Key != "video:supplement" {
		t.Fatalf("RC1 requirements = %s (%v); want only video:supplement", draft.Requirements, err)
	}
	var selected []database.RecaptureRequirement
	if err := fx.h.AdminDB.Where("tenant_id=? AND request_id=?", fx.seed.TenantID, payload.Data.Request.Recapture.ID).Find(&selected).Error; err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].RequirementKey != fx.componentKey || selected[0].Reason != "Show sink front" {
		t.Fatalf("selected RC1 scope = %+v; want sink with supplied reason", selected)
	}
}

func TestVV_IT_225SixthRecaptureRejectedAndGapRemains(t *testing.T) {
	fx := newAPI10Fixture(t)
	now := time.Now().UTC()
	if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
		for cycle := 0; cycle < 5; cycle++ {
			id := identity.NewID()
			if err := tx.Create(&database.RecaptureRequest{ID: id, TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, ResponsibilityID: identity.NewID(), Status: "SUBMITTED", DeadlineAt: now, IdempotencyKey: "vv-it225-prior-" + id.String(), PayloadDigest: strings.Repeat("b", 64), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fx.valid("vv-it-225"), fx.ctx, "", "")
	if !strings.Contains(response, "INVALID_STATE") || strings.Contains(response, `"recapture":{"`) {
		t.Fatalf("sixth cycle response = %s", response)
	}
	var requests int64
	if err := fx.h.AdminDB.Model(&database.RecaptureRequest{}).Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, fx.seed.InspectionID).Count(&requests).Error; err != nil {
		t.Fatal(err)
	}
	var gap database.VideoPlanItem
	if err := fx.h.AdminDB.Where("tenant_id=? AND requirement_key=?", fx.seed.TenantID, fx.componentKey).Take(&gap).Error; err != nil {
		t.Fatal(err)
	}
	if requests != 5 || gap.Outcome != "CURRENT_ONLY" {
		t.Fatalf("cycle count=%d gap outcome=%q; want five cycles and visible unresolved gap", requests, gap.Outcome)
	}
}

func TestVV_IT_226UnrelatedVideoKeyAndEmptyReasonAreInvalid(t *testing.T) {
	fx := newAPI10Fixture(t)
	t.Run("unrelated video UUID", func(t *testing.T) {
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fx.query("vv-it-226-foreign", "video:"+identity.NewID().String(), fx.currentMedia.String(), "Show sink front", time.Now().UTC().Add(24*time.Hour)), fx.ctx, "", "")
		assertAPI10Rejected(t, fx, response, "INVALID_INPUT")
	})
	t.Run("empty reason", func(t *testing.T) {
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fx.query("vv-it-226-empty", fx.componentKey, fx.currentMedia.String(), "", time.Now().UTC().Add(24*time.Hour)), fx.ctx, "", "")
		assertAPI10Rejected(t, fx, response, "INVALID_INPUT")
	})
}

func TestVV_IT_224RPT1AndEvidenceRemainPublishedAfterRC1(t *testing.T) {
	fx := newAPI11Fixture(t)
	beforeReport := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api11Query(fx, "report", fx.seed.InspectionID, ""), fx.ctx, "", "")
	beforeCustomer := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api11Query(fx, "customerReport", fx.seed.InspectionID, ""), fx.customer, "", "")
	if strings.Contains(beforeReport, `"errors"`) || !strings.Contains(beforeReport, fx.frameID.String()) || strings.Contains(beforeCustomer, `"errors"`) || !strings.Contains(beforeCustomer, fx.snapshotID.String()) {
		t.Fatalf("RPT1 baseline unavailable: report=%s customerReport=%s", beforeReport, beforeCustomer)
	}
	var item database.VideoPlanItem
	if err := fx.h.AdminDB.Where("tenant_id=? AND plan_id=?", fx.seed.TenantID, fx.planID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	item.RequirementKey = "video:" + item.ComponentID.String()
	// API11's report fixture intentionally seeds the publication evidence only;
	// add the plan membership and active-generation pointer required by the
	// production recapture projection.
	var frame database.VideoFrame
	if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.frameID).Take(&frame).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.DraftID).Updates(map[string]any{"kind": "INSPECTION", "media_kind": "VIDEO", "status": "SUBMITTED", "requirements": json.RawMessage(`[{"key":"video:main","section":"inspection","label":"Video","evidenceKind":"VIDEO","required":true,"minimumMedia":1,"maximumMedia":1}]`)}).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoPlanItem{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, item.ID).Update("requirement_key", item.RequirementKey).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlanPreparation{TenantID: fx.seed.TenantID, PlanID: fx.planID, PreparationID: frame.PreparationID, Role: "MAIN", CreatedAt: time.Now().UTC()}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.InspectionID).Update("active_video_plan_id", fx.planID).Error
	}); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := participantget.Setup(participantget.Dependencies{DB: fx.h.DB, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	if err := recapturerequest.Setup(recapturerequest.Dependencies{DB: fx.h.DB, Bus: bus, Service: recapturecore.Service{DB: fx.h.DB}, Notifications: originInviteNotificationStub{}, CaptureBaseURL: "https://capture.example.test", Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: fx.h.DB, Bus: bus, PublicationService: publication.Service{DB: fx.h.DB}}}))
	server.SetErrorPresenter(graph.PresentError)
	admin := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: requestctx.Principal{IdentityID: fx.seed.IdentityID, TenantID: fx.seed.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "vv-it-224"})
	recaptureFx := api10Fixture{fixedOriginGraphQLFixture: fixedOriginGraphQLFixture{ctx: admin, h: fx.h, seed: fx.seed, server: server, bus: bus}, componentKey: item.RequirementKey, currentMedia: fx.sourceMedia}
	response := executeFixedOriginRequest(t, recaptureFx.fixedOriginGraphQLFixture, recaptureFx.valid("vv-it-224"), admin, "", "")
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"status":"REQUESTED"`) {
		t.Fatalf("requestRecapture after RPT1: %s", response)
	}
	afterReport := executeFixedOriginRequest(t, recaptureFx.fixedOriginGraphQLFixture, api11Query(fx, "report", fx.seed.InspectionID, ""), admin, "", "")
	afterCustomer := executeFixedOriginRequest(t, recaptureFx.fixedOriginGraphQLFixture, api11Query(fx, "customerReport", fx.seed.InspectionID, ""), fx.customer, "", "")
	afterCustomerEvidence := executeFixedOriginRequest(t, recaptureFx.fixedOriginGraphQLFixture, api11Query(fx, "customerEvidence", fx.seed.InspectionID, ""), fx.customer, "", "")
	if strings.Contains(afterReport, `"errors"`) || !strings.Contains(afterReport, fx.frameID.String()) {
		t.Fatalf("internal report lost RPT1 evidence after RC1: %s", afterReport)
	}
	if strings.Contains(afterCustomer, `"errors"`) || !strings.Contains(afterCustomer, fx.snapshotID.String()) {
		t.Fatalf("customerReport lost RPT1 after RC1: %s", afterCustomer)
	}
	if strings.Contains(afterCustomerEvidence, `"errors"`) || !strings.Contains(afterCustomerEvidence, fx.frameID.String()) {
		t.Fatalf("customer evidence lost RPT1 evidence after RC1: %s", afterCustomerEvidence)
	}
}

func TestVV_IT_003ActivePhotoTemplateV4DoesNotChangeVideoV1Capture(t *testing.T) {
	fx := newInspectionModeGraphQLFixture(t, false, catalog.ChecklistOnly)
	setHistoricalVideoAdmissionDisabled(t, fx)
	inspectionID := createGuidanceInspection(t, fx, "CHECKLIST_ONLY", "vv-it-003-video-v1")
	var inspection database.Inspection
	if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, inspectionID).Take(&inspection).Error; err != nil {
		t.Fatal(err)
	}
	photoDocument, err := json.Marshal(catalog.TemplateDocument{SchemaVersion: catalog.SchemaVersion, SegmentVersionID: fx.seed.SegmentVersionID.String(), DefaultComparisonMode: catalog.ChecklistOnly, AnalysisType: "REAL_ESTATE", Requirements: []catalog.CaptureRequirement{{Key: "room", Section: "inside", Label: "Room", EvidenceKind: "PHOTO", MinimumCount: 1, MaximumCount: 2, Required: true}}})
	if err != nil {
		t.Fatal(err)
	}
	photoVersionID := identity.NewID()
	if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Model(&database.TemplateVersion{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, inspection.TemplateVersionID).Update("status", "SUPERSEDED").Error; err != nil {
			return err
		}
		if err := tx.Create(&database.TemplateVersion{ID: photoVersionID, TenantID: fx.seed.TenantID, TemplateID: fx.templateID, VersionNumber: 4, SchemaVersion: catalog.SchemaVersion, DefinitionJSON: photoDocument, CanonicalDigest: strings.Repeat("4", 64), Status: "ACTIVE", IdempotencyKey: "vv-it003-photo-v4-" + photoVersionID.String(), PublishedAt: now, CreatedBy: fx.seed.IdentityID}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Template{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.templateID).Updates(map[string]any{"active_version_id": photoVersionID, "version": 4}).Error
	}); err != nil {
		t.Fatal(err)
	}
	capture := openGuidance(t, fx, inspectionID)
	if capture.MediaKind != "VIDEO" || len(capture.Requirements) != 1 || capture.Requirements[0].Key != "video:main" || capture.Requirements[0].EvidenceKind != "VIDEO" {
		t.Fatalf("externalCapture(V1) after PHOTO v4 = %+v; expected frozen VIDEO admission", capture)
	}
}

func TestVV_IT_235RecapturePageExposesThirtyItemsAndOneSupplementRequirement(t *testing.T) {
	fx := newAPI10Fixture(t)
	var baseItem database.VideoPlanItem
	var baseFrame database.VideoFrame
	if err := fx.h.AdminDB.Where("tenant_id=? AND requirement_key=?", fx.seed.TenantID, fx.componentKey).Take(&baseItem).Error; err != nil {
		t.Fatal(err)
	}
	var frameIDs []identity.ID
	if err := json.Unmarshal(baseItem.CurrentFrameIDs, &frameIDs); err != nil || len(frameIDs) != 1 {
		t.Fatalf("base frame IDs=%s (%v)", baseItem.CurrentFrameIDs, err)
	}
	if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, frameIDs[0]).Take(&baseFrame).Error; err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		componentID, frameID := identity.NewID(), identity.NewID()
		key := "video:" + componentID.String()
		keys = append(keys, key)
		if i == 0 { // Keep the fixture's existing current component as item one.
			keys[0] = fx.componentKey
			continue
		}
		if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
			now := time.Now().UTC()
			if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fx.seed.TenantID, SourceMediaID: fx.currentMedia, PreparationID: baseFrame.PreparationID, PTSMicros: int64(7_000_000 + i), SHA256: strings.Repeat("e", 64), ObjectKey: "vv-it235/frame/" + frameID.String(), Width: 2, Height: 2, Role: "CURRENT", SizeBytes: 10, CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fx.seed.TenantID, PreparationID: baseFrame.PreparationID, FrameID: frameID, EnvironmentCandidate: "Casa", ComponentLabel: "Item " + string(rune('A'+i)), ViewLabel: "frente", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
				return err
			}
			return tx.Create(&database.VideoPlanItem{ID: identity.NewID(), TenantID: fx.seed.TenantID, PlanID: baseItem.PlanID, ComponentID: componentID, RequirementKey: key, Outcome: "CURRENT_ONLY", InputDigest: strings.Repeat("e", 64), OriginFrameIDs: json.RawMessage(`[]`), CurrentFrameIDs: json.RawMessage(`[` + `"` + frameID.String() + `"` + `]`), CoreGroups: json.RawMessage(`[]`), CurrentGroups: json.RawMessage(`[]`), CreatedAt: now}).Error
		}); err != nil {
			t.Fatal(err)
		}
	}
	items := make([]string, 0, len(keys))
	for i, key := range keys {
		items = append(items, `{requirementKey:"`+key+`",originalMediaId:"`+fx.currentMedia.String()+`",reason:"Instruction `+strconv.Itoa(i+1)+`"}`)
	}
	deadline := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	query := `mutation { requestRecapture(input:{inspectionId:"` + fx.seed.InspectionID.String() + `",items:[` + strings.Join(items, ",") + `],deadlineAt:"` + deadline + `",clientMutationId:"vv-it-235"}) { recapture { responsibilityId status } userErrors { code } } }`
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
	var result struct {
		Data struct {
			Request struct {
				Recapture struct{ ResponsibilityID, Status string } `json:"recapture"`
			} `json:"requestRecapture"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &result); err != nil || result.Data.Request.Recapture.Status != "REQUESTED" || strings.Contains(response, `"errors"`) {
		t.Fatalf("request 30 recapture items: %s (%v)", response, err)
	}
	capture := openRecaptureResponsibility(t, fx, result.Data.Request.Recapture.ResponsibilityID)
	if len(capture.ReferenceItems) != 30 {
		t.Fatalf("recapture reference items=%d, want 30", len(capture.ReferenceItems))
	}
	if capture.MediaKind != "VIDEO" || len(capture.Requirements) != 1 || capture.Requirements[0].Key != "video:supplement" || capture.Requirements[0].EvidenceKind != "VIDEO" {
		t.Fatalf("recapture requirements=%+v media=%s; want one video:supplement", capture.Requirements, capture.MediaKind)
	}
}

func openRecaptureResponsibility(t *testing.T, fx api10Fixture, responsibilityText string) guidanceCapture {
	t.Helper()
	responsibilityID, err := identity.ParseID(responsibilityText)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	pepper := []byte(strings.Repeat("p", 32))
	token, err := security.NewScopedToken(fx.seed.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "vv-it235-csrf"
	hash := security.HashToken(token)
	proof, err := security.CSRFProof(hash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	invitation := database.Invitation{ID: identity.NewID(), TenantID: fx.seed.TenantID, ResponsibilityID: responsibilityID, TokenHash: hash[:], DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	for _, row := range []any{&invitation, &database.ExternalSession{ID: identity.NewID(), TenantID: fx.seed.TenantID, InvitationID: identity.Ptr(invitation.ID), ResponsibilityID: &responsibilityID, SessionDigest: hash[:], CSRFDigest: proof[:], ExpiresAt: now.Add(time.Hour), CreatedAt: now}, &database.ProcessingAcceptance{ID: identity.NewID(), TenantID: fx.seed.TenantID, ResponsibilityID: responsibilityID, DisclosureVersion: invitationcore.PrivacyDisclosureVersion, PhotoProcessing: true, VideoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}} {
		if err := fx.h.AdminDB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	bus := mediator.New()
	if err := capturebootstrap.Setup(capturebootstrap.Dependencies{Bus: bus, Service: capturecore.Service{DB: fx.h.DB}}); err != nil {
		t.Fatal(err)
	}
	if err := capturereferences.Setup(capturereferences.Dependencies{Bus: bus, DB: fx.h.DB, Store: fx.h.Store}); err != nil {
		t.Fatal(err)
	}
	if err := configure_video_policy.Setup(configure_video_policy.Dependencies{DB: fx.h.DB, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: fx.h.DB, Bus: bus, Invitations: invitationcore.Service{DB: fx.h.DB, Pepper: pepper}}}))
	server.SetErrorPresenter(graph.PresentError)
	query := `query { externalCapture { mediaKind requirements { key evidenceKind comparisonTarget } referenceItems { evidenceId timestampMs environment component view ambiguous availability imageUrl } } }`
	response := executeCaptureGraphQL(t, server, query, fx.seed.TenantID, requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf}, true)
	var payload struct {
		Data struct {
			Capture guidanceCapture `json:"externalCapture"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil || strings.Contains(response, `"errors"`) {
		t.Fatalf("externalCapture recapture: %s (%v)", response, err)
	}
	return payload.Data.Capture
}
