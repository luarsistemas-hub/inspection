//go:build integration

package schedule_video_plan

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/features/analysis/prepare_video_comparisons"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestSupplementalPlanCarriesUnchangedResultIT237(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	componentA, componentB := identity.NewID(), identity.NewID()
	inspectionID, mainResponsibility, mainDraft := identity.NewID(), identity.NewID(), identity.NewID()
	originResponsibility, originMedia, originPreparation := identity.NewID(), identity.NewID(), identity.NewID()
	originID, originVersionID := identity.NewID(), identity.NewID()
	mainPreparation, mainFrameA, mainFrameB := identity.NewID(), identity.NewID(), identity.NewID()
	originFrameA, originFrameB := identity.NewID(), identity.NewID()
	mainSubmission := identity.NewID()
	mainMedia := identity.NewID()
	originalExpiredAt := now.Add(-time.Hour)
	firstFrameReadyAt := now.Add(-31 * 24 * time.Hour)
	originRetentionDays := 30
	mainPayload, _ := json.Marshal(map[string]any{"mediaKind": "VIDEO", "mediaId": mainMedia})
	mainSubmissionRow := database.SubmissionVersion{ID: mainSubmission, TenantID: fixture.TenantID, DraftID: mainDraft, ResponsibilityID: mainResponsibility, VersionNumber: 1, Complete: true, Payload: mainPayload, PayloadDigest: digest, SubmittedAt: now}
	requirements := json.RawMessage(`[{"key":"video:main","section":"inspection","label":"Video","instructions":"Record the inspection","evidenceKind":"VIDEO","required":true,"minimumMedia":1,"maximumMedia":1,"captureSourcePolicy":"CAMERA_DEFAULT"}]`)
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.Inspection{ID: inspectionID, TenantID: fixture.TenantID, BusinessUnitID: fixture.BusinessUnitID, AssetID: fixture.AssetID, ParticipantID: fixture.ParticipantID, TemplateID: fixture.TemplateID, TemplateVersionID: fixture.TemplateVersionID, AnalysisPromptSnapshotID: fixture.PromptSnapshotID, ProjectID: &fixture.ProjectID, StageID: &fixture.StageID, Source: "MANUAL", SourceKey: "it237-" + inspectionID.String(), Status: "SUBMITTED", MediaKind: "VIDEO", DueAt: now, DeadlineAt: now.Add(time.Hour), ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Responsibility{ID: mainResponsibility, TenantID: fixture.TenantID, InspectionID: inspectionID, ParticipantID: fixture.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.CaptureDraft{ID: mainDraft, TenantID: fixture.TenantID, ResponsibilityID: mainResponsibility, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: fixture.TemplateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: requirements, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mainMedia, TenantID: fixture.TenantID, ResponsibilityID: mainResponsibility, ObjectKey: "it237/main.mp4", ContentType: "video/mp4", SHA256: digest, SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "it237-main", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&mainSubmissionRow).Error; err != nil {
			return err
		}
		for _, row := range []any{
			&database.Responsibility{ID: originResponsibility, TenantID: fixture.TenantID, InspectionID: inspectionID, ParticipantID: fixture.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now},
			&database.MediaObject{ID: originMedia, TenantID: fixture.TenantID, ResponsibilityID: originResponsibility, ObjectKey: "it237/origin.mp4", ContentType: "video/mp4", SHA256: digest, SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "it237-origin", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now},
			&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: originMedia, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 96, Height: 64, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &firstFrameReadyAt, RetentionDays: &originRetentionDays, SourceExpiryAt: &originalExpiredAt, OriginalDeletedAt: &originalExpiredAt, Status: "EXPIRED", CreatedAt: firstFrameReadyAt, UpdatedAt: originalExpiredAt},
			&database.VideoPreparationJob{ID: originPreparation, TenantID: fixture.TenantID, MediaID: originMedia, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, Attempts: 1, NextAttemptAt: now, ManifestDigest: digest, PendingObjectKeys: []byte("[]"), FirstReadyAt: &now, CreatedAt: now},
			&database.VideoPreparationJob{ID: mainPreparation, TenantID: fixture.TenantID, MediaID: mainMedia, SubmissionID: &mainSubmission, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, Attempts: 1, NextAttemptAt: now, ManifestDigest: digest, PendingObjectKeys: []byte("[]"), FirstReadyAt: &now, CreatedAt: now},
			&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, ActiveVersionID: &originVersionID, Version: 1, CreatedAt: now, UpdatedAt: now},
			&database.OriginVersion{ID: originVersionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: &originResponsibility, Status: "ACTIVE", MediaKind: "VIDEO", VideoPreparationID: &originPreparation, IdempotencyKey: "it237-origin-version", CreatedAt: now, SubmittedAt: &now, ActivatedAt: &now},
		} {
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(&database.ReferenceSnapshot{ID: identity.NewID(), TenantID: fixture.TenantID, InspectionID: inspectionID, ReferenceVersionID: &originVersionID, ComparisonMode: "VIDEO_MATCH", Payload: json.RawMessage(`{"comparisonMode":"VIDEO_MATCH"}`), CreatedAt: now}).Error; err != nil {
			return err
		}
		for _, frame := range []database.VideoFrame{
			{ID: originFrameA, TenantID: fixture.TenantID, SourceMediaID: originMedia, PreparationID: originPreparation, PTSMicros: 0, SHA256: digest, ObjectKey: "it237/origin-a.jpg", Width: 1, Height: 1, Role: "BASELINE", SizeBytes: 1, CreatedAt: now},
			{ID: originFrameB, TenantID: fixture.TenantID, SourceMediaID: originMedia, PreparationID: originPreparation, PTSMicros: 1, SHA256: digest, ObjectKey: "it237/origin-b.jpg", Width: 1, Height: 1, Role: "BASELINE", SizeBytes: 1, CreatedAt: now},
			{ID: mainFrameA, TenantID: fixture.TenantID, SourceMediaID: mainMedia, PreparationID: mainPreparation, PTSMicros: 0, SHA256: digest, ObjectKey: "it237/main-a.jpg", Width: 1, Height: 1, Role: "BASELINE", SizeBytes: 1, CreatedAt: now},
			{ID: mainFrameB, TenantID: fixture.TenantID, SourceMediaID: mainMedia, PreparationID: mainPreparation, PTSMicros: 1, SHA256: digest, ObjectKey: "it237/main-b.jpg", Width: 1, Height: 1, Role: "BASELINE", SizeBytes: 1, CreatedAt: now},
		} {
			if err := tx.Create(&frame).Error; err != nil {
				return err
			}
		}
		for _, scope := range []database.VideoScopeItem{
			{ID: componentA, TenantID: fixture.TenantID, PreparationID: originPreparation, FrameID: originFrameA, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now},
			{ID: componentB, TenantID: fixture.TenantID, PreparationID: originPreparation, FrameID: originFrameB, EnvironmentCandidate: "bathroom", ComponentLabel: "shower", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now},
			{ID: identity.NewID(), TenantID: fixture.TenantID, PreparationID: mainPreparation, FrameID: mainFrameA, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now},
			{ID: identity.NewID(), TenantID: fixture.TenantID, PreparationID: mainPreparation, FrameID: mainFrameB, EnvironmentCandidate: "bathroom", ComponentLabel: "shower", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now},
		} {
			if err := tx.Create(&scope).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	matcher := integrationMatcher{componentByFrame: map[identity.ID]identity.ID{originFrameA: componentA, originFrameB: componentB, mainFrameA: componentA, mainFrameB: componentB}}
	scheduler, err := Setup(Dependencies{DB: h.DB, ClaimDB: h.AdminDB, Frames: prepare_video_comparisons.DBFrameReader{DB: h.DB}, Matcher: matcher, Verifier: matcher, Now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		payload, _ := json.Marshal(map[string]any{"draftId": mainDraft, "responsibilityId": mainResponsibility, "submissionId": mainSubmission, "mediaId": mainMedia, "preparationStatus": "READY"})
		return scheduler.Handle(ctx, tx, events.RawEnvelope{Type: "capture.video_submitted.v1", TenantID: fixture.TenantID, Payload: payload})
	}); err != nil {
		t.Fatal(err)
	}
	var g1 database.VideoPlan
	for attempt := 0; attempt < 5; attempt++ {
		worked, err := scheduler.RunDue(ctx)
		if err != nil || !worked {
			t.Fatalf("seal G1: worked=%t err=%v", worked, err)
		}
		if err := h.AdminDB.Where("tenant_id=? AND inspection_id=? AND generation=1", fixture.TenantID, inspectionID).Take(&g1).Error; err != nil {
			t.Fatal(err)
		}
		if g1.Attempts > 0 || g1.Status == "SEALED" {
			break
		}
	}
	if g1.OriginPreparationID == nil || *g1.OriginPreparationID != originPreparation || g1.ReferenceVersionID == nil || *g1.ReferenceVersionID != originVersionID {
		t.Fatalf("expired O1 was not retained in the comparative plan lineage: %+v", g1)
	}
	var g1Items []database.VideoPlanItem
	if err := h.AdminDB.Where("tenant_id=? AND plan_id=?", fixture.TenantID, g1.ID).Find(&g1Items).Error; err != nil || len(g1Items) != 2 {
		t.Fatalf("G1 items=%d err=%v", len(g1Items), err)
	}
	var retainedOriginFrames int64
	if err := h.AdminDB.Model(&database.VideoPlanItemFrame{}).Where("tenant_id=? AND plan_item_id IN ? AND frame_id IN ? AND role='ORIGIN'", fixture.TenantID, []identity.ID{g1Items[0].ID, g1Items[1].ID}, []identity.ID{originFrameA, originFrameB}).Count(&retainedOriginFrames).Error; err != nil {
		t.Fatal(err)
	}
	if retainedOriginFrames != 2 {
		t.Fatalf("expired O1 retained frames in sealed plan=%d, want 2", retainedOriginFrames)
	}
	if err := h.AdminDB.Model(&database.ComparisonJob{}).Where("tenant_id=? AND video_plan_id=?", fixture.TenantID, g1.ID).Update("status", "COMPLETED").Error; err != nil {
		t.Fatal(err)
	}
	var kitchenJob database.ComparisonJob
	if err := h.AdminDB.Where("tenant_id=? AND video_plan_id=? AND requirement_key=?", fixture.TenantID, g1.ID, "video:"+componentA.String()).Take(&kitchenJob).Error; err != nil {
		t.Fatal(err)
	}
	var original database.MediaObject
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.TenantID, mainMedia).Take(&original).Error; err != nil {
		t.Fatal(err)
	}
	created, err := (recapturecore.Service{DB: h.DB}).Request(ctx, recapturecore.RequestInput{TenantID: fixture.TenantID, InspectionID: inspectionID, Items: []recapturecore.Item{{RequirementKey: "video:" + componentB.String(), OriginalMediaID: &original.ID, Reason: "Regravar o chuveiro"}}, Delivery: []invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "task06@example.test", Verified: true}}, DeadlineAt: now.Add(time.Hour), IdempotencyKey: "it237-request"})
	if err != nil {
		t.Fatal(err)
	}
	var recaptureDraft database.CaptureDraft
	if err := h.AdminDB.Where("tenant_id=? AND responsibility_id=? AND kind='RECAPTURE'", fixture.TenantID, created.ResponsibilityID).Take(&recaptureDraft).Error; err != nil {
		t.Fatal(err)
	}
	supplementMedia, supplementSubmission, supplementPreparation := identity.NewID(), identity.NewID(), identity.NewID()
	supplementFrame := identity.NewID()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.MediaObject{ID: supplementMedia, TenantID: fixture.TenantID, ResponsibilityID: created.ResponsibilityID, ObjectKey: "it237/supplement.mp4", ContentType: "video/mp4", SHA256: digest, SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:supplement", IdempotencyKey: "it237-supplement", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"mediaKind": "VIDEO", "mediaId": supplementMedia})
		if err := tx.Create(&database.SubmissionVersion{ID: supplementSubmission, TenantID: fixture.TenantID, DraftID: recaptureDraft.ID, ResponsibilityID: created.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: payload, PayloadDigest: digest, SubmittedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: supplementPreparation, TenantID: fixture.TenantID, MediaID: supplementMedia, SubmissionID: &supplementSubmission, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 2, Attempts: 1, NextAttemptAt: now, ManifestDigest: digest, PendingObjectKeys: []byte("[]"), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: supplementFrame, TenantID: fixture.TenantID, SourceMediaID: supplementMedia, PreparationID: supplementPreparation, PTSMicros: 0, SHA256: digest, ObjectKey: "it237/supplement.jpg", Width: 1, Height: 1, Role: "SUPPLEMENT", SizeBytes: 1, CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fixture.TenantID, PreparationID: supplementPreparation, FrameID: supplementFrame, EnvironmentCandidate: "bathroom", ComponentLabel: "shower", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	matcher.componentByFrame[supplementFrame] = componentB
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", fixture.TenantID, recaptureDraft.ID).Update("status", "SUBMITTED").Error; err != nil {
			return err
		}
		if _, err := (recapturecore.Service{}).ReceiveVideoTx(tx, fixture.TenantID, created.RequestID, created.ResponsibilityID, supplementSubmission); err != nil {
			return err
		}
		finalized, err := (recapturecore.Service{}).FinalizeVideoTx(tx, fixture.TenantID, created.RequestID, []identity.ID{componentB})
		if err != nil || !finalized.Submitted || finalized.SupplementPreparationID != supplementPreparation {
			return fmt.Errorf("FinalizeVideoTx result=%+v err=%w", finalized, err)
		}
		return (SupplementalRequester{}).RequestSupplementalPlan(ctx, tx, fixture.TenantID, created.RequestID, g1.ID, supplementPreparation)
	}); err != nil {
		t.Fatal(err)
	}
	if worked, err := scheduler.RunDue(ctx); err != nil || !worked {
		t.Fatalf("seal G2: worked=%t err=%v", worked, err)
	}
	var g2 database.VideoPlan
	if err := h.AdminDB.Where("tenant_id=? AND parent_plan_id=?", fixture.TenantID, g1.ID).Take(&g2).Error; err != nil {
		t.Fatal(err)
	}
	if g2.Status != "SEALED" {
		t.Fatalf("G2 status=%s error=%s", g2.Status, g2.ErrorCode)
	}
	if g2.Status != "SEALED" || g2.Generation != 2 || g2.ParentPlanID == nil || *g2.ParentPlanID != g1.ID {
		t.Fatalf("unexpected G2 lineage: %+v", g2)
	}
	var preparations []database.VideoPlanPreparation
	if err := h.AdminDB.Where("tenant_id=? AND plan_id=?", fixture.TenantID, g2.ID).Find(&preparations).Error; err != nil {
		t.Fatal(err)
	}
	roles := map[string]bool{}
	for _, preparation := range preparations {
		roles[preparation.Role] = true
	}
	if !roles["MAIN"] || !roles["SUPPLEMENT"] {
		t.Fatalf("G2 preparation lineage missing MAIN or SUPPLEMENT: %+v", preparations)
	}
	var g2Items []database.VideoPlanItem
	if err := h.AdminDB.Where("tenant_id=? AND plan_id=?", fixture.TenantID, g2.ID).Find(&g2Items).Error; err != nil || len(g2Items) != 2 {
		t.Fatalf("G2 items=%d err=%v", len(g2Items), err)
	}
	itemByComponent := map[identity.ID]database.VideoPlanItem{}
	for _, item := range g2Items {
		itemByComponent[item.ComponentID] = item
	}
	if itemA, ok := itemByComponent[componentA]; !ok || itemA.CarriedResultJobID == nil || *itemA.CarriedResultJobID != kitchenJob.ID {
		t.Fatalf("G2 did not carry the completed A result: %+v", itemA)
	} else {
		for _, parentItem := range g1Items {
			if parentItem.ComponentID == componentA && (itemA.Outcome != parentItem.Outcome || itemA.Reason != parentItem.Reason) {
				t.Fatalf("G2 changed carried A result: parent=%+v child=%+v", parentItem, itemA)
			}
		}
	}
	var newJobs []database.ComparisonJob
	if err := h.AdminDB.Where("tenant_id=? AND video_plan_id=?", fixture.TenantID, g2.ID).Find(&newJobs).Error; err != nil || len(newJobs) != 1 || newJobs[0].RequirementKey != "video:"+componentB.String() {
		t.Fatalf("G2 comparison jobs=%+v err=%v", newJobs, err)
	}
	if itemB := itemByComponent[componentB]; itemB.CarriedResultJobID != nil {
		t.Fatalf("G2 incorrectly carried B: %+v", itemB)
	}
	var inspection database.Inspection
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.TenantID, inspectionID).Take(&inspection).Error; err != nil || inspection.ActiveVideoPlanID == nil || *inspection.ActiveVideoPlanID != g2.ID {
		t.Fatalf("active plan=%v err=%v; want G2 %s", inspection.ActiveVideoPlanID, err, g2.ID)
	}
	var corrected database.RecaptureRequirement
	if err := h.AdminDB.Where("tenant_id=? AND request_id=? AND requirement_key=?", fixture.TenantID, created.RequestID, "video:"+componentB.String()).Take(&corrected).Error; err != nil || corrected.Status != "CORRECTED" {
		t.Fatalf("B requirement status=%s err=%v", corrected.Status, err)
	}
	var request database.RecaptureRequest
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.TenantID, created.RequestID).Take(&request).Error; err != nil || request.Status != "SUBMITTED" {
		t.Fatalf("request status=%s err=%v", request.Status, err)
	}
	var completedEvents int64
	if err := h.AdminDB.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='recapture.completed.v1' AND correlation_id=?", fixture.TenantID, "recapture-complete-"+created.RequestID.String()).Count(&completedEvents).Error; err != nil || completedEvents != 1 {
		t.Fatalf("recapture completion event count=%d err=%v", completedEvents, err)
	}
}

func (m integrationMatcher) sameViewPairs(origin, current []prepare_video_comparisons.Frame) []prepare_video_comparisons.Pair {
	var pairs []prepare_video_comparisons.Pair
	for _, left := range origin {
		for _, right := range current {
			componentID, known := m.componentByFrame[left.ID]
			if !known || m.componentByFrame[right.ID] != componentID || !left.Visible || !right.Visible || left.View != right.View {
				continue
			}
			pairs = append(pairs, prepare_video_comparisons.Pair{ComponentID: componentID, OriginFrameID: left.ID, CurrentFrameID: right.ID, Decision: prepare_video_comparisons.SameView, Landmarks: []string{"visible-boundary"}})
		}
	}
	return pairs
}
