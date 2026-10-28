//go:build integration

package schedule_video_plan

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/features/analysis/prepare_video_comparisons"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	"inspection/services/inspection/internal/features/recapture/evaluate_video_coverage"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// task06Component is one origin component also filmed in the main video.
type task06Component struct {
	environment, component string
	id, originFrame        identity.ID
	mainFrame              identity.ID
	// mainQuality marks the main-video frame quality (LOW_QUALITY = blurred).
	mainQuality string
}

// task06Supplement is a sealed comparative G1 whose jobs are terminal, ready
// for focused video recapture cycles.
type task06Supplement struct {
	ctx                                  context.Context
	h                                    *harness.Harness
	fixture                              harness.Task06Fixture
	inspectionID, mainResponsibility     identity.ID
	mainDraft, mainSubmission, mainMedia identity.ID
	originPreparation, mainPreparation   identity.ID
	components                           []task06Component
	matcher                              integrationMatcher
	scheduler                            *Scheduler
	g1                                   database.VideoPlan
	digest                               string
	supplements                          int
}

func task06Labels(labels ...string) []task06Component {
	components := make([]task06Component, 0, len(labels))
	for _, label := range labels {
		parts := strings.SplitN(label, "/", 2)
		components = append(components, task06Component{environment: parts[0], component: parts[1]})
	}
	return components
}

func newTask06Supplement(t *testing.T, components []task06Component) *task06Supplement {
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
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := &task06Supplement{ctx: ctx, h: h, fixture: fixture, inspectionID: identity.NewID(), mainResponsibility: identity.NewID(), mainDraft: identity.NewID(), mainSubmission: identity.NewID(), mainMedia: identity.NewID(), originPreparation: identity.NewID(), mainPreparation: identity.NewID(), digest: strings.Repeat("ab", 32), matcher: integrationMatcher{componentByFrame: map[identity.ID]identity.ID{}}}
	for i := range components {
		components[i].id, components[i].originFrame, components[i].mainFrame = identity.NewID(), identity.NewID(), identity.NewID()
	}
	s.components = components
	now := time.Now().UTC().Truncate(time.Microsecond)
	originResponsibility, originMedia, originID, originVersionID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	mainPayload, _ := json.Marshal(map[string]any{"mediaKind": "VIDEO", "mediaId": s.mainMedia})
	requirements := json.RawMessage(`[{"key":"video:main","section":"inspection","label":"Video","instructions":"Record the inspection","evidenceKind":"VIDEO","required":true,"minimumMedia":1,"maximumMedia":1,"captureSourcePolicy":"CAMERA_DEFAULT"}]`)
	firstReady := now.Add(-time.Hour)
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		rows := []any{
			&database.Inspection{ID: s.inspectionID, TenantID: fixture.TenantID, BusinessUnitID: fixture.BusinessUnitID, AssetID: fixture.AssetID, ParticipantID: fixture.ParticipantID, TemplateID: fixture.TemplateID, TemplateVersionID: fixture.TemplateVersionID, AnalysisPromptSnapshotID: fixture.PromptSnapshotID, ProjectID: &fixture.ProjectID, StageID: &fixture.StageID, Source: "MANUAL", SourceKey: "task06-supplement-" + s.inspectionID.String(), Status: "SUBMITTED", MediaKind: "VIDEO", DueAt: now, DeadlineAt: now.Add(time.Hour), ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), Version: 1, CreatedAt: now, UpdatedAt: now},
			&database.Responsibility{ID: s.mainResponsibility, TenantID: fixture.TenantID, InspectionID: s.inspectionID, ParticipantID: fixture.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now},
			&database.CaptureDraft{ID: s.mainDraft, TenantID: fixture.TenantID, ResponsibilityID: s.mainResponsibility, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: fixture.TemplateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: requirements, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now},
			&database.MediaObject{ID: s.mainMedia, TenantID: fixture.TenantID, ResponsibilityID: s.mainResponsibility, ObjectKey: "task06-supplement/main-" + s.mainMedia.String() + ".mp4", ContentType: "video/mp4", SHA256: s.digest, SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "task06-main-" + s.mainMedia.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now},
			&database.SubmissionVersion{ID: s.mainSubmission, TenantID: fixture.TenantID, DraftID: s.mainDraft, ResponsibilityID: s.mainResponsibility, VersionNumber: 1, Complete: true, Payload: mainPayload, PayloadDigest: s.digest, SubmittedAt: now},
			&database.Responsibility{ID: originResponsibility, TenantID: fixture.TenantID, InspectionID: s.inspectionID, ParticipantID: fixture.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now},
			&database.MediaObject{ID: originMedia, TenantID: fixture.TenantID, ResponsibilityID: originResponsibility, ObjectKey: "task06-supplement/origin-" + originMedia.String() + ".mp4", ContentType: "video/mp4", SHA256: s.digest, SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "task06-origin-" + originMedia.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now},
			&database.VideoPreparationJob{ID: s.originPreparation, TenantID: fixture.TenantID, MediaID: originMedia, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, Attempts: 1, NextAttemptAt: now, ManifestDigest: s.digest, PendingObjectKeys: []byte("[]"), FirstReadyAt: &firstReady, CreatedAt: now},
			&database.VideoPreparationJob{ID: s.mainPreparation, TenantID: fixture.TenantID, MediaID: s.mainMedia, SubmissionID: &s.mainSubmission, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, Attempts: 1, NextAttemptAt: now, ManifestDigest: s.digest, PendingObjectKeys: []byte("[]"), FirstReadyAt: &firstReady, CreatedAt: now},
			&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, ActiveVersionID: &originVersionID, Version: 1, CreatedAt: now, UpdatedAt: now},
			&database.OriginVersion{ID: originVersionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: &originResponsibility, Status: "ACTIVE", MediaKind: "VIDEO", VideoPreparationID: &s.originPreparation, IdempotencyKey: "task06-origin-version-" + originVersionID.String(), CreatedAt: now, SubmittedAt: &now, ActivatedAt: &now},
			&database.ReferenceSnapshot{ID: identity.NewID(), TenantID: fixture.TenantID, InspectionID: s.inspectionID, ReferenceVersionID: &originVersionID, ComparisonMode: "VIDEO_MATCH", Payload: json.RawMessage(`{"comparisonMode":"VIDEO_MATCH"}`), CreatedAt: now},
		}
		for i, component := range s.components {
			rows = append(rows,
				&database.VideoFrame{ID: component.originFrame, TenantID: fixture.TenantID, SourceMediaID: originMedia, PreparationID: s.originPreparation, PTSMicros: int64(i) * 1_000_000, SHA256: s.digest, ObjectKey: "task06-supplement/origin-" + component.originFrame.String() + ".jpg", Width: 1, Height: 1, Role: "BASELINE", SizeBytes: 1, CreatedAt: now},
				&database.VideoFrame{ID: component.mainFrame, TenantID: fixture.TenantID, SourceMediaID: s.mainMedia, PreparationID: s.mainPreparation, PTSMicros: int64(i) * 1_000_000, Quality: component.mainQuality, SHA256: s.digest, ObjectKey: "task06-supplement/main-" + component.mainFrame.String() + ".jpg", Width: 1, Height: 1, Role: "BASELINE", SizeBytes: 1, CreatedAt: now},
				&database.VideoScopeItem{ID: component.id, TenantID: fixture.TenantID, PreparationID: s.originPreparation, FrameID: component.originFrame, EnvironmentCandidate: component.environment, ComponentLabel: component.component, ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now.Add(time.Duration(i) * time.Millisecond)},
				&database.VideoScopeItem{ID: identity.NewID(), TenantID: fixture.TenantID, PreparationID: s.mainPreparation, FrameID: component.mainFrame, EnvironmentCandidate: component.environment, ComponentLabel: component.component, ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now.Add(time.Duration(i) * time.Millisecond)},
			)
			s.matcher.componentByFrame[component.originFrame] = component.id
			s.matcher.componentByFrame[component.mainFrame] = component.id
		}
		for _, row := range rows {
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.scheduler, err = Setup(Dependencies{DB: h.DB, ClaimDB: h.AdminDB, Frames: prepare_video_comparisons.DBFrameReader{DB: h.DB}, Matcher: s.matcher, Verifier: s.matcher, Now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		payload, _ := json.Marshal(map[string]any{"draftId": s.mainDraft, "responsibilityId": s.mainResponsibility, "submissionId": s.mainSubmission, "mediaId": s.mainMedia, "preparationStatus": "READY"})
		return s.scheduler.Handle(ctx, tx, events.RawEnvelope{Type: "capture.video_submitted.v1", TenantID: fixture.TenantID, Payload: payload})
	}); err != nil {
		t.Fatal(err)
	}
	s.g1 = s.runPlan(t, 1)
	if s.g1.Status != "SEALED" {
		t.Fatalf("G1 status=%s error=%s", s.g1.Status, s.g1.ErrorCode)
	}
	if err := h.AdminDB.Model(&database.ComparisonJob{}).Where("tenant_id=? AND video_plan_id=?", fixture.TenantID, s.g1.ID).Update("status", "COMPLETED").Error; err != nil {
		t.Fatal(err)
	}
	return s
}

// runPlan repeats RunDue until the plan of the given generation has run.
func (s *task06Supplement) runPlan(t *testing.T, generation int) database.VideoPlan {
	t.Helper()
	var plan database.VideoPlan
	for attempt := 0; attempt < 5; attempt++ {
		if _, err := s.scheduler.RunDue(s.ctx); err != nil {
			t.Fatal(err)
		}
		err := s.h.AdminDB.Where("tenant_id=? AND inspection_id=? AND generation=?", s.fixture.TenantID, s.inspectionID, generation).Take(&plan).Error
		if err == nil && (plan.Attempts > 0 || plan.Status == "SEALED") {
			break
		}
	}
	return plan
}

func (s *task06Supplement) component(t *testing.T, label string) task06Component {
	t.Helper()
	for _, component := range s.components {
		if component.environment+"/"+component.component == label {
			return component
		}
	}
	t.Fatalf("unknown component %s", label)
	return task06Component{}
}

// request opens a focused video recapture for the labelled components.
func (s *task06Supplement) request(t *testing.T, key string, deadline time.Time, labels ...string) recapturecore.Result {
	t.Helper()
	items := make([]recapturecore.Item, 0, len(labels))
	for _, label := range labels {
		items = append(items, recapturecore.Item{RequirementKey: "video:" + s.component(t, label).id.String(), OriginalMediaID: &s.mainMedia, Reason: "Regravar " + label})
	}
	result, err := (recapturecore.Service{DB: s.h.DB}).Request(s.ctx, recapturecore.RequestInput{TenantID: s.fixture.TenantID, InspectionID: s.inspectionID, Items: items, Delivery: []invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "task06@example.test", Verified: true}}, DeadlineAt: deadline, IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// task06Shot is one supplement frame and the component the matcher sees in it.
type task06Shot struct {
	environment, component string
	matches                string // origin component label, "" for no match
	durationMicros         int64
}

type task06SupplementResult struct {
	media, submission, preparation identity.ID
	frames                         []identity.ID
}

// submitSupplement records a READY supplement for the request and runs the
// production coverage evaluator.
func (s *task06Supplement) submitSupplement(t *testing.T, request recapturecore.Result, shots []task06Shot) task06SupplementResult {
	t.Helper()
	r := s.recordSupplement(t, request, shots)
	s.evaluateCoverage(t, r)
	return r
}

// recordSupplement stores a READY supplement received by the request.
func (s *task06Supplement) recordSupplement(t *testing.T, request recapturecore.Result, shots []task06Shot) task06SupplementResult {
	t.Helper()
	s.supplements++
	r := task06SupplementResult{media: identity.NewID(), submission: identity.NewID(), preparation: identity.NewID()}
	now := time.Now().UTC().Truncate(time.Microsecond)
	var draft database.CaptureDraft
	if err := s.h.AdminDB.Where("tenant_id=? AND responsibility_id=? AND kind='RECAPTURE'", s.fixture.TenantID, request.ResponsibilityID).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	duration := int64(20_000_000)
	for _, shot := range shots {
		if shot.durationMicros > 0 {
			duration = shot.durationMicros
		}
	}
	firstReady := now
	days := 5
	expiry := firstReady.Add(5 * 24 * time.Hour)
	if err := s.h.WithinTenant(s.ctx, s.fixture.TenantID, func(tx *gorm.DB) error {
		payload, _ := json.Marshal(map[string]any{"mediaKind": "VIDEO", "mediaId": r.media})
		rows := []any{
			&database.MediaObject{ID: r.media, TenantID: s.fixture.TenantID, ResponsibilityID: request.ResponsibilityID, ObjectKey: "task06-supplement/supplement-" + r.media.String() + ".mp4", ContentType: "video/mp4", SHA256: s.digest, SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:supplement", IdempotencyKey: "task06-supplement-" + r.media.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now},
			&database.SubmissionVersion{ID: r.submission, TenantID: s.fixture.TenantID, DraftID: draft.ID, ResponsibilityID: request.ResponsibilityID, VersionNumber: s.supplements, Complete: true, Payload: payload, PayloadDigest: s.digest, SubmittedAt: now},
			&database.VideoPreparationJob{ID: r.preparation, TenantID: s.fixture.TenantID, MediaID: r.media, SubmissionID: &r.submission, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1 + s.supplements, Attempts: 1, NextAttemptAt: now, ManifestDigest: s.digest, PendingObjectKeys: []byte("[]"), FirstReadyAt: &firstReady, CreatedAt: now},
			&database.VideoSource{ID: identity.NewID(), TenantID: s.fixture.TenantID, MediaID: r.media, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: duration, Width: 96, Height: 64, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &firstReady, RetentionDays: &days, SourceExpiryAt: &expiry, Status: "RETAINED", CreatedAt: now, UpdatedAt: now},
		}
		for i, shot := range shots {
			frame := identity.NewID()
			r.frames = append(r.frames, frame)
			rows = append(rows,
				&database.VideoFrame{ID: frame, TenantID: s.fixture.TenantID, SourceMediaID: r.media, PreparationID: r.preparation, PTSMicros: int64(i) * 1_000_000, SHA256: s.digest, ObjectKey: "task06-supplement/supplement-" + frame.String() + ".jpg", Width: 1, Height: 1, Role: "SUPPLEMENT", SizeBytes: 1, CreatedAt: now},
				&database.VideoScopeItem{ID: identity.NewID(), TenantID: s.fixture.TenantID, PreparationID: r.preparation, FrameID: frame, EnvironmentCandidate: shot.environment, ComponentLabel: shot.component, ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now.Add(time.Duration(i) * time.Millisecond)},
			)
			if shot.matches != "" {
				s.matcher.componentByFrame[frame] = s.component(t, shot.matches).id
			}
		}
		for _, row := range rows {
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", s.fixture.TenantID, draft.ID).Update("status", "SUBMITTED").Error; err != nil {
			return err
		}
		_, err := (recapturecore.Service{}).ReceiveVideoTx(tx, s.fixture.TenantID, request.RequestID, request.ResponsibilityID, r.submission)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return r
}

// evaluateCoverage runs the production supplement coverage evaluator.
func (s *task06Supplement) evaluateCoverage(t *testing.T, r task06SupplementResult) {
	t.Helper()
	evaluator, err := evaluate_video_coverage.Setup(evaluate_video_coverage.Dependencies{DB: s.h.DB, ClaimDB: s.h.AdminDB, Frames: prepare_video_comparisons.DBFrameReader{DB: s.h.DB}, Matcher: s.matcher, Verifier: s.matcher, PlanRequests: SupplementalRequester{}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.VideoPreparedPayload{MediaID: r.media, PreparationID: r.preparation, ManifestDigest: s.digest})
	if err := s.h.WithinTenant(s.ctx, s.fixture.TenantID, func(tx *gorm.DB) error {
		return evaluator.HandlePrepared(s.ctx, tx, events.RawEnvelope{Type: "media.video_prepared.v1", TenantID: s.fixture.TenantID, Payload: payload})
	}); err != nil {
		t.Fatal(err)
	}
	// Other tests may leave coverage jobs due; run until this preparation's job completes.
	for attempt := 0; attempt < 5; attempt++ {
		if _, err := evaluator.RunDue(s.ctx); err != nil {
			t.Fatal(err)
		}
		var job database.VideoCoverageJob
		if err := s.h.AdminDB.Where("tenant_id=? AND preparation_id=?", s.fixture.TenantID, r.preparation).Take(&job).Error; err == nil && job.Status != "QUEUED" && job.Status != "PROCESSING" {
			break
		}
	}
}

func (s *task06Supplement) coverageJob(t *testing.T, preparation identity.ID) database.VideoCoverageJob {
	t.Helper()
	var job database.VideoCoverageJob
	if err := s.h.AdminDB.Where("tenant_id=? AND preparation_id=?", s.fixture.TenantID, preparation).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	return job
}

func (s *task06Supplement) recaptureState(t *testing.T, request recapturecore.Result) (database.RecaptureRequest, map[string]string) {
	t.Helper()
	var row database.RecaptureRequest
	if err := s.h.AdminDB.Where("tenant_id=? AND id=?", s.fixture.TenantID, request.RequestID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	var requirements []database.RecaptureRequirement
	if err := s.h.AdminDB.Where("tenant_id=? AND request_id=?", s.fixture.TenantID, request.RequestID).Find(&requirements).Error; err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, requirement := range requirements {
		statuses[requirement.RequirementKey] = requirement.Status
	}
	return row, statuses
}

func (s *task06Supplement) childPlan(t *testing.T) (database.VideoPlan, bool) {
	t.Helper()
	var plan database.VideoPlan
	err := s.h.AdminDB.Where("tenant_id=? AND inspection_id=? AND parent_plan_id IS NOT NULL", s.fixture.TenantID, s.inspectionID).Order("generation DESC").Take(&plan).Error
	if err == gorm.ErrRecordNotFound {
		return plan, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return plan, true
}
