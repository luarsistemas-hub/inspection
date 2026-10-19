//go:build integration

package schedule_video_plan

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"

	"gorm.io/gorm"
)

// addTask07CurrentKitchenFrames adds frames of the current kitchen sink seen
// from distinct angles; scope grouping keeps one component per view, all with
// the "kitchen — sink" label.
func addTask07CurrentKitchenFrames(t *testing.T, fixture task04PlanFixture, count int) []identity.ID {
	t.Helper()
	var kitchen database.VideoFrame
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.currentFrame).Take(&kitchen).Error; err != nil {
		t.Fatal(err)
	}
	ids := []identity.ID{kitchen.ID}
	now := time.Now().UTC()
	for i := 1; i < count; i++ {
		frame := kitchen
		frame.ID, frame.PTSMicros, frame.ObjectKey, frame.CreatedAt = identity.NewID(), kitchen.PTSMicros+int64(i)*1_000_000, "f6-current-frame/"+identity.NewID().String(), now
		if err := fixture.h.AdminDB.Create(&frame).Error; err != nil {
			t.Fatal(err)
		}
		if err := fixture.h.AdminDB.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fixture.tenantID, PreparationID: fixture.currentPreparation, FrameID: frame.ID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "angle-" + strconv.Itoa(i), Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now.Add(time.Duration(i) * time.Millisecond)}).Error; err != nil {
			t.Fatal(err)
		}
		ids = append(ids, frame.ID)
	}
	return ids
}

func TestVV_IT_206TwentyFramesOfScratchYieldOneFindingWithSupportingFrames(t *testing.T) {
	fixture := newTask04PlanFixtureWithOriginStatus(t, false, true, "READY", true)
	frames := addTask07CurrentKitchenFrames(t, fixture, 20)
	plan := runTask04Plan(t, fixture)
	processTask07PlanJobs(t, fixture, plan, task07ClaimGateway{title: "Risco D1 visível"})
	snapshot, _ := task07WorkerReport(t, fixture, plan)
	if len(snapshot.Findings) != 1 {
		t.Fatalf("current-only report has %d findings for one scratch D1: %+v", len(snapshot.Findings), snapshot.Findings)
	}
	cited := map[string]bool{}
	for _, id := range snapshot.Findings[0].EvidenceIDs {
		cited[id] = true
	}
	for _, id := range frames {
		if !cited[id.String()] {
			t.Fatalf("D1 finding does not cite supporting frame %s (cites %d frames)", id, len(cited))
		}
	}
}

// task07ComponentGateway answers D2 for living-room frames and D1 otherwise.
type task07ComponentGateway struct{ livingRoom map[string]bool }

func (g task07ComponentGateway) CompleteStructured(ctx context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
	title := "Risco D1 visível"
	for _, image := range request.Images {
		if g.livingRoom[image.EvidenceID] {
			title = "Mancha D2 visível"
		}
	}
	return task07ClaimGateway{title: title}.CompleteStructured(ctx, request)
}

func TestVV_IT_264DuplicateD1ViewsConsolidateOnceKeepingD2AndGap(t *testing.T) {
	fixture := newTask04PlanFixtureWithOriginStatus(t, false, true, "READY", true)
	kitchen := addTask07CurrentKitchenFrames(t, fixture, 3)
	var base database.VideoFrame
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.currentFrame).Take(&base).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	living := map[string]bool{}
	for i, scope := range []struct{ environment, component, visibility string }{{"living room", "wall", "VISIBLE"}, {"bathroom", "basin", "OBSCURED"}} {
		frame := base
		frame.ID, frame.PTSMicros, frame.ObjectKey, frame.CreatedAt = identity.NewID(), base.PTSMicros+int64(100+i)*1_000_000, "f6-current-frame/"+identity.NewID().String(), now
		if err := fixture.h.AdminDB.Create(&frame).Error; err != nil {
			t.Fatal(err)
		}
		if err := fixture.h.AdminDB.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fixture.tenantID, PreparationID: fixture.currentPreparation, FrameID: frame.ID, EnvironmentCandidate: scope.environment, ComponentLabel: scope.component, ViewLabel: "wide", Visibility: scope.visibility, IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now.Add(time.Duration(i+10) * time.Second)}).Error; err != nil {
			t.Fatal(err)
		}
		if scope.environment == "living room" {
			living[frame.ID.String()] = true
		}
	}
	plan := runTask04Plan(t, fixture)
	processTask07PlanJobs(t, fixture, plan, task07ComponentGateway{livingRoom: living})
	snapshot, _ := task07WorkerReport(t, fixture, plan)
	var d1, d2 int
	for _, finding := range snapshot.Findings {
		switch finding.Title {
		case "Risco D1 visível":
			d1++
			if len(finding.EvidenceIDs) != len(kitchen) {
				t.Fatalf("consolidated D1 cites %d frames, want the %d kitchen views", len(finding.EvidenceIDs), len(kitchen))
			}
		case "Mancha D2 visível":
			d2++
		}
	}
	if d1 != 1 || d2 != 1 || len(snapshot.Findings) != 2 {
		t.Fatalf("findings=%+v; want D1 once and D2 retained", snapshot.Findings)
	}
	gap := task04Requirement(t, snapshot, "bathroom — basin")
	if gap.AnalysisStatus != "INCONCLUSIVE" {
		t.Fatalf("gap G=%+v; want retained inconclusive gap", gap)
	}
}

// runTask07PlanWithKitchenViews runs the plan with extra current frames that
// the matcher pairs with the origin kitchen sink, like runTask04Plan.
func runTask07PlanWithKitchenViews(t *testing.T, fixture task04PlanFixture, kitchenViews []identity.ID) database.VideoPlan {
	t.Helper()
	mapper := map[identity.ID]identity.ID{}
	var originScopes []database.VideoScopeItem
	if err := fixture.h.AdminDB.Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.originPreparation).Find(&originScopes).Error; err != nil {
		t.Fatal(err)
	}
	for _, scope := range originScopes {
		mapper[scope.FrameID] = scope.ID
	}
	for _, id := range kitchenViews {
		mapper[id] = fixture.kitchenID
	}
	scheduler := newTask04Scheduler(t, fixture, integrationMatcher{componentByFrame: mapper}, integrationMatcher{componentByFrame: mapper})
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return scheduler.Handle(fixture.ctx, tx, fixture.submittedEvent(t)) }); err != nil {
		t.Fatal(err)
	}
	var plan database.VideoPlan
	for attempt := 0; attempt < 5; attempt++ {
		if _, err := scheduler.RunDue(fixture.ctx); err != nil {
			t.Fatal(err)
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

func TestVV_IT_191RepeatedViewsOfD1UseOneCoreGroupAndOneFinding(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	var base database.VideoFrame
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.currentFrame).Take(&base).Error; err != nil {
		t.Fatal(err)
	}
	views := []identity.ID{base.ID}
	now := time.Now().UTC()
	// Repeated views of the same sink with distinct landmarks remain separate
	// scope items, all with the origin's component label and view.
	for i := 1; i < 3; i++ {
		frame := base
		frame.ID, frame.PTSMicros, frame.ObjectKey, frame.CreatedAt = identity.NewID(), base.PTSMicros+int64(i)*2_000_000, "f6-current-frame/"+identity.NewID().String(), now
		if err := fixture.h.AdminDB.Create(&frame).Error; err != nil {
			t.Fatal(err)
		}
		if err := fixture.h.AdminDB.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fixture.tenantID, PreparationID: fixture.currentPreparation, FrameID: frame.ID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`["angle-` + strconv.Itoa(i) + `"]`), CreatedAt: now.Add(time.Duration(i) * time.Second)}).Error; err != nil {
			t.Fatal(err)
		}
		views = append(views, frame.ID)
	}
	plan := runTask07PlanWithKitchenViews(t, fixture, views)
	var item database.VideoPlanItem
	if err := fixture.h.AdminDB.Where("tenant_id=? AND plan_id=? AND component_id=?", fixture.tenantID, plan.ID, fixture.kitchenID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	var groups [][]json.RawMessage
	if err := json.Unmarshal(item.CoreGroups, &groups); err != nil {
		t.Fatal(err)
	}
	var jobs []database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND video_plan_item_id=?", fixture.tenantID, item.ID).Find(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if item.Outcome != "MATCHED" || len(groups) != 1 || len(groups[0]) != len(views) || len(jobs) != 1 {
		t.Fatalf("kitchen item outcome=%s groups=%d pairs=%d jobs=%d; want one coherent core group of %d pairs in one job", item.Outcome, len(groups), len(groups[0]), len(jobs), len(views))
	}
	processTask07PlanJobs(t, fixture, plan, &task04ConditionGateway{categories: []string{"CONSERVATION"}})
	snapshot, _ := task07WorkerReport(t, fixture, plan)
	if len(snapshot.Findings) != 1 {
		t.Fatalf("report has %d findings for repeated views of D1: %+v", len(snapshot.Findings), snapshot.Findings)
	}
	cited := map[string]bool{}
	for _, id := range snapshot.Findings[0].EvidenceIDs {
		cited[id] = true
	}
	for _, id := range views {
		if !cited[id.String()] {
			t.Fatalf("consolidated D1 does not cite repeated view %s", id)
		}
	}
}
