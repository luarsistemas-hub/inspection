//go:build integration

package schedule_video_plan

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"inspection/libs/identity"
	buildrequest "inspection/services/inspection/internal/features/analysis/build_request"
	buildplan "inspection/services/inspection/internal/features/analysis/prepare_video_comparisons"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

type mismatchedSceneMatcher struct {
	originEnv, currentEnv     string
	originFrame, currentFrame identity.ID
	component                 identity.ID
}

func (m *mismatchedSceneMatcher) Propose(_ context.Context, origin, current []buildplan.Frame) ([]buildplan.Pair, error) {
	if len(origin) == 0 || len(current) == 0 {
		return nil, errors.New("missing scene frames")
	}
	m.originEnv, m.currentEnv, m.originFrame, m.currentFrame = origin[0].Environment, current[0].Environment, origin[0].ID, current[0].ID
	component, err := identity.ParseID(origin[0].Component)
	if err != nil {
		return nil, err
	}
	return []buildplan.Pair{{ComponentID: component, OriginFrameID: origin[0].ID, CurrentFrameID: current[0].ID, Decision: buildplan.SameView}}, nil
}
func (m *mismatchedSceneMatcher) Verify(_ context.Context, _, _ []buildplan.Frame, proposed []buildplan.Pair) ([]buildplan.Pair, error) {
	if len(proposed) == 0 {
		return nil, nil
	}
	pair := proposed[0]
	pair.Decision = buildplan.NotComparable
	return []buildplan.Pair{pair}, nil
}

func TestVV_IT_182KitchenBedroomMismatchBecomesInconclusive(t *testing.T) {
	f := newTask04PlanFixture(t, false, true)
	if err := f.h.AdminDB.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND id=?", f.tenantID, f.currentKitchenScope).Update("environment_candidate", "bedroom").Error; err != nil {
		t.Fatal(err)
	}
	matcher := &mismatchedSceneMatcher{}
	scheduler := newTask04Scheduler(t, f, matcher, matcher)
	if err := f.h.WithinTenant(f.ctx, f.tenantID, func(tx *gorm.DB) error { return scheduler.Handle(f.ctx, tx, f.submittedEvent(t)) }); err != nil {
		t.Fatal(err)
	}
	if worked, err := scheduler.RunDue(f.ctx); err != nil || !worked {
		t.Fatalf("RunDue=%t, %v", worked, err)
	}
	var item database.VideoPlanItem
	if err := f.h.AdminDB.Where("tenant_id=? AND plan_id IN (SELECT id FROM analysis.video_plans WHERE submission_id=?) AND component_id=?", f.tenantID, f.submissionID, f.kitchenID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	if matcher.originEnv != "kitchen" || matcher.currentEnv != "bedroom" || matcher.originFrame == matcher.currentFrame {
		t.Fatalf("matcher did not receive cross-room frames: %+v", matcher)
	}
	if item.Outcome != buildplan.Inconclusive || item.Reason != "NO_VERIFIED_CORRESPONDENCE" {
		t.Fatalf("rejected cross-room pairing=%s/%q, want INCONCLUSIVE/NO_VERIFIED_CORRESPONDENCE", item.Outcome, item.Reason)
	}
}

type recordingMatcher struct{ origin, current []identity.ID }

func (m *recordingMatcher) Propose(_ context.Context, origin, current []buildplan.Frame) ([]buildplan.Pair, error) {
	for _, frame := range origin {
		m.origin = append(m.origin, frame.ID)
	}
	for _, frame := range current {
		m.current = append(m.current, frame.ID)
	}
	component, err := identity.ParseID(origin[0].Component)
	if err != nil {
		return nil, err
	}
	return []buildplan.Pair{{ComponentID: component, OriginFrameID: origin[0].ID, CurrentFrameID: current[0].ID, Decision: buildplan.SameView}}, nil
}
func (m *recordingMatcher) Verify(_ context.Context, _, _ []buildplan.Frame, proposed []buildplan.Pair) ([]buildplan.Pair, error) {
	return proposed, nil
}

func TestVV_IT_186ActivationDuringAnalysisKeepsPinnedO1Frame(t *testing.T) {
	f := newTask04PlanFixture(t, false, true)
	var originFrame database.VideoFrame
	if err := f.h.AdminDB.Where("tenant_id=? AND preparation_id=?", f.tenantID, f.originPreparation).Take(&originFrame).Error; err != nil {
		t.Fatal(err)
	}
	var origin database.Origin
	if err := f.h.AdminDB.Where("tenant_id=? AND active_version_id=?", f.tenantID, f.originVersionID).Take(&origin).Error; err != nil {
		t.Fatal(err)
	}
	scheduler := newTask04Scheduler(t, f, &recordingMatcher{}, &recordingMatcher{})
	if err := f.h.WithinTenant(f.ctx, f.tenantID, func(tx *gorm.DB) error { return scheduler.Handle(f.ctx, tx, f.submittedEvent(t)) }); err != nil {
		t.Fatal(err)
	}
	var newer database.VideoPreparationJob
	var newerVersion identity.ID
	var currentMedia database.MediaObject
	if err := f.h.AdminDB.Where("tenant_id=? AND id=?", f.tenantID, originFrame.SourceMediaID).Take(&currentMedia).Error; err != nil {
		t.Fatal(err)
	}
	newer = database.VideoPreparationJob{ID: identity.NewID(), TenantID: f.tenantID, MediaID: currentMedia.ID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 2, ProfileDigest: "f6-it186-o2-profile", NextAttemptAt: time.Now().UTC(), ManifestDigest: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", PendingObjectKeys: []byte(`[]`), CreatedAt: time.Now().UTC()}
	newerVersion = identity.NewID()
	var oldResponsibility database.Responsibility
	if err := f.h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.tenantID, f.inspectionID).Take(&oldResponsibility).Error; err != nil {
		t.Fatal(err)
	}
	newResponsibility := database.Responsibility{ID: identity.NewID(), TenantID: f.tenantID, InspectionID: f.inspectionID, ParticipantID: oldResponsibility.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := f.h.WithinTenant(f.ctx, f.tenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&newer).Error; err != nil {
			return err
		}
		if err := tx.Create(&newResponsibility).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", f.tenantID, f.originVersionID).Update("status", "SUPERSEDED").Error; err != nil {
			return err
		}
		if err := tx.Create(&database.OriginVersion{ID: newerVersion, TenantID: f.tenantID, OriginID: origin.ID, VersionNumber: 2, ResponsibilityID: newResponsibility.ID, SupersedesID: &f.originVersionID, Status: "ACTIVE", MediaKind: "VIDEO", VideoPreparationID: &newer.ID, ScopeManifestDigest: newer.ManifestDigest, IdempotencyKey: "f6-it186-o2-" + newerVersion.String(), CreatedAt: time.Now().UTC(), SubmittedAt: ptrTask04(time.Now().UTC()), ActivatedAt: ptrTask04(time.Now().UTC())}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Origin{}).Where("tenant_id=? AND id=?", f.tenantID, origin.ID).Updates(map[string]any{"active_version_id": newerVersion, "version": 2}).Error
	}); err != nil {
		t.Fatal(err)
	}
	matcher := &recordingMatcher{}
	scheduler = newTask04Scheduler(t, f, matcher, matcher)
	if worked, err := scheduler.RunDue(f.ctx); err != nil || !worked {
		t.Fatalf("RunDue=%t, %v", worked, err)
	}
	if len(matcher.origin) != 1 || matcher.origin[0] != originFrame.ID {
		t.Fatalf("request followed newly active O2 instead of pinned O1 F01: frames=%v F01=%s", matcher.origin, originFrame.ID)
	}
}

type failingConditionGateway struct{ calls int }

func (g *failingConditionGateway) CompleteStructured(context.Context, llm.StructuredRequest) (llm.StructuredResult, error) {
	g.calls++
	return llm.StructuredResult{HTTPStatus: 503}, llm.NewError(llm.CodeProviderHTTP, 503, errors.New("test dependency unavailable"))
}

func TestVV_IT_187Condition503EndsTechnicalFailureWithoutNoChange(t *testing.T) {
	f := newTask04PlanFixture(t, false, true)
	plan := runTask04Plan(t, f)
	prepareTask04FrameObjects(t, f)
	var job database.ComparisonJob
	if err := f.h.AdminDB.Where("tenant_id=? AND video_plan_id=?", f.tenantID, plan.ID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	builder, err := buildrequest.Setup(buildrequest.Dependencies{Store: f.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	gateway := &failingConditionGateway{}
	process, err := processcomparison.Setup(processcomparison.Dependencies{Gateway: gateway, BuildRequest: builder, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(processcomparison.Payload{JobID: job.ID})
	for attempt := 0; attempt < 4; attempt++ {
		err = f.h.WithinTenant(f.ctx, f.tenantID, func(tx *gorm.DB) error {
			return process(messaging.WithAttempt(f.ctx, attempt), tx, payload, f.tenantID)
		})
		if attempt < 3 && err == nil {
			t.Fatalf("attempt %d unexpectedly succeeded", attempt)
		}
		if attempt == 3 && err != nil {
			t.Fatalf("terminal attempt failed to persist technical outcome: %v", err)
		}
	}
	var got database.ComparisonJob
	if err := f.h.AdminDB.Where("tenant_id=? AND id=?", f.tenantID, job.ID).Take(&got).Error; err != nil {
		t.Fatal(err)
	}
	var runs int64
	if err := f.h.AdminDB.Model(&database.AnalysisRun{}).Where("tenant_id=? AND job_id=?", f.tenantID, job.ID).Count(&runs).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != "FAILED" || runs != 1 || gateway.calls != 4 {
		t.Fatalf("503 did not terminate as technical failure: status=%s runs=%d calls=%d", got.Status, runs, gateway.calls)
	}
	var run database.AnalysisRun
	if err := f.h.AdminDB.Where("tenant_id=? AND job_id=?", f.tenantID, job.ID).Take(&run).Error; err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(run.Output, &output); err != nil {
		t.Fatal(err)
	}
	if run.ValidationOutcome != "FAILED" || output["noRelevantChange"] != nil {
		t.Fatalf("503 produced a non-technical/no-change result: outcome=%s output=%s", run.ValidationOutcome, run.Output)
	}
}

func TestVV_IT_190RetainedF01BuildsComparisonAfterOriginalExpiry(t *testing.T) {
	f := newTask04PlanFixture(t, false, true)
	plan := runTask04Plan(t, f)
	prepareTask04FrameObjects(t, f)
	var originPrep database.VideoPreparationJob
	if err := f.h.AdminDB.Where("tenant_id=? AND id=?", f.tenantID, f.originPreparation).Take(&originPrep).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := f.h.WithinTenant(f.ctx, f.tenantID, func(tx *gorm.DB) error {
		source := database.VideoSource{ID: identity.NewID(), TenantID: f.tenantID, MediaID: originPrep.MediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 1, Height: 1, FrameRate: 1, ScreeningStatus: "PASSED", ScreeningModelDigest: "qa", Status: "EXPIRED", OriginalDeletedAt: &now, CreatedAt: now, UpdatedAt: now}
		return tx.Create(&source).Error
	}); err != nil {
		t.Fatal(err)
	}
	var job database.ComparisonJob
	if err := f.h.AdminDB.Where("tenant_id=? AND video_plan_id=?", f.tenantID, plan.ID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	builder, err := buildrequest.Setup(buildrequest.Dependencies{Store: f.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	gateway := &task04ConditionGateway{categories: []string{"CONSERVATION"}}
	process, err := processcomparison.Setup(processcomparison.Dependencies{Gateway: gateway, BuildRequest: builder, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(processcomparison.Payload{JobID: job.ID})
	if err := f.h.WithinTenant(f.ctx, f.tenantID, func(tx *gorm.DB) error { return process(f.ctx, tx, payload, f.tenantID) }); err != nil {
		t.Fatal(err)
	}
	var originImage *llm.NormalizedImage
	for i := range gateway.request.Images {
		if gateway.request.Images[i].Source == "ORIGIN" {
			originImage = &gateway.request.Images[i]
		}
	}
	if originImage == nil || originImage.DataURL == "" || gateway.request.Mode != "COMPARE_ORIGIN_CURRENT" {
		t.Fatalf("retained F01 did not reach comparative condition request: %+v", gateway.request)
	}
	var got database.ComparisonJob
	if err := f.h.AdminDB.Where("tenant_id=? AND id=?", f.tenantID, job.ID).Take(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != "COMPLETED" {
		t.Fatalf("comparison using retained frame status=%s, want COMPLETED", got.Status)
	}
}

func ptrTask04(at time.Time) *time.Time { return &at }
