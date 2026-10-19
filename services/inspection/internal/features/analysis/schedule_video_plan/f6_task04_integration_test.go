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
	buildplan "inspection/services/inspection/internal/features/analysis/prepare_video_comparisons"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/observability"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

func TestVV_IT_085SubmissionWithoutGuidanceKeepsOmittedBathroomInconclusive(t *testing.T) {
	fixture := newTask04PlanFixture(t, true, true)
	if fixture.guidanceOpened {
		t.Fatal("fixture unexpectedly opened video guidance")
	}
	plan := runTask04Plan(t, fixture)
	if plan.Status != "SEALED" {
		t.Fatalf("plan status=%s, want SEALED", plan.Status)
	}
	var item database.VideoPlanItem
	if err := fixture.h.AdminDB.Where("tenant_id=? AND plan_id=? AND component_id=?", fixture.tenantID, plan.ID, fixture.bathroomID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Outcome != buildplan.Inconclusive {
		t.Fatalf("omitted bathroom outcome=%s, want INCONCLUSIVE", item.Outcome)
	}
}

func TestVV_IT_159SubmittedEventWhilePreparationReceivedStaysPendingWithoutConditionCall(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, false)
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.currentPreparation).Updates(map[string]any{"status": "QUEUED", "phase": "RECEIVED"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	var calls int
	matcher := &countingTask04Matcher{calls: &calls}
	scheduler := newTask04Scheduler(t, fixture, matcher, matcher)
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return scheduler.Handle(fixture.ctx, tx, fixture.submittedEvent(t))
	}); err != nil {
		t.Fatal(err)
	}
	worked, err := scheduler.RunDue(fixture.ctx)
	if err != nil || !worked {
		t.Fatalf("pending plan execution = %t, %v", worked, err)
	}
	var plan database.VideoPlan
	if err := fixture.h.AdminDB.Where("tenant_id=? AND submission_id=?", fixture.tenantID, fixture.submissionID).Take(&plan).Error; err != nil {
		t.Fatal(err)
	}
	var jobs int64
	if err := fixture.h.AdminDB.Model(&database.ComparisonJob{}).Where("tenant_id=? AND inspection_id=?", fixture.tenantID, fixture.inspectionID).Count(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if plan.Status != "BUILDING" || plan.PlanDigest != "" || jobs != 0 || calls != 0 {
		t.Fatalf("received preparation ran condition work: plan=%s digest=%q jobs=%d matcherCalls=%d", plan.Status, plan.PlanDigest, jobs, calls)
	}
	// Park the waiting plan: RunDue claims across tenants, and a retry due
	// later would otherwise be claimed by another test's scheduler.
	if err := fixture.h.AdminDB.Model(&database.VideoPlan{}).Where("tenant_id=? AND id=?", fixture.tenantID, plan.ID).Update("next_attempt_at", time.Now().UTC().Add(24*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
}

func TestVV_IT_168MissingExpectedSinkViewIsPersistedInconclusive(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, false)
	// The fixed origin expects a kitchen sink, while the current frame bank has
	// no frame covering it.
	plan := runTask04Plan(t, fixture)
	var item database.VideoPlanItem
	if err := fixture.h.AdminDB.Where("tenant_id=? AND plan_id=? AND component_id=?", fixture.tenantID, plan.ID, fixture.kitchenID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Outcome != buildplan.Inconclusive || item.Reason != "NO_CURRENT_VIEW" {
		t.Fatalf("missing sink view=%s/%q, want INCONCLUSIVE/NO_CURRENT_VIEW", item.Outcome, item.Reason)
	}
}

func TestVV_IT_183MissingCurrentComponentDoesNotCreateConditionRequest(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, false)
	plan := runTask04Plan(t, fixture)
	var item database.VideoPlanItem
	if err := fixture.h.AdminDB.Where("tenant_id=? AND plan_id=? AND component_id=?", fixture.tenantID, plan.ID, fixture.kitchenID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	var jobs []database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND video_plan_id=? AND video_plan_item_id=?", fixture.tenantID, plan.ID, item.ID).Find(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if item.Outcome != buildplan.Inconclusive || len(jobs) != 1 || jobs[0].Status != "INCONCLUSIVE" {
		t.Fatalf("missing current frame must remain inconclusive without dispatching a condition request: outcome=%s reason=%s jobs=%+v", item.Outcome, item.Reason, jobs)
	}
	gateway := &failingConditionGateway{}
	processor, err := processcomparison.Setup(processcomparison.Dependencies{Gateway: gateway, BuildRequest: func(context.Context, *gorm.DB, database.ComparisonJob, processcomparison.Payload) (llm.StructuredRequest, error) {
		return llm.StructuredRequest{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(processcomparison.Payload{JobID: jobs[0].ID})
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return processor(fixture.ctx, tx, payload, fixture.tenantID) }); err != nil {
		t.Fatal(err)
	}
	if gateway.calls != 0 {
		t.Fatalf("inconclusive item issued %d condition requests", gateway.calls)
	}
}

func TestVV_IT_184MatchingBudgetExhaustionSealsWallUnprocessedWithoutConditionJob(t *testing.T) {
	fixture := newTask04PlanFixture(t, true, true)
	var kitchenScope database.VideoScopeItem
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.kitchenID).Take(&kitchenScope).Error; err != nil {
		t.Fatal(err)
	}
	var wallScope database.VideoScopeItem
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.bathroomID).Take(&wallScope).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&wallScope).Updates(map[string]any{"environment_candidate": "kitchen", "component_label": "wall", "created_at": kitchenScope.CreatedAt.Add(time.Second)}).Error; err != nil {
			return err
		}
		var currentPreparation database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.currentPreparation).Take(&currentPreparation).Error; err != nil {
			return err
		}
		currentWallFrame := identity.NewID()
		if err := tx.Create(&database.VideoFrame{ID: currentWallFrame, TenantID: fixture.tenantID, SourceMediaID: currentPreparation.MediaID, PreparationID: fixture.currentPreparation, PTSMicros: 8_000_000, SHA256: wallScopeDigest, ObjectKey: "f6-current-wall/" + currentWallFrame.String(), Width: 1, Height: 1, Role: "CURRENT", SizeBytes: 1, CreatedAt: kitchenScope.CreatedAt.Add(time.Second)}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fixture.tenantID, PreparationID: fixture.currentPreparation, FrameID: currentWallFrame, EnvironmentCandidate: "kitchen", ComponentLabel: "wall", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: kitchenScope.CreatedAt.Add(time.Second)}).Error
	}); err != nil {
		t.Fatal(err)
	}
	matcher := &budgetTask04Matcher{}
	metrics := observability.NewMetrics()
	scheduler, err := Setup(Dependencies{DB: fixture.h.DB, ClaimDB: fixture.h.AdminDB, Frames: buildplan.DBFrameReader{DB: fixture.h.DB}, Matcher: matcher, Verifier: matcher, MaxCalls: 2, Metrics: metrics, Now: func() time.Time { return time.Now().UTC() }})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return scheduler.Handle(fixture.ctx, tx, fixture.submittedEvent(t)) }); err != nil {
		t.Fatal(err)
	}
	worked, err := scheduler.RunDue(fixture.ctx)
	if err != nil || !worked {
		t.Fatalf("RunDue = %t, %v", worked, err)
	}
	var plan database.VideoPlan
	if err := fixture.h.AdminDB.Where("tenant_id=? AND submission_id=?", fixture.tenantID, fixture.submissionID).Take(&plan).Error; err != nil {
		t.Fatal(err)
	}
	var kitchen, wall database.VideoPlanItem
	if err := fixture.h.AdminDB.Where("tenant_id=? AND plan_id=? AND component_id=?", fixture.tenantID, plan.ID, fixture.kitchenID).Take(&kitchen).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.AdminDB.Where("tenant_id=? AND plan_id=? AND component_id=?", fixture.tenantID, plan.ID, fixture.bathroomID).Take(&wall).Error; err != nil {
		t.Fatal(err)
	}
	var wallJobs int64
	if err := fixture.h.AdminDB.Model(&database.ComparisonJob{}).Where("tenant_id=? AND video_plan_item_id=?", fixture.tenantID, wall.ID).Count(&wallJobs).Error; err != nil {
		t.Fatal(err)
	}
	if plan.Status != "SEALED" || kitchen.Outcome != buildplan.Matched || wall.Outcome != buildplan.TechnicalFailure || wall.Reason != "MATCH_BUDGET_EXHAUSTED" || wallJobs != 0 || matcher.calls != 3 || !strings.Contains(metrics.Prometheus(), "inspection_video_unprocessed_candidates_total 1") {
		t.Fatalf("budget exhaustion did not checkpoint and leave wall unprocessed: plan=%s kitchen=%s wall=%s/%q wallJobs=%d matcherCalls=%d metrics=%s", plan.Status, kitchen.Outcome, wall.Outcome, wall.Reason, wallJobs, matcher.calls, metrics.Prometheus())
	}
}

func TestVV_IT_199MatchingBudgetProcessesTwoOfThreeDiscoveredComponents(t *testing.T) {
	fixture := newTask04PlanFixture(t, true, true)
	now := time.Now().UTC()
	thirdComponentID, thirdCurrentFrameID, thirdCurrentScopeID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var origin database.VideoScopeItem
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.kitchenID).Take(&origin).Error; err != nil {
			return err
		}
		var originPrep database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.originPreparation).Take(&originPrep).Error; err != nil {
			return err
		}
		originFrameID := identity.NewID()
		if err := tx.Create(&database.VideoFrame{ID: originFrameID, TenantID: fixture.tenantID, SourceMediaID: originPrep.MediaID, PreparationID: fixture.originPreparation, PTSMicros: 9_000_000, SHA256: originFrameDigest, ObjectKey: "f6-it199-origin/" + originFrameID.String(), Width: 1, Height: 1, Role: "BASELINE", SizeBytes: 1, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoScopeItem{ID: thirdComponentID, TenantID: fixture.tenantID, PreparationID: fixture.originPreparation, FrameID: originFrameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now.Add(time.Second)}).Error; err != nil {
			return err
		}
		var currentPrep database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.currentPreparation).Take(&currentPrep).Error; err != nil {
			return err
		}
		var originBathroom database.VideoScopeItem
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.bathroomID).Take(&originBathroom).Error; err != nil {
			return err
		}
		if err := tx.Model(&originBathroom).Updates(map[string]any{"environment_candidate": "kitchen", "component_label": "sink", "view_label": "wide"}).Error; err != nil {
			return err
		}
		bathroomFrameID := identity.NewID()
		if err := tx.Create(&database.VideoFrame{ID: bathroomFrameID, TenantID: fixture.tenantID, SourceMediaID: currentPrep.MediaID, PreparationID: fixture.currentPreparation, PTSMicros: 8_000_000, SHA256: originFrameDigest, ObjectKey: "f6-it199-current/" + bathroomFrameID.String(), Width: 1, Height: 1, Role: "CURRENT", SizeBytes: 1, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fixture.tenantID, PreparationID: fixture.currentPreparation, FrameID: bathroomFrameID, EnvironmentCandidate: originBathroom.EnvironmentCandidate, ComponentLabel: originBathroom.ComponentLabel, ViewLabel: originBathroom.ViewLabel, Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: thirdCurrentFrameID, TenantID: fixture.tenantID, SourceMediaID: currentPrep.MediaID, PreparationID: fixture.currentPreparation, PTSMicros: 9_000_000, SHA256: originFrameDigest, ObjectKey: "f6-it199-current/" + thirdCurrentFrameID.String(), Width: 1, Height: 1, Role: "CURRENT", SizeBytes: 1, CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoScopeItem{ID: thirdCurrentScopeID, TenantID: fixture.tenantID, PreparationID: fixture.currentPreparation, FrameID: thirdCurrentFrameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now.Add(time.Second)}).Error
	}); err != nil {
		t.Fatal(err)
	}
	matcher := &budgetTask04Matcher{}
	scheduler, err := Setup(Dependencies{DB: fixture.h.DB, ClaimDB: fixture.h.AdminDB, Frames: buildplan.DBFrameReader{DB: fixture.h.DB}, Matcher: matcher, Verifier: matcher, MaxCalls: 4, Now: func() time.Time { return time.Now().UTC() }})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return scheduler.Handle(fixture.ctx, tx, fixture.submittedEvent(t)) }); err != nil {
		t.Fatal(err)
	}
	if worked, err := scheduler.RunDue(fixture.ctx); err != nil || !worked {
		t.Fatalf("RunDue = %t, %v", worked, err)
	}
	var plan database.VideoPlan
	if err := fixture.h.AdminDB.Where("tenant_id=? AND submission_id=?", fixture.tenantID, fixture.submissionID).Take(&plan).Error; err != nil {
		t.Fatal(err)
	}
	var items []database.VideoPlanItem
	if err := fixture.h.AdminDB.Where("tenant_id=? AND plan_id=?", fixture.tenantID, plan.ID).Order("created_at, component_id").Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	var processed, unprocessed int
	for _, item := range items {
		switch item.Outcome {
		case buildplan.Matched:
			processed++
		case buildplan.Inconclusive:
			processed++
		case buildplan.TechnicalFailure:
			if item.Reason != "MATCH_BUDGET_EXHAUSTED" {
				t.Errorf("unprocessed component reason=%q", item.Reason)
			}
			unprocessed++
		}
	}
	if plan.Status != "SEALED" || len(items) != 3 || processed != 2 || unprocessed != 1 || matcher.calls != 5 {
		t.Fatalf("three-component budget result: plan=%s items=%d processed=%d unprocessed=%d calls=%d (%+v)", plan.Status, len(items), processed, unprocessed, matcher.calls, items)
	}
}

const originFrameDigest = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

const wallScopeDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type budgetTask04Matcher struct{ calls int }

func (m *budgetTask04Matcher) Propose(ctx context.Context, origin, current []buildplan.Frame) ([]buildplan.Pair, error) {
	m.calls++
	if err := buildplan.ConsumeMatchCall(ctx); err != nil {
		return nil, err
	}
	if len(origin) == 0 || len(current) == 0 {
		return nil, nil
	}
	componentID, err := identity.ParseID(origin[0].Component)
	if err != nil {
		return nil, err
	}
	return []buildplan.Pair{{ComponentID: componentID, OriginFrameID: origin[0].ID, CurrentFrameID: current[0].ID, Decision: buildplan.SameView}}, nil
}

func (m *budgetTask04Matcher) Verify(ctx context.Context, _, _ []buildplan.Frame, proposed []buildplan.Pair) ([]buildplan.Pair, error) {
	m.calls++
	if err := buildplan.ConsumeMatchCall(ctx); err != nil {
		return nil, err
	}
	for i := range proposed {
		proposed[i].Landmarks = []string{"verified wall edge"}
	}
	return proposed, nil
}

func TestVV_IT_189CurrentReadyOriginNotReadyRemainsWaiting(t *testing.T) {
	fixture := newTask04PlanFixtureWithOriginStatus(t, false, true, "QUEUED")
	var calls int
	matcher := &countingTask04Matcher{calls: &calls}
	scheduler := newTask04Scheduler(t, fixture, matcher, matcher)
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return scheduler.Handle(fixture.ctx, tx, fixture.submittedEvent(t)) }); err != nil {
		t.Fatal(err)
	}
	worked, err := scheduler.RunDue(fixture.ctx)
	if err != nil || !worked {
		t.Fatalf("RunDue = %t, %v", worked, err)
	}
	var plan database.VideoPlan
	if err := fixture.h.AdminDB.Where("tenant_id=? AND submission_id=?", fixture.tenantID, fixture.submissionID).Take(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if plan.Status != "BUILDING" || plan.ErrorCode != "" || calls != 0 {
		t.Fatalf("origin prerequisite did not defer plan: status=%s error=%q matcherCalls=%d", plan.Status, plan.ErrorCode, calls)
	}
}

func TestVV_IT_176NineHundredAdjacentSinkViewsSealOneComponent(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	frameIDs := make([]string, 900)
	var first database.VideoFrame
	if err := fixture.h.AdminDB.Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.originPreparation).Take(&first).Error; err != nil {
		t.Fatal(err)
	}
	frameIDs[0] = first.ID.String()
	for i := 1; i < len(frameIDs); i++ {
		frameIDs[i] = identity.NewID().String()
	}
	observations, err := video.DiscoverScope(fixture.ctx, frameIDs, func(_ context.Context, batch []string) ([]video.ScopeObservation, []string, error) {
		result := make([]video.ScopeObservation, 0, len(batch))
		for _, frameID := range batch {
			result = append(result, video.ScopeObservation{FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE"})
		}
		return result, nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	consolidated, err := video.ConsolidateScope(frameIDs, observations)
	if err != nil {
		t.Fatal(err)
	}
	if len(consolidated) != 1 || consolidated[0].FrameID != frameIDs[0] || consolidated[0].ComponentLabel != "sink" {
		t.Fatalf("900 adjacent sink views were not represented by one deterministic representative: count=%d first=%+v", len(consolidated), consolidated)
	}
	// Persist the preparation result as the real scope stage does: 900 frame
	// observations become one representative VideoScopeItem. The durable plan
	// scheduler must then see that one scope component, not the observations.
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.originPreparation).Delete(&database.VideoScopeItem{}).Error; err != nil {
			return err
		}
		frames := make([]database.VideoFrame, 0, 899)
		for i := 1; i < len(frameIDs); i++ {
			frameID, err := identity.ParseID(frameIDs[i])
			if err != nil {
				return err
			}
			frames = append(frames, database.VideoFrame{ID: frameID, TenantID: fixture.tenantID, SourceMediaID: first.SourceMediaID, PreparationID: fixture.originPreparation, PTSMicros: int64(i) * 1_000_000, SHA256: first.SHA256, ObjectKey: "f6-it176-frame/" + frameIDs[i], Width: 1, Height: 1, Role: "BASELINE", SizeBytes: 1, CreatedAt: first.CreatedAt.Add(time.Duration(i) * time.Second)})
		}
		if err := tx.CreateInBatches(&frames, 100).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoScopeItem{ID: fixture.kitchenID, TenantID: fixture.tenantID, PreparationID: fixture.originPreparation, FrameID: first.ID, EnvironmentCandidate: consolidated[0].EnvironmentCandidate, ComponentLabel: consolidated[0].ComponentLabel, ViewLabel: consolidated[0].ViewLabel, Visibility: consolidated[0].Visibility, IdentityStatus: consolidated[0].IdentityStatus, Landmarks: json.RawMessage(`[]`), CreatedAt: first.CreatedAt}).Error
	}); err != nil {
		t.Fatal(err)
	}
	plan := runTask04Plan(t, fixture)
	var items []database.VideoPlanItem
	if err := fixture.h.AdminDB.Where("tenant_id=? AND plan_id=?", fixture.tenantID, plan.ID).Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ComponentID != fixture.kitchenID {
		t.Fatalf("sealed plan multiplied the unchanged sink: items=%+v", items)
	}
}

func TestVV_IT_178SealedPlanMatchesSinkAtOriginAndCurrentPTS(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.originPreparation).Update("pts_us", int64(10_000_000)).Error; err != nil {
			return err
		}
		return tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.currentPreparation).Update("pts_us", int64(70_000_000)).Error
	}); err != nil {
		t.Fatal(err)
	}
	plan := runTask04Plan(t, fixture)
	if plan.Status != "SEALED" {
		t.Fatalf("plan status=%s, want SEALED", plan.Status)
	}
	var item database.VideoPlanItem
	if err := fixture.h.AdminDB.Where("tenant_id=? AND plan_id=? AND component_id=?", fixture.tenantID, plan.ID, fixture.kitchenID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Outcome != buildplan.Matched {
		t.Fatalf("sink comparison outcome=%s, want MATCHED", item.Outcome)
	}
	var groups [][]buildplan.Pair
	if err := json.Unmarshal(item.CoreGroups, &groups); err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0]) != 1 || groups[0][0].ComponentID != fixture.kitchenID {
		t.Fatalf("sealed plan did not retain one same-component pair: %+v", groups)
	}
	var origin, current database.VideoFrame
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, groups[0][0].OriginFrameID).Take(&origin).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, groups[0][0].CurrentFrameID).Take(&current).Error; err != nil {
		t.Fatal(err)
	}
	if origin.PTSMicros != 10_000_000 || current.PTSMicros != 70_000_000 || groups[0][0].ComponentID != fixture.kitchenID {
		t.Fatalf("pair lost component/PTS provenance: origin=%d current=%d pair=%+v", origin.PTSMicros, current.PTSMicros, groups[0][0])
	}
}

type task04PlanFixture struct {
	ctx                                               context.Context
	h                                                 *harness.Harness
	tenantID, inspectionID, responsibilityID, draftID identity.ID
	assetID, submissionID, currentPreparation         identity.ID
	originPreparation, originVersionID                identity.ID
	kitchenID, bathroomID, currentKitchenScope        identity.ID
	currentFrame                                      identity.ID
	guidanceOpened                                    bool
}

func newTask04PlanFixture(t *testing.T, includeBathroom, includeKitchenCurrent bool) task04PlanFixture {
	return newTask04PlanFixtureWithOriginStatus(t, includeBathroom, includeKitchenCurrent, "READY")
}

func newTask04PlanFixtureWithOriginStatus(t *testing.T, includeBathroom, includeKitchenCurrent bool, originStatus string, currentOnly ...bool) task04PlanFixture {
	t.Helper()
	withoutOrigin := len(currentOnly) > 0 && currentOnly[0]
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	base, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	f := task04PlanFixture{ctx: ctx, h: h, tenantID: base.TenantID, inspectionID: identity.NewID(), responsibilityID: identity.NewID(), draftID: identity.NewID(), assetID: base.AssetID, submissionID: identity.NewID(), currentPreparation: identity.NewID(), originPreparation: identity.NewID(), originVersionID: identity.NewID(), kitchenID: identity.NewID(), bathroomID: identity.NewID(), currentKitchenScope: identity.NewID(), currentFrame: identity.NewID()}
	currentMediaID, originMediaID := identity.NewID(), identity.NewID()
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	err = h.WithinTenant(ctx, f.tenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.Inspection{ID: f.inspectionID, TenantID: f.tenantID, BusinessUnitID: base.BusinessUnitID, AssetID: f.assetID, ParticipantID: base.ParticipantID, TemplateID: base.TemplateID, TemplateVersionID: base.TemplateVersionID, AnalysisPromptSnapshotID: base.PromptSnapshotID, ProjectID: &base.ProjectID, StageID: &base.StageID, Source: "MANUAL", SourceKey: "f6-plan-" + f.inspectionID.String(), Status: "SUBMITTED", MediaKind: "VIDEO", DueAt: now, DeadlineAt: now.Add(time.Hour), ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Responsibility{ID: f.responsibilityID, TenantID: f.tenantID, InspectionID: f.inspectionID, ParticipantID: base.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.CaptureDraft{ID: f.draftID, TenantID: f.tenantID, ResponsibilityID: f.responsibilityID, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: base.TemplateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[]`), Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if !withoutOrigin {
			originResponsibility := identity.NewID()
			if err := tx.Create(&database.Responsibility{ID: originResponsibility, TenantID: f.tenantID, InspectionID: f.inspectionID, ParticipantID: base.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: f.tenantID, ResponsibilityID: originResponsibility, Kind: "ORIGIN", MediaKind: "VIDEO", TemplateVersionID: base.TemplateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[]`), Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.MediaObject{ID: originMediaID, TenantID: f.tenantID, ResponsibilityID: originResponsibility, ObjectKey: "f6-origin/" + originMediaID.String(), ContentType: "video/mp4", SHA256: digest, SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "f6-origin-" + originMediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
				return err
			}
			originPhase := "READY"
			if originStatus != "READY" {
				originPhase = "PROCESSING"
			}
			if err := tx.Create(&database.VideoPreparationJob{ID: f.originPreparation, TenantID: f.tenantID, MediaID: originMediaID, Status: originStatus, Phase: originPhase, PreparationVersion: 1, Generation: 1, NextAttemptAt: now, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
				return err
			}
			originID := identity.NewID()
			if err := tx.Create(&database.Origin{ID: originID, TenantID: f.tenantID, AssetID: f.assetID, ActiveVersionID: &f.originVersionID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.OriginVersion{ID: f.originVersionID, TenantID: f.tenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: originResponsibility, Status: "ACTIVE", MediaKind: "VIDEO", VideoPreparationID: &f.originPreparation, ScopeManifestDigest: digest, IdempotencyKey: "f6-origin-version-" + f.originVersionID.String(), CreatedAt: now, SubmittedAt: &now, ActivatedAt: &now}).Error; err != nil {
				return err
			}
			originFrame := identity.NewID()
			if err := tx.Create(&database.VideoFrame{ID: originFrame, TenantID: f.tenantID, SourceMediaID: originMediaID, PreparationID: f.originPreparation, PTSMicros: 0, SHA256: digest, ObjectKey: "f6-origin-frame/" + originFrame.String(), Width: 1, Height: 1, Role: "BASELINE", SizeBytes: 1, CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.VideoScopeItem{ID: f.kitchenID, TenantID: f.tenantID, PreparationID: f.originPreparation, FrameID: originFrame, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
				return err
			}
			if includeBathroom {
				bathroomFrame := identity.NewID()
				if err := tx.Create(&database.VideoFrame{ID: bathroomFrame, TenantID: f.tenantID, SourceMediaID: originMediaID, PreparationID: f.originPreparation, PTSMicros: 1, SHA256: digest, ObjectKey: "f6-origin-frame/" + bathroomFrame.String(), Width: 1, Height: 1, Role: "BASELINE", SizeBytes: 1, CreatedAt: now}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.VideoScopeItem{ID: f.bathroomID, TenantID: f.tenantID, PreparationID: f.originPreparation, FrameID: bathroomFrame, EnvironmentCandidate: "bathroom", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
					return err
				}
			}
			if err := tx.Create(&database.ReferenceSnapshot{ID: identity.NewID(), TenantID: f.tenantID, InspectionID: f.inspectionID, ReferenceVersionID: &f.originVersionID, ComparisonMode: "VIDEO_MATCH", Payload: json.RawMessage(`{"comparisonMode":"VIDEO_MATCH"}`), CreatedAt: now}).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Create(&database.ReferenceSnapshot{ID: identity.NewID(), TenantID: f.tenantID, InspectionID: f.inspectionID, ComparisonMode: "CHECKLIST_ONLY", Payload: json.RawMessage(`{"comparisonMode":"CHECKLIST_ONLY"}`), CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(&database.MediaObject{ID: currentMediaID, TenantID: f.tenantID, ResponsibilityID: f.responsibilityID, ObjectKey: "f6-current/" + currentMediaID.String(), ContentType: "video/mp4", SHA256: digest, SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "f6-current-" + currentMediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"mediaKind": "VIDEO", "mediaId": currentMediaID})
		if err := tx.Create(&database.SubmissionVersion{ID: f.submissionID, TenantID: f.tenantID, DraftID: f.draftID, ResponsibilityID: f.responsibilityID, VersionNumber: 1, Complete: true, Payload: payload, PayloadDigest: digest, SubmittedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: f.currentPreparation, TenantID: f.tenantID, MediaID: currentMediaID, SubmissionID: &f.submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		if includeKitchenCurrent {
			if err := tx.Create(&database.VideoFrame{ID: f.currentFrame, TenantID: f.tenantID, SourceMediaID: currentMediaID, PreparationID: f.currentPreparation, PTSMicros: 7_000_000, SHA256: digest, ObjectKey: "f6-current-frame/" + f.currentFrame.String(), Width: 1, Height: 1, Role: "CURRENT", SizeBytes: 1, CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.VideoScopeItem{ID: f.currentKitchenScope, TenantID: f.tenantID, PreparationID: f.currentPreparation, FrameID: f.currentFrame, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func runTask04Plan(t *testing.T, fixture task04PlanFixture) database.VideoPlan {
	t.Helper()
	mapper := map[identity.ID]identity.ID{}
	var originScopes []database.VideoScopeItem
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.originPreparation).Find(&originScopes).Error
	}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range originScopes {
		mapper[scope.FrameID] = scope.ID
	}
	if fixture.currentFrame != (identity.ID{}) {
		mapper[fixture.currentFrame] = fixture.kitchenID
	}
	scheduler := newTask04Scheduler(t, fixture, integrationMatcher{componentByFrame: mapper}, integrationMatcher{componentByFrame: mapper})
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return scheduler.Handle(fixture.ctx, tx, fixture.submittedEvent(t)) }); err != nil {
		t.Fatal(err)
	}
	// RunDue claims the oldest due plan of any tenant, so a plan left due by
	// another test may be claimed first; repeat until this plan has run.
	var plan database.VideoPlan
	for attempt := 0; attempt < 5; attempt++ {
		worked, err := scheduler.RunDue(fixture.ctx)
		if err != nil || !worked {
			t.Fatalf("RunDue = %t, %v", worked, err)
		}
		if err := fixture.h.AdminDB.Where("tenant_id=? AND submission_id=?", fixture.tenantID, fixture.submissionID).Take(&plan).Error; err != nil {
			t.Fatal(err)
		}
		if plan.Attempts > 0 || plan.Status == "SEALED" {
			break
		}
	}
	return plan
}

func newTask04Scheduler(t *testing.T, fixture task04PlanFixture, matcher buildplan.Proposer, verifier buildplan.Verifier) *Scheduler {
	t.Helper()
	scheduler, err := Setup(Dependencies{DB: fixture.h.DB, ClaimDB: fixture.h.AdminDB, Frames: buildplan.DBFrameReader{DB: fixture.h.DB}, Matcher: matcher, Verifier: verifier, Now: func() time.Time { return time.Now().UTC() }})
	if err != nil {
		t.Fatal(err)
	}
	return scheduler
}

func (f task04PlanFixture) submittedEvent(t *testing.T) events.RawEnvelope {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"draftId": f.draftID, "responsibilityId": f.responsibilityID, "submissionId": f.submissionID, "mediaId": f.currentMediaID(), "preparationStatus": "PENDING_VALIDATION"})
	if err != nil {
		t.Fatal(err)
	}
	return events.RawEnvelope{Type: "capture.video_submitted.v1", TenantID: f.tenantID, Payload: payload}
}

func (f task04PlanFixture) currentMediaID() identity.ID {
	var preparation database.VideoPreparationJob
	if err := f.h.AdminDB.Where("tenant_id=? AND id=?", f.tenantID, f.currentPreparation).Take(&preparation).Error; err != nil {
		return identity.ID{}
	}
	return preparation.MediaID
}

type countingTask04Matcher struct{ calls *int }

func (m *countingTask04Matcher) Propose(context.Context, []buildplan.Frame, []buildplan.Frame) ([]buildplan.Pair, error) {
	*m.calls++
	return nil, nil
}
func (m *countingTask04Matcher) Verify(context.Context, []buildplan.Frame, []buildplan.Frame, []buildplan.Pair) ([]buildplan.Pair, error) {
	*m.calls++
	return nil, nil
}
