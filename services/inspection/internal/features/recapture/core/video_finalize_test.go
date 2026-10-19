package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
)

func videoFinalizeFixture(t *testing.T, prepare bool) (recaptureFixture, Result, identity.ID, identity.ID, identity.ID) {
	t.Helper()
	f, originalID, firstComponentID := videoRecaptureFixture(t)
	secondComponentID := identity.NewID()
	var firstItem database.VideoPlanItem
	if err := f.db.Where("tenant_id=? AND component_id=?", f.tenantID, firstComponentID).Take(&firstItem).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec("INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id,component_id,requirement_key,origin_frame_ids,current_frame_ids) SELECT ?,tenant_id,id,?,?,?,? FROM analysis.video_plans WHERE inspection_id=?", identity.NewID(), secondComponentID, "video:"+secondComponentID.String(), []byte(`[]`), firstItem.CurrentFrameIDs, f.inspectionID).Error; err != nil {
		t.Fatal(err)
	}
	input := f.input("video:"+firstComponentID.String(), "Mostre a pia", "finalize-video")
	input.Items = append(input.Items, Item{RequirementKey: "video:" + secondComponentID.String(), OriginalMediaID: &originalID, Reason: "Mostre o banheiro"})
	input.Items[0].OriginalMediaID = &originalID
	created, err := f.service.Request(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&database.RecaptureRequest{}).Where("tenant_id=? AND id=?", f.tenantID, created.RequestID).Update("status", "RECEIVED").Error; err != nil {
		t.Fatal(err)
	}
	draft := loadDraft(t, f.db, created.ResponsibilityID)
	mediaID := identity.NewID()
	media := database.MediaObject{ID: mediaID, TenantID: f.tenantID, ResponsibilityID: created.ResponsibilityID, RequirementKey: "video:supplement", MediaKind: "VIDEO", Status: "READY", CreatedAt: f.now.Add(2 * time.Hour)}
	if err := f.db.Create(&media).Error; err != nil {
		t.Fatal(err)
	}
	receiptPayload, err := json.Marshal(map[string]any{"mediaKind": "VIDEO", "mediaId": mediaID})
	if err != nil {
		t.Fatal(err)
	}
	submission := database.SubmissionVersion{ID: identity.NewID(), TenantID: f.tenantID, DraftID: draft.ID, ResponsibilityID: created.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: receiptPayload, PayloadDigest: strings.Repeat("a", 64), SubmittedAt: f.now}
	if err := f.db.Create(&submission).Error; err != nil {
		t.Fatal(err)
	}
	if prepare {
		preparationID := identity.NewID()
		if err := f.db.Exec("INSERT INTO media.video_preparation_jobs (id,tenant_id,media_id,status) VALUES (?,?,?,?)", preparationID, f.tenantID, mediaID, "READY").Error; err != nil {
			t.Fatal(err)
		}
	}
	return f, created, firstComponentID, secondComponentID, originalID
}

func TestFinalizeVideoTxCoveredComponentsAndReplay(t *testing.T) {
	f, created, firstComponentID, secondComponentID, _ := videoFinalizeFixture(t, true)
	f.service.Now = func() time.Time { return f.now.Add(2 * time.Hour) }
	first, err := f.service.FinalizeVideoTx(f.db, f.tenantID, created.RequestID, []identity.ID{firstComponentID})
	if err != nil || !first.Submitted || first.Request.Status != "SUBMITTED" || first.SupplementPreparationID == (identity.ID{}) {
		t.Fatalf("video recapture was not finalized: result=%+v err=%v", first, err)
	}
	var requirements []database.RecaptureRequirement
	if err := f.db.Where("tenant_id=? AND request_id=?", f.tenantID, created.RequestID).Order("requirement_key").Find(&requirements).Error; err != nil || len(requirements) != 2 {
		t.Fatalf("requirements not loaded: %+v err=%v", requirements, err)
	}
	corrected := 0
	for _, requirement := range requirements {
		if requirement.RequirementKey == "video:"+firstComponentID.String() && requirement.Status == "CORRECTED" {
			corrected++
		}
		if requirement.RequirementKey == "video:"+secondComponentID.String() && requirement.Status == "OPEN" {
			corrected++
		}
	}
	if corrected != 2 {
		t.Fatalf("partial coverage was not preserved: %+v", requirements)
	}
	var payload struct {
		Payload map[string]any `json:"payload"`
	}
	var eventsCount int64
	if err := f.db.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='recapture.completed.v1'", f.tenantID).Count(&eventsCount).Error; err != nil || eventsCount != 1 {
		t.Fatalf("completion event count=%d err=%v", eventsCount, err)
	}
	var event database.OutboxIntent
	if err := f.db.Where("tenant_id=? AND type='recapture.completed.v1'", f.tenantID).Take(&event).Error; err != nil || json.Unmarshal(event.Payload, &payload) != nil || payload.Payload["supplementPreparationId"] == nil {
		t.Fatalf("completion event is missing video coverage metadata: %+v err=%v", payload, err)
	}
	replay, err := f.service.FinalizeVideoTx(f.db, f.tenantID, created.RequestID, []identity.ID{firstComponentID})
	if err != nil || !replay.Submitted {
		t.Fatalf("same coverage replay failed: result=%+v err=%v", replay, err)
	}
	if _, err := f.service.FinalizeTx(f.db, f.tenantID, created.RequestID, true); errorCode(err) != apperror.InvalidState {
		t.Fatalf("generic finalizer accepted an already submitted video request: %v", err)
	}
	if err := f.db.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='recapture.completed.v1'", f.tenantID).Count(&eventsCount).Error; err != nil || eventsCount != 1 {
		t.Fatalf("replay duplicated completion event: count=%d err=%v", eventsCount, err)
	}
	if _, err := f.service.FinalizeVideoTx(f.db, f.tenantID, created.RequestID, []identity.ID{secondComponentID}); errorCode(err) != apperror.InvalidState {
		t.Fatalf("different coverage replay was accepted: %v", err)
	}
}

func TestFinalizeVideoTxRejectsReceiptAfterDeadline(t *testing.T) {
	f, created, componentID, _, _ := videoFinalizeFixture(t, true)
	var submission database.SubmissionVersion
	if err := f.db.Where("tenant_id=? AND responsibility_id=?", f.tenantID, created.ResponsibilityID).Take(&submission).Error; err != nil {
		t.Fatal(err)
	}
	deadline := f.now.Add(time.Hour)
	if err := f.db.Model(&submission).Update("submitted_at", deadline.Add(time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	f.service.Now = func() time.Time { return deadline.Add(2 * time.Hour) }
	if _, err := f.service.FinalizeVideoTx(f.db, f.tenantID, created.RequestID, []identity.ID{componentID}); errorCode(err) != apperror.InvalidState {
		t.Fatalf("late video receipt was accepted: %v", err)
	}
	assertRequestStatus(t, f.db, created.RequestID, "RECEIVED")
	var requirement database.RecaptureRequirement
	if err := f.db.Where("tenant_id=? AND request_id=?", f.tenantID, created.RequestID).Take(&requirement).Error; err != nil || requirement.Status != "OPEN" {
		t.Fatalf("late receipt changed requirement fulfillment: %+v err=%v", requirement, err)
	}
}

func TestFinalizeVideoTxRejectsUnselectedComponent(t *testing.T) {
	f, created, _, _, _ := videoFinalizeFixture(t, true)
	if _, err := f.service.FinalizeVideoTx(f.db, f.tenantID, created.RequestID, []identity.ID{identity.NewID()}); errorCode(err) != apperror.InvalidInput {
		t.Fatalf("unselected component was accepted: %v", err)
	}
	assertRequestStatus(t, f.db, created.RequestID, "RECEIVED")
}

func TestFinalizeVideoTxRequiresReadySupplementPreparation(t *testing.T) {
	f, created, componentID, _, _ := videoFinalizeFixture(t, false)
	if _, err := f.service.FinalizeVideoTx(f.db, f.tenantID, created.RequestID, []identity.ID{componentID}); errorCode(err) != apperror.InvalidState {
		t.Fatalf("supplement without READY preparation was accepted: %v", err)
	}
	assertRequestStatus(t, f.db, created.RequestID, "RECEIVED")
}

func TestFinalizeVideoTxEmptyCoverageDoesNotCloseRequest(t *testing.T) {
	f, created, _, _, _ := videoFinalizeFixture(t, true)
	result, err := f.service.FinalizeVideoTx(f.db, f.tenantID, created.RequestID, nil)
	if err != nil || result.Submitted || result.Request.Status != "RECEIVED" {
		t.Fatalf("empty coverage should remain pending: result=%+v err=%v", result, err)
	}
	assertRequestStatus(t, f.db, created.RequestID, "RECEIVED")
	var eventsCount int64
	if err := f.db.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='recapture.completed.v1'", f.tenantID).Count(&eventsCount).Error; err != nil || eventsCount != 0 {
		t.Fatalf("empty coverage emitted completion event: count=%d err=%v", eventsCount, err)
	}
}

func TestFinalizeTxStillRejectsVideoAndPhotoCompletes(t *testing.T) {
	f, created, _, _, _ := videoFinalizeFixture(t, true)
	if _, err := f.service.FinalizeTx(f.db, f.tenantID, created.RequestID, true); errorCode(err) != apperror.InvalidState {
		t.Fatalf("generic finalizer accepted video: %v", err)
	}

	photo, photoRequest := requestedFixture(t, 1)
	seedReplacement(t, photo, photoRequest, "requirement-1")
	result, err := photo.service.Finalize(context.Background(), photo.tenantID, photoRequest.RequestID, true)
	if err != nil || result.Status != "SUBMITTED" {
		t.Fatalf("photo finalization regressed: result=%+v err=%v", result, err)
	}
	var photoComparison database.OutboxIntent
	if err := photo.db.Where("tenant_id=? AND type='analysis.comparison_requested.v1'", photo.tenantID).Take(&photoComparison).Error; err != nil {
		t.Fatalf("photo comparison request event changed: %v", err)
	}
}

func TestVideoRecaptureExhaustedCyclesCloseUnresolvedRequirements(t *testing.T) {
	f, created, _, _, _ := videoFinalizeFixture(t, false)
	for cycle := 0; cycle < MaxRecaptureCycles-1; cycle++ {
		prior := database.RecaptureRequest{ID: identity.NewID(), TenantID: f.tenantID, InspectionID: f.inspectionID, ResponsibilityID: identity.NewID(), Status: "EXPIRED", DeadlineAt: f.now, IdempotencyKey: "prior-video-cycle-" + strings.Repeat("x", cycle+1), CreatedAt: f.now, UpdatedAt: f.now}
		if err := f.db.Create(&prior).Error; err != nil {
			t.Fatal(err)
		}
	}
	f.service.Now = func() time.Time { return f.now.Add(2 * time.Hour) }
	if _, err := f.service.FinalizeTx(f.db, f.tenantID, created.RequestID, false); err != nil {
		t.Fatalf("expire fifth video cycle: %v", err)
	}
	var unresolved int64
	if err := f.db.Model(&database.RecaptureRequirement{}).Where("tenant_id=? AND request_id=? AND status='UNRESOLVED'", f.tenantID, created.RequestID).Count(&unresolved).Error; err != nil || unresolved != 2 {
		t.Fatalf("fifth cycle did not close its uncovered requirements: count=%d err=%v", unresolved, err)
	}
	var reclassification database.OutboxIntent
	if err := f.db.Where("tenant_id=? AND type='analysis.comparison_completed.v1'", f.tenantID).Take(&reclassification).Error; err != nil {
		t.Fatalf("video deadline did not request active-plan reclassification: %v", err)
	}
	var envelope struct {
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(reclassification.Payload, &envelope); err != nil || envelope.Payload["force"] != true {
		t.Fatalf("video reclassification event lacks force flag: payload=%s err=%v", reclassification.Payload, err)
	}
}

func TestVideoRecaptureFifthCycleLeavesUncoveredScopeUnresolved(t *testing.T) {
	f, created, coveredID, _, _ := videoFinalizeFixture(t, true)
	for cycle := 0; cycle < MaxRecaptureCycles-1; cycle++ {
		prior := database.RecaptureRequest{ID: identity.NewID(), TenantID: f.tenantID, InspectionID: f.inspectionID, ResponsibilityID: identity.NewID(), Status: "SUBMITTED", DeadlineAt: f.now, IdempotencyKey: "prior-submitted-video-cycle-" + strings.Repeat("y", cycle+1), CreatedAt: f.now, UpdatedAt: f.now}
		if err := f.db.Create(&prior).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.service.FinalizeVideoTx(f.db, f.tenantID, created.RequestID, []identity.ID{coveredID}); err != nil {
		t.Fatalf("finalize fifth video cycle: %v", err)
	}
	var unresolved int64
	if err := f.db.Model(&database.RecaptureRequirement{}).Where("tenant_id=? AND request_id=? AND status='UNRESOLVED'", f.tenantID, created.RequestID).Count(&unresolved).Error; err != nil || unresolved != 1 {
		t.Fatalf("uncovered fifth-cycle scope was not marked unresolved: count=%d err=%v", unresolved, err)
	}
}
