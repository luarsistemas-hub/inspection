//go:build integration

package resolvers

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	capturebeginvideo "inspection/services/inspection/internal/features/capture/begin_video_capture"
	capturebootstrap "inspection/services/inspection/internal/features/capture/bootstrap"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	capturereferences "inspection/services/inspection/internal/features/capture/reference_items"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/features/media/configure_video_policy"
	mediacore "inspection/services/inspection/internal/features/media/core"
	mediacreate "inspection/services/inspection/internal/features/media/create_upload"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

type guidanceScope struct{ environment, component, view, identityStatus string }

type guidanceOrigin struct {
	originID, versionID, preparationID, mediaID identity.ID
	frameIDs                                    []identity.ID
}

// seedGuidanceOrigin creates a READY VIDEO origin version whose frame bank
// carries the given scope, and activates it for the fixture asset.
func seedGuidanceOrigin(t *testing.T, fx fixedOriginGraphQLFixture, originID identity.ID, version int, scopes []guidanceScope) guidanceOrigin {
	t.Helper()
	o := guidanceOrigin{originID: originID, versionID: identity.NewID(), preparationID: identity.NewID(), mediaID: identity.NewID()}
	digest := strings.Repeat(strconv.Itoa(version%10), 64)
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		responsibility := database.Responsibility{ID: identity.NewID(), TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, ParticipantID: fx.seed.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&responsibility).Error; err != nil {
			return err
		}
		if version == 1 {
			if err := tx.Create(&database.Origin{ID: originID, TenantID: fx.seed.TenantID, AssetID: fx.seed.AssetID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
		} else if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND origin_id=? AND status='ACTIVE'", fx.seed.TenantID, originID).Update("status", "SUPERSEDED").Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: o.mediaID, TenantID: fx.seed.TenantID, ResponsibilityID: responsibility.ID, ObjectKey: "guidance/" + o.mediaID.String(), ContentType: "video/mp4", SHA256: digest, SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "guidance-" + o.mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: o.preparationID, TenantID: fx.seed.TenantID, MediaID: o.mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		for i, scope := range scopes {
			frameID := identity.NewID()
			o.frameIDs = append(o.frameIDs, frameID)
			if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fx.seed.TenantID, SourceMediaID: o.mediaID, PreparationID: o.preparationID, PTSMicros: int64(i+1) * 1_000_000, SHA256: digest, ObjectKey: "guidance/frames/" + frameID.String(), Width: 1, Height: 1, Role: "BASELINE", Quality: "GOOD", SizeBytes: 1, CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fx.seed.TenantID, PreparationID: o.preparationID, FrameID: frameID, EnvironmentCandidate: scope.environment, ComponentLabel: scope.component, ViewLabel: scope.view, Visibility: "VISIBLE", IdentityStatus: scope.identityStatus, Landmarks: json.RawMessage(`[]`), CreatedAt: now.Add(time.Duration(i) * time.Millisecond)}).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(&database.OriginVersion{ID: o.versionID, TenantID: fx.seed.TenantID, OriginID: originID, VersionNumber: version, ResponsibilityID: &responsibility.ID, Status: "ACTIVE", MediaKind: "VIDEO", VideoPreparationID: &o.preparationID, ScopeManifestDigest: digest, IdempotencyKey: "guidance-v" + strconv.Itoa(version) + "-" + o.versionID.String(), CreatedAt: now, SubmittedAt: &now, ActivatedAt: &now}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Origin{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, originID).Updates(map[string]any{"active_version_id": o.versionID, "version": version}).Error
	}); err != nil {
		t.Fatal(err)
	}
	return o
}

func createGuidanceInspection(t *testing.T, fx fixedOriginGraphQLFixture, mode, key string) identity.ID {
	t.Helper()
	now := time.Now().UTC()
	if err := fx.h.AdminDB.Exec("INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, TRUE, 1, ?, 'guidance fixture', 'vv-guidance-admission', '') ON CONFLICT (id) DO UPDATE SET enabled=TRUE", now).Error; err != nil {
		t.Fatal(err)
	}
	query := `mutation { createInspection(input:{assetId:"` + fx.seed.AssetID.String() + `",participantId:"` + fx.seed.ParticipantID.String() + `",templateId:"` + fx.templateID.String() + `",comparisonMode:` + mode + `,dueAt:"` + now.Add(time.Hour).Format(time.RFC3339) + `",deadlineAt:"` + now.Add(24*time.Hour).Format(time.RFC3339) + `",reminderInstants:[],reason:"` + key + `",clientMutationId:"` + key + `"}) { inspection { id } } }`
	response := executeTask06GraphQL(t, fx.server, query, fx.ctx)
	var payload struct {
		Data struct {
			Create struct {
				Inspection struct {
					ID string `json:"id"`
				} `json:"inspection"`
			} `json:"createInspection"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil || payload.Data.Create.Inspection.ID == "" || strings.Contains(response, `"errors"`) {
		t.Fatalf("createInspection %s: %s (%v)", mode, response, err)
	}
	id, err := identity.ParseID(payload.Data.Create.Inspection.ID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type guidanceCapture struct {
	MediaKind    string `json:"mediaKind"`
	Requirements []struct {
		Key              string `json:"key"`
		EvidenceKind     string `json:"evidenceKind"`
		ComparisonTarget string `json:"comparisonTarget"`
	} `json:"requirements"`
	ReferenceItems []struct {
		EvidenceID   string `json:"evidenceId"`
		TimestampMS  int    `json:"timestampMs"`
		Environment  string `json:"environment"`
		Component    string `json:"component"`
		View         string `json:"view"`
		Ambiguous    bool   `json:"ambiguous"`
		Availability string `json:"availability"`
		ImageURL     string `json:"imageUrl"`
	} `json:"referenceItems"`
}

type guidanceCaptureSession struct {
	server      *handler.Server
	credentials requestctx.ExternalCredentials
}

func newGuidanceCaptureSession(t *testing.T, fx fixedOriginGraphQLFixture, inspectionID identity.ID) guidanceCaptureSession {
	t.Helper()
	var responsibility database.Responsibility
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, inspectionID).Take(&responsibility).Error
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	pepper := []byte(strings.Repeat("p", 32))
	token, err := security.NewScopedToken(fx.seed.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "vv-guidance-csrf"
	hash := security.HashToken(token)
	proof, err := security.CSRFProof(hash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	invitation := database.Invitation{ID: identity.NewID(), TenantID: fx.seed.TenantID, ResponsibilityID: responsibility.ID, TokenHash: hash[:], DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	rows := []any{
		&invitation,
		&database.ExternalSession{ID: identity.NewID(), TenantID: fx.seed.TenantID, InvitationID: identity.Ptr(invitation.ID), ResponsibilityID: &responsibility.ID, SessionDigest: hash[:], CSRFDigest: proof[:], ExpiresAt: now.Add(time.Hour), CreatedAt: now},
	}
	var accepted int64
	if err := fx.h.AdminDB.Model(&database.ProcessingAcceptance{}).Where("tenant_id=? AND responsibility_id=?", fx.seed.TenantID, responsibility.ID).Count(&accepted).Error; err != nil {
		t.Fatal(err)
	}
	if accepted == 0 {
		rows = append(rows, &database.ProcessingAcceptance{ID: identity.NewID(), TenantID: fx.seed.TenantID, ResponsibilityID: responsibility.ID, DisclosureVersion: invitationcore.PrivacyDisclosureVersion, PhotoProcessing: true, VideoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now})
	}
	for _, row := range rows {
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
	if err := capturebeginvideo.Setup(capturebeginvideo.Dependencies{DB: fx.h.DB, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	if err := mediacreate.Setup(mediacreate.Dependencies{Bus: bus, Service: mediacore.Service{DB: fx.h.DB, Store: objectstore.Store{Bucket: fx.h.Store.Bucket, Client: fx.h.Store.Client}, Within: fx.h.WithinTenant}}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: fx.h.DB, Bus: bus, Invitations: invitationcore.Service{DB: fx.h.DB, Pepper: pepper}}}))
	server.SetErrorPresenter(graph.PresentError)
	return guidanceCaptureSession{server: server, credentials: requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf}}
}

// openGuidance opens the participant's authenticated capture for the
// inspection responsibility and reads externalCapture guidance.
func openGuidance(t *testing.T, fx fixedOriginGraphQLFixture, inspectionID identity.ID) guidanceCapture {
	t.Helper()
	session := newGuidanceCaptureSession(t, fx, inspectionID)
	query := `query { externalCapture { mediaKind requirements { key evidenceKind comparisonTarget } referenceItems { evidenceId timestampMs environment component view ambiguous availability imageUrl } } }`
	response := executeCaptureGraphQL(t, session.server, query, fx.seed.TenantID, session.credentials, true)
	var payload struct {
		Data struct {
			Capture guidanceCapture `json:"externalCapture"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil || strings.Contains(response, `"errors"`) {
		t.Fatalf("externalCapture guidance: %s (%v)", response, err)
	}
	return payload.Data.Capture
}

func assertSingleVideoMain(t *testing.T, capture guidanceCapture, target string) {
	t.Helper()
	if capture.MediaKind != "VIDEO" || len(capture.Requirements) != 1 || capture.Requirements[0].Key != "video:main" || capture.Requirements[0].EvidenceKind != "VIDEO" || capture.Requirements[0].ComparisonTarget != target {
		t.Fatalf("guidance requirements=%+v media=%s; want one video:main for %s", capture.Requirements, capture.MediaKind, target)
	}
}

func beginGuidanceVideoAttempt(t *testing.T, fx fixedOriginGraphQLFixture, session guidanceCaptureSession, mutationID, wantKind string) identity.ID {
	t.Helper()
	response := executeCaptureGraphQL(t, session.server, `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"`+mutationID+`"}) { attempt { id kind maxDurationSeconds status } userErrors { code } } }`, fx.seed.TenantID, session.credentials, true)
	var payload struct {
		Data struct {
			Begin struct {
				Attempt struct {
					ID          string `json:"id"`
					Kind        string `json:"kind"`
					MaxDuration int    `json:"maxDurationSeconds"`
					Status      string `json:"status"`
				} `json:"attempt"`
			} `json:"beginVideoCapture"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil || strings.Contains(response, `"errors"`) {
		t.Fatalf("beginVideoCapture %s: %s (%v)", mutationID, response, err)
	}
	attempt := payload.Data.Begin.Attempt
	if attempt.ID == "" || attempt.Kind != wantKind || attempt.MaxDuration != 900 || attempt.Status != "ACTIVE" {
		t.Fatalf("attempt %s = %+v; want kind=%s maxDurationSeconds=900 status=ACTIVE", mutationID, attempt, wantKind)
	}
	id, err := identity.ParseID(attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func guidanceVideoUpload(t *testing.T, fx fixedOriginGraphQLFixture, session guidanceCaptureSession, attemptID identity.ID, mutationID, contentType string) string {
	t.Helper()
	return executeCaptureGraphQL(t, session.server, `mutation { createMediaUpload(input:{contentType:"`+contentType+`",sizeBytes:1024,sha256:"`+strings.Repeat("a", 64)+`",attemptId:"`+attemptID.String()+`",clientMutationId:"`+mutationID+`"}) { upload { mediaId } userErrors { code } } }`, fx.seed.TenantID, session.credentials, true)
}

func TestVV_IT_002VideoTemplateV1AdmitsOnlyVideoForMainAndRC1Supplement(t *testing.T) {
	fx := newInspectionModeGraphQLFixture(t, false, catalog.ChecklistOnly)
	inspectionID := createGuidanceInspection(t, fx, "CHECKLIST_ONLY", "vv-it-002-video-v1")
	var inspection database.Inspection
	var responsibility database.Responsibility
	var templateVersion database.TemplateVersion
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", fx.seed.TenantID, inspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, inspectionID).Take(&responsibility).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND id=?", fx.seed.TenantID, inspection.TemplateVersionID).Take(&templateVersion).Error
	}); err != nil {
		t.Fatal(err)
	}
	var definition catalog.TemplateDocument
	if err := json.Unmarshal(templateVersion.DefinitionJSON, &definition); err != nil || !definition.IsVideo || inspection.TemplateVersionID == (identity.ID{}) {
		t.Fatalf("V1 is not pinned to isVideo=true template version: inspection=%+v template=%s (%v)", inspection, templateVersion.DefinitionJSON, err)
	}
	session := newGuidanceCaptureSession(t, fx, inspectionID)
	mainAttemptForPhoto := beginGuidanceVideoAttempt(t, fx, session, "vv-it-002-main-photo", "CURRENT")
	photo := guidanceVideoUpload(t, fx, session, mainAttemptForPhoto, "vv-it-002-main-photo-upload", "image/jpeg")
	if !strings.Contains(photo, `"code":"INVALID_INPUT"`) || strings.Contains(photo, `"upload":{"mediaId"`) {
		t.Fatalf("main video capture accepted JPEG: %s", photo)
	}
	mainVideo := guidanceVideoUpload(t, fx, session, mainAttemptForPhoto, "vv-it-002-main-video-upload", "video/mp4")
	if strings.Contains(mainVideo, `"errors"`) || !strings.Contains(mainVideo, `"upload":{"mediaId"`) {
		t.Fatalf("main video capture rejected VIDEO: %s", mainVideo)
	}
	if err := fx.h.AdminDB.Model(&database.VideoAttempt{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, mainAttemptForPhoto).Update("status", "RECEIVED").Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.AdminDB.Model(&database.Invitation{}).Where("tenant_id=? AND responsibility_id=? AND status='ACTIVE'", fx.seed.TenantID, responsibility.ID).Update("status", "COMPLETED").Error; err != nil {
		t.Fatal(err)
	}
	requestID := identity.NewID()
	now := time.Now().UTC()
	if err := fx.h.AdminDB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fx.seed.TenantID, responsibility.ID).Updates(map[string]any{"kind": "RECAPTURE", "requirements": json.RawMessage(`[{"key":"video:supplement","label":"Complemento","evidenceKind":"VIDEO","required":true,"minimumCount":1,"maximumCount":1}]`)}).Error; err != nil {
			return err
		}
		return tx.Create(&database.RecaptureRequest{ID: requestID, TenantID: fx.seed.TenantID, InspectionID: inspectionID, ResponsibilityID: responsibility.ID, Status: "REQUESTED", DeadlineAt: now.Add(time.Hour), IdempotencyKey: "vv-it-002-request", CreatedAt: now, UpdatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	recaptureSession := newGuidanceCaptureSession(t, fx, inspectionID)
	supplementPhotoAttempt := beginGuidanceVideoAttempt(t, fx, recaptureSession, "vv-it-002-supplement-photo", "SUPPLEMENT")
	supplementPhoto := guidanceVideoUpload(t, fx, recaptureSession, supplementPhotoAttempt, "vv-it-002-supplement-photo-upload", "image/jpeg")
	if !strings.Contains(supplementPhoto, `"code":"INVALID_INPUT"`) || strings.Contains(supplementPhoto, `"upload":{"mediaId"`) {
		t.Fatalf("RC1 supplement accepted JPEG: %s", supplementPhoto)
	}
	supplementVideo := guidanceVideoUpload(t, fx, recaptureSession, supplementPhotoAttempt, "vv-it-002-supplement-video-upload", "video/mp4")
	if strings.Contains(supplementVideo, `"errors"`) || !strings.Contains(supplementVideo, `"upload":{"mediaId"`) {
		t.Fatalf("RC1 supplement rejected VIDEO: %s", supplementVideo)
	}
}

func TestVV_IT_080GuidanceListsAllTwoHundredOriginComponentsWithOneVideoMain(t *testing.T) {
	fx := newInspectionModeGraphQLFixture(t, false, catalog.FixedOrigin)
	scopes := make([]guidanceScope, 200)
	for i := range scopes {
		scopes[i] = guidanceScope{environment: "room-" + strconv.Itoa(i/10), component: "component-" + strconv.Itoa(i), view: "wide", identityStatus: "RESOLVED"}
	}
	origin := seedGuidanceOrigin(t, fx, identity.NewID(), 1, scopes)
	capture := openGuidance(t, fx, createGuidanceInspection(t, fx, "FIXED_ORIGIN", "vv-it-080"))
	assertSingleVideoMain(t, capture, "FIXED_ORIGIN")
	if len(capture.ReferenceItems) != 200 {
		t.Fatalf("guidance items=%d, want all 200 origin components", len(capture.ReferenceItems))
	}
	want := map[string]bool{}
	for _, id := range origin.frameIDs {
		want[id.String()] = true
	}
	for _, item := range capture.ReferenceItems {
		if !want[item.EvidenceID] || item.Availability != "AVAILABLE" || item.ImageURL == "" {
			t.Fatalf("guidance item %+v is not an available O1 component", item)
		}
		delete(want, item.EvidenceID)
	}
	if len(want) != 0 {
		t.Fatalf("%d origin components missing from guidance", len(want))
	}
}

func TestVV_IT_087IdenticalBedroomsAreTwoAmbiguousContextualCandidates(t *testing.T) {
	fx := newInspectionModeGraphQLFixture(t, false, catalog.FixedOrigin)
	seedGuidanceOrigin(t, fx, identity.NewID(), 1, []guidanceScope{
		{"kitchen", "sink", "wide", "RESOLVED"},
		{"bedroom", "wardrobe", "wide", "AMBIGUOUS"},
		{"bedroom", "wardrobe", "wide", "AMBIGUOUS"},
	})
	capture := openGuidance(t, fx, createGuidanceInspection(t, fx, "FIXED_ORIGIN", "vv-it-087"))
	var bedrooms []int
	for i, item := range capture.ReferenceItems {
		if item.Environment == "bedroom" {
			bedrooms = append(bedrooms, i)
		} else if item.Ambiguous {
			t.Fatalf("resolved %s/%s marked ambiguous", item.Environment, item.Component)
		}
	}
	if len(bedrooms) != 2 {
		t.Fatalf("bedroom candidates=%d, want two", len(bedrooms))
	}
	first, second := capture.ReferenceItems[bedrooms[0]], capture.ReferenceItems[bedrooms[1]]
	if !first.Ambiguous || !second.Ambiguous || first.EvidenceID == second.EvidenceID || first.TimestampMS == second.TimestampMS {
		t.Fatalf("identical bedrooms are not two distinct ambiguous candidates: %+v %+v", first, second)
	}
}

func TestVV_IT_083ReloadAfterO2ActivationKeepsPinnedO1Scope(t *testing.T) {
	fx := newInspectionModeGraphQLFixture(t, false, catalog.FixedOrigin)
	originID := identity.NewID()
	o1 := seedGuidanceOrigin(t, fx, originID, 1, []guidanceScope{{"kitchen", "sink", "wide", "RESOLVED"}, {"bathroom", "basin", "wide", "RESOLVED"}})
	v1 := createGuidanceInspection(t, fx, "FIXED_ORIGIN", "vv-it-083")
	before := openGuidance(t, fx, v1)
	seedGuidanceOrigin(t, fx, originID, 2, []guidanceScope{{"living room", "window", "wide", "RESOLVED"}})
	after := openGuidance(t, fx, v1)
	if len(after.ReferenceItems) != len(o1.frameIDs) || len(after.ReferenceItems) != len(before.ReferenceItems) {
		t.Fatalf("reloaded guidance items=%d before=%d; want pinned O1 (%d)", len(after.ReferenceItems), len(before.ReferenceItems), len(o1.frameIDs))
	}
	for i, item := range after.ReferenceItems {
		if item.EvidenceID != before.ReferenceItems[i].EvidenceID || item.EvidenceID != o1.frameIDs[i].String() || item.Environment == "living room" {
			t.Fatalf("reloaded guidance item %d=%+v; want pinned O1 frame %s", i, item, o1.frameIDs[i])
		}
	}
}

func TestVV_IT_090CurrentOnlyGuidanceHasNoFabricatedReferenceImage(t *testing.T) {
	fx := newInspectionModeGraphQLFixture(t, false, catalog.ChecklistOnly)
	capture := openGuidance(t, fx, createGuidanceInspection(t, fx, "CHECKLIST_ONLY", "vv-it-090"))
	assertSingleVideoMain(t, capture, "CHECKLIST_ONLY")
	if len(capture.ReferenceItems) != 0 {
		t.Fatalf("current-only V2 guidance has reference items: %+v", capture.ReferenceItems)
	}
}

func TestVV_IT_077LostO1BankShowsUnavailableBaselineWithoutSwitchingMode(t *testing.T) {
	fx := newInspectionModeGraphQLFixture(t, false, catalog.FixedOrigin)
	origin := seedGuidanceOrigin(t, fx, identity.NewID(), 1, []guidanceScope{{"kitchen", "sink", "wide", "RESOLVED"}, {"bathroom", "basin", "wide", "RESOLVED"}})
	v1 := createGuidanceInspection(t, fx, "FIXED_ORIGIN", "vv-it-077")
	// O1's usable bank is lost before the comparative capture starts.
	if err := fx.h.AdminDB.Where("tenant_id=? AND preparation_id=?", fx.seed.TenantID, origin.preparationID).Delete(&database.VideoScopeItem{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.AdminDB.Where("tenant_id=? AND preparation_id=?", fx.seed.TenantID, origin.preparationID).Delete(&database.VideoFrame{}).Error; err != nil {
		t.Fatal(err)
	}
	capture := openGuidance(t, fx, v1)
	assertSingleVideoMain(t, capture, "FIXED_ORIGIN")
	if len(capture.ReferenceItems) != len(origin.frameIDs) {
		t.Fatalf("guidance items=%d, want the pinned O1 components explained as unavailable", len(capture.ReferenceItems))
	}
	for _, item := range capture.ReferenceItems {
		if item.Availability != "UNAVAILABLE" || item.ImageURL != "" {
			t.Fatalf("lost baseline item %+v; want UNAVAILABLE without an image", item)
		}
	}
	var inspection database.Inspection
	if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, v1).Take(&inspection).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot database.ReferenceSnapshot
	if err := fx.h.AdminDB.Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, v1).Take(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if inspection.MediaKind != "VIDEO" || snapshot.ComparisonMode != "FIXED_ORIGIN" {
		t.Fatalf("lost baseline switched V1 to media=%s mode=%s", inspection.MediaKind, snapshot.ComparisonMode)
	}
}
