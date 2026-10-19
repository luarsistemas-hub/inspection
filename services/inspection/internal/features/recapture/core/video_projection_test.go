package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
)

func videoRecaptureFixture(t *testing.T) (recaptureFixture, identity.ID, identity.ID) {
	t.Helper()
	f := newRecaptureFixture(t, "SUBMITTED", 1)
	sqlDB, err := f.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := f.db.Exec("ATTACH DATABASE ':memory:' AS analysis").Error; err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE capture.submission_versions (id BLOB PRIMARY KEY, tenant_id BLOB, draft_id BLOB, responsibility_id BLOB, version_number INTEGER, complete BOOLEAN, requires_attention BOOLEAN, payload BLOB, payload_digest TEXT, submitted_at DATETIME)",
		"CREATE TABLE analysis.video_plans (id BLOB PRIMARY KEY, tenant_id BLOB, inspection_id BLOB, current_preparation_id BLOB, generation INTEGER, status TEXT)",
		"CREATE TABLE analysis.video_plan_items (id BLOB PRIMARY KEY, tenant_id BLOB, plan_id BLOB, component_id BLOB, carried_result_job_id BLOB, requirement_key TEXT, origin_frame_ids BLOB, current_frame_ids BLOB)",
		"CREATE TABLE analysis.video_plan_preparations (tenant_id BLOB, plan_id BLOB, preparation_id BLOB, role TEXT)",
		"CREATE TABLE analysis.comparison_jobs (id BLOB PRIMARY KEY, tenant_id BLOB, video_plan_id BLOB, video_plan_item_id BLOB, status TEXT)",
		"CREATE TABLE media.video_preparation_jobs (id BLOB PRIMARY KEY, tenant_id BLOB, media_id BLOB, status TEXT, generation INTEGER)",
		"CREATE TABLE media.video_frames (id BLOB PRIMARY KEY, tenant_id BLOB, source_media_id BLOB, preparation_id BLOB)",
	} {
		if err := f.db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	var original database.MediaObject
	if err := f.db.Where("tenant_id=? AND responsibility_id=?", f.tenantID, f.originalResponsibilityID).Take(&original).Error; err != nil {
		t.Fatal(err)
	}
	main := capturecore.Requirement{Key: "video:main", Section: "vistoria", Label: "Vídeo principal", Instructions: "Filme a vistoria", EvidenceKind: "VIDEO", Required: true, MinimumMedia: 1, MaximumMedia: 1, CaptureSourcePolicy: "CAMERA_DEFAULT"}
	if err := f.db.Exec("UPDATE capture.capture_drafts SET media_kind='VIDEO',requirements=? WHERE tenant_id=? AND responsibility_id=?", mustMarshal([]capturecore.Requirement{main}), f.tenantID, f.originalResponsibilityID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec("UPDATE media.media_objects SET media_kind='VIDEO',requirement_key='video:main' WHERE tenant_id=? AND id=?", f.tenantID, original.ID).Error; err != nil {
		t.Fatal(err)
	}
	preparationID, planID, itemID, componentID, frameID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	if err := f.db.Exec("INSERT INTO media.video_preparation_jobs (id,tenant_id,media_id,status) VALUES (?,?,?,?)", preparationID, f.tenantID, original.ID, "READY").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec("INSERT INTO analysis.video_plans (id,tenant_id,inspection_id,current_preparation_id,generation,status) VALUES (?,?,?,?,?,?)", planID, f.tenantID, f.inspectionID, preparationID, 1, "SEALED").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec("INSERT INTO analysis.video_plan_preparations (tenant_id,plan_id,preparation_id,role) VALUES (?,?,?,?)", f.tenantID, planID, preparationID, "MAIN").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec("INSERT INTO media.video_frames (id,tenant_id,source_media_id,preparation_id) VALUES (?,?,?,?)", frameID, f.tenantID, original.ID, preparationID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec("INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id,component_id,requirement_key,origin_frame_ids,current_frame_ids) VALUES (?,?,?,?,?,?,?)", itemID, f.tenantID, planID, componentID, "video:"+componentID.String(), []byte(`[]`), mustMarshal([]identity.ID{frameID})).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", f.tenantID, f.inspectionID).Update("active_video_plan_id", planID).Error; err != nil {
		t.Fatal(err)
	}
	return f, original.ID, componentID
}

func TestVideoRecaptureProjectsSingleSupplementUT050(t *testing.T) {
	f, originalID, componentID := videoRecaptureFixture(t)
	input := f.input("video:"+componentID.String(), "Mostre a pia de frente", "video-focus")
	input.Items[0].OriginalMediaID = &originalID
	created, err := f.service.Request(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	draft := loadDraft(t, f.db, created.ResponsibilityID)
	var requirements []capturecore.Requirement
	if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
		t.Fatal(err)
	}
	if draft.MediaKind != "VIDEO" || len(requirements) != 1 || requirements[0].Key != "video:supplement" || requirements[0].EvidenceKind != "VIDEO" || requirements[0].MinimumMedia != 1 || requirements[0].MaximumMedia != 1 || !strings.Contains(requirements[0].Instructions, input.Items[0].Reason) {
		t.Fatalf("focused video draft is invalid: %+v requirements=%+v", draft, requirements)
	}
	var reference struct {
		VideoRecapture videoRecaptureProjection `json:"videoRecapture"`
	}
	if err := json.Unmarshal(draft.ReferencePayload, &reference); err != nil || reference.VideoRecapture.Generation != 1 || len(reference.VideoRecapture.Selected) != 1 || reference.VideoRecapture.Selected[0].ComponentID != componentID || reference.VideoRecapture.Selected[0].OriginalMediaID != originalID {
		t.Fatalf("component projection was lost: %+v err=%v", reference, err)
	}
	var selected database.RecaptureRequirement
	if err := f.db.Where("tenant_id=? AND request_id=?", f.tenantID, created.RequestID).Take(&selected).Error; err != nil || selected.RequirementKey != input.Items[0].RequirementKey || selected.OriginalMediaID == nil || *selected.OriginalMediaID != originalID {
		t.Fatalf("selected component was not retained: %+v err=%v", selected, err)
	}
	originalDraft := loadDraft(t, f.db, f.originalResponsibilityID)
	if !strings.Contains(string(originalDraft.Requirements), "video:main") || strings.Contains(string(originalDraft.Requirements), "video:supplement") {
		t.Fatal("original capture draft was rewritten")
	}
}

func TestVideoRecaptureReadsLegacyScopeRequirements(t *testing.T) {
	f, originalID, componentID := videoRecaptureFixture(t)
	legacy := []capturecore.Requirement{{Key: "video:" + identity.NewID().String(), Section: "kitchen", Label: "sink", Instructions: "front view", EvidenceKind: "VIDEO_FRAME", Required: true, MinimumMedia: 1, MaximumMedia: 1, ComparisonTarget: "FIXED_ORIGIN"}}
	if err := f.db.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", f.tenantID, f.originalResponsibilityID).Update("requirements", mustMarshal(legacy)).Error; err != nil {
		t.Fatal(err)
	}
	input := f.input("video:"+componentID.String(), "Mostre a pia de frente", "video-legacy-draft")
	input.Items[0].OriginalMediaID = &originalID
	created, err := f.service.Request(context.Background(), input)
	if err != nil {
		t.Fatalf("legacy video draft requirements were rejected: %v", err)
	}
	draft := loadDraft(t, f.db, created.ResponsibilityID)
	var requirements []capturecore.Requirement
	if err := json.Unmarshal(draft.Requirements, &requirements); err != nil || len(requirements) != 1 || requirements[0].Key != "video:supplement" || requirements[0].EvidenceKind != "VIDEO" || requirements[0].Section != "vistoria" || requirements[0].CaptureSourcePolicy != "CAMERA_DEFAULT" || requirements[0].ComparisonTarget != "FIXED_ORIGIN" {
		t.Fatalf("legacy draft did not yield one supplement slot: requirements=%+v err=%v", requirements, err)
	}
}

func TestVideoRecaptureRejectsComponentOutsideSealedPlanUT051(t *testing.T) {
	f, originalID, _ := videoRecaptureFixture(t)
	input := f.input("video:"+identity.NewID().String(), "Mostre a pia", "video-other")
	input.Items[0].OriginalMediaID = &originalID
	if _, err := f.service.Request(context.Background(), input); errorCode(err) != apperror.InvalidInput {
		t.Fatalf("unknown video component was accepted: %v", err)
	}
	var count int64
	if err := f.db.Model(&database.RecaptureRequest{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("invalid component created a recapture: count=%d err=%v", count, err)
	}
}

func TestVideoRecaptureRejectsSixthCycleUT052(t *testing.T) {
	f, originalID, componentID := videoRecaptureFixture(t)
	for cycle := 0; cycle < MaxRecaptureCycles; cycle++ {
		row := database.RecaptureRequest{ID: identity.NewID(), TenantID: f.tenantID, InspectionID: f.inspectionID, ResponsibilityID: identity.NewID(), Status: "SUBMITTED", DeadlineAt: f.now, IdempotencyKey: "video-prior-" + string(rune('a'+cycle)), PayloadDigest: strings.Repeat("a", 64), CreatedAt: f.now, UpdatedAt: f.now}
		if err := f.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	input := f.input("video:"+componentID.String(), "Mostre a pia", "video-sixth")
	input.Items[0].OriginalMediaID = &originalID
	if _, err := f.service.Request(context.Background(), input); errorCode(err) != apperror.InvalidState {
		t.Fatalf("sixth video cycle was accepted: %v", err)
	}
}

func TestVideoRecaptureAfterG2UsesCurrentComponentEvidenceFrames(t *testing.T) {
	f, originalID, carriedComponentID := videoRecaptureFixture(t)
	var g1 database.VideoPlan
	if err := f.db.Where("tenant_id=? AND inspection_id=?", f.tenantID, f.inspectionID).Take(&g1).Error; err != nil {
		t.Fatal(err)
	}
	var carried database.VideoPlanItem
	if err := f.db.Where("tenant_id=? AND plan_id=? AND component_id=?", f.tenantID, g1.ID, carriedComponentID).Take(&carried).Error; err != nil {
		t.Fatal(err)
	}
	var carriedFrameIDs []identity.ID
	if err := json.Unmarshal(carried.CurrentFrameIDs, &carriedFrameIDs); err != nil || len(carriedFrameIDs) != 1 {
		t.Fatalf("G1 current frames=%v err=%v", carriedFrameIDs, err)
	}

	supplementMediaID := identity.NewID()
	if err := f.db.Create(&database.MediaObject{ID: supplementMediaID, TenantID: f.tenantID, ResponsibilityID: identity.NewID(), ObjectKey: "supplement/video.mp4", ContentType: "video/mp4", SHA256: strings.Repeat("b", 64), SizeBytes: 100, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:supplement", CreatedAt: f.now}).Error; err != nil {
		t.Fatal(err)
	}
	var latest database.MediaObject
	if err := f.db.Where("object_key=?", "supplement/video.mp4").Take(&latest).Error; err != nil {
		t.Fatal(err)
	}
	supplementPreparationID, g2ID := identity.NewID(), identity.NewID()
	if err := f.db.Exec("INSERT INTO media.video_preparation_jobs (id,tenant_id,media_id,status) VALUES (?,?,?,?)", supplementPreparationID, f.tenantID, latest.ID, "READY").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec("INSERT INTO analysis.video_plans (id,tenant_id,inspection_id,current_preparation_id,generation,status) VALUES (?,?,?,?,?,?)", g2ID, f.tenantID, f.inspectionID, supplementPreparationID, 2, "SEALED").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec("INSERT INTO analysis.video_plan_preparations (tenant_id,plan_id,preparation_id,role) VALUES (?,?,?,?)", f.tenantID, g2ID, supplementPreparationID, "SUPPLEMENT").Error; err != nil {
		t.Fatal(err)
	}
	supplementComponentID, supplementItemID, supplementFrameID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := f.db.Exec("INSERT INTO media.video_frames (id,tenant_id,source_media_id,preparation_id) VALUES (?,?,?,?)", supplementFrameID, f.tenantID, latest.ID, supplementPreparationID).Error; err != nil {
		t.Fatal(err)
	}
	carriedJobID := identity.NewID()
	if err := f.db.Exec("INSERT INTO analysis.comparison_jobs (id,tenant_id,video_plan_id,video_plan_item_id,status) VALUES (?,?,?,?,?)", carriedJobID, f.tenantID, g1.ID, carried.ID, "COMPLETED").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec("INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id,component_id,carried_result_job_id,requirement_key,origin_frame_ids,current_frame_ids) VALUES (?,?,?,?,?,?,?,?)", identity.NewID(), f.tenantID, g2ID, carriedComponentID, carriedJobID, "video:"+carriedComponentID.String(), []byte(`[]`), mustMarshal(carriedFrameIDs)).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec("INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id,component_id,requirement_key,origin_frame_ids,current_frame_ids) VALUES (?,?,?,?,?,?,?)", supplementItemID, f.tenantID, g2ID, supplementComponentID, "video:"+supplementComponentID.String(), []byte(`[]`), mustMarshal([]identity.ID{supplementFrameID})).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", f.tenantID, f.inspectionID).Update("active_video_plan_id", g2ID).Error; err != nil {
		t.Fatal(err)
	}
	input := f.input("video:"+supplementComponentID.String(), "Regravar o chuveiro", "video-after-g2")
	input.Items = append(input.Items, Item{RequirementKey: "video:" + carriedComponentID.String(), OriginalMediaID: &originalID, Reason: "Revisar a pia"})
	input.Items[0].OriginalMediaID = &supplementMediaID
	created, err := f.service.Request(context.Background(), input)
	if err != nil {
		t.Fatalf("recapture request after G2 failed: %v", err)
	}
	draft := loadDraft(t, f.db, created.ResponsibilityID)
	var reference struct {
		VideoRecapture videoRecaptureProjection `json:"videoRecapture"`
	}
	if err := json.Unmarshal(draft.ReferencePayload, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.VideoRecapture.PlanID != g2ID || len(reference.VideoRecapture.Selected) != 2 {
		t.Fatalf("projection did not use active G2: %+v", reference.VideoRecapture)
	}
	projected := make(map[identity.ID][]identity.ID, len(reference.VideoRecapture.Selected))
	for _, item := range reference.VideoRecapture.Selected {
		projected[item.ComponentID] = item.OriginFrameIDs
	}
	if len(projected[supplementComponentID]) != 1 || projected[supplementComponentID][0] != supplementFrameID {
		t.Fatalf("supplement component frames=%v; want %s", projected[supplementComponentID], supplementFrameID)
	}
	if len(projected[carriedComponentID]) != 1 || projected[carriedComponentID][0] != carriedFrameIDs[0] {
		t.Fatalf("carried G1 component frames=%v; want %s", projected[carriedComponentID], carriedFrameIDs[0])
	}
}

func TestVideoRecaptureReceiptDoesNotFulfillBeforePreparationUT053(t *testing.T) {
	f, originalID, componentID := videoRecaptureFixture(t)
	input := f.input("video:"+componentID.String(), "Mostre a pia", "video-pending")
	input.Items[0].OriginalMediaID = &originalID
	created, err := f.service.Request(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&database.MediaObject{
		ID: identity.NewID(), TenantID: f.tenantID, ResponsibilityID: created.ResponsibilityID,
		RequirementKey: "video:supplement", MediaKind: "VIDEO", Status: "RECEIVED",
		CreatedAt: f.now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Finalize(context.Background(), f.tenantID, created.RequestID, true); errorCode(err) != apperror.InvalidState {
		t.Fatalf("receipt without prepared evidence fulfilled recapture: %v", err)
	}
	var request database.RecaptureRequest
	if err := f.db.Where("tenant_id=? AND id=?", f.tenantID, created.RequestID).Take(&request).Error; err != nil {
		t.Fatal(err)
	}
	var selected database.RecaptureRequirement
	if err := f.db.Where("tenant_id=? AND request_id=?", f.tenantID, created.RequestID).Take(&selected).Error; err != nil {
		t.Fatal(err)
	}
	if request.Status != "REQUESTED" || selected.Status != "OPEN" {
		t.Fatalf("unprepared supplement changed fulfillment: request=%s selected=%s", request.Status, selected.Status)
	}
}

func TestVideoRecapturePersistsReceiptWithoutVisualFulfillment(t *testing.T) {
	f, originalID, componentID := videoRecaptureFixture(t)
	input := f.input("video:"+componentID.String(), "Mostre a pia", "video-received")
	input.Items[0].OriginalMediaID = &originalID
	created, err := f.service.Request(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	draft := database.CaptureDraft{ID: identity.NewID(), TenantID: f.tenantID, ResponsibilityID: created.ResponsibilityID, Kind: "RECAPTURE", MediaKind: "VIDEO", ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Status: "SUBMITTED", Requirements: mustMarshal([]capturecore.Requirement{{Key: "video:supplement", EvidenceKind: "VIDEO"}}), CreatedAt: f.now, UpdatedAt: f.now}
	if err := f.db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	mediaID := identity.NewID()
	if err := f.db.Create(&database.MediaObject{ID: mediaID, TenantID: f.tenantID, ResponsibilityID: created.ResponsibilityID, RequirementKey: "video:supplement", MediaKind: "VIDEO", Status: "RECEIVED", CreatedAt: f.now}).Error; err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(map[string]any{"mediaKind": "VIDEO", "mediaId": mediaID})
	if err != nil {
		t.Fatal(err)
	}
	submission := database.SubmissionVersion{ID: identity.NewID(), TenantID: f.tenantID, DraftID: draft.ID, ResponsibilityID: created.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: encoded, PayloadDigest: strings.Repeat("a", 64), SubmittedAt: f.now}
	if err := f.db.Create(&submission).Error; err != nil {
		t.Fatal(err)
	}
	request, err := f.service.ReceiveVideoTx(f.db, f.tenantID, created.RequestID, created.ResponsibilityID, submission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if request.Status != "RECEIVED" {
		t.Fatalf("receipt was not persisted distinctly: status=%s", request.Status)
	}
	var selected database.RecaptureRequirement
	if err := f.db.Where("tenant_id=? AND request_id=?", f.tenantID, request.ID).Take(&selected).Error; err != nil {
		t.Fatal(err)
	}
	if selected.Status != "OPEN" {
		t.Fatalf("receipt fulfilled an unprepared visual gap: status=%s", selected.Status)
	}
	if _, err := f.service.Finalize(context.Background(), f.tenantID, request.ID, true); errorCode(err) != apperror.InvalidState {
		t.Fatalf("receipt was mistaken for prepared visual coverage: %v", err)
	}
}
