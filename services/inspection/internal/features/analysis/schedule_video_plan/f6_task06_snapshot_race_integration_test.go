//go:build integration

package schedule_video_plan

import (
	"sync"
	"testing"
	"time"

	"inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/platform/database"
)

func TestVV_IT_259ConcurrentGenerationSnapshotsKeepPlanMembership(t *testing.T) {
	fixtures := []task04PlanFixture{
		newTask04PlanFixture(t, true, true),
		newTask04PlanFixture(t, true, true),
	}
	plans := make([]database.VideoPlan, len(fixtures))
	frames := make([]database.VideoFrame, len(fixtures))
	inputs := make([]generate_snapshot.Input, len(fixtures))
	for i, fixture := range fixtures {
		plans[i] = runTask04Plan(t, fixture)
		parkTask04Plans(t, fixture)
		deferTask04ForeignOutbox(t, fixture)
		if plans[i].Status != "SEALED" {
			t.Fatalf("generation %d plan status=%s, want SEALED", i+1, plans[i].Status)
		}
		if err := fixture.h.AdminDB.Model(&database.VideoFrame{}).
			Where("tenant_id=? AND id IN (SELECT frame_id FROM analysis.video_plan_item_frames WHERE tenant_id=? AND plan_item_id IN (SELECT id FROM analysis.video_plan_items WHERE tenant_id=? AND plan_id=?))", fixture.tenantID, fixture.tenantID, fixture.tenantID, plans[i].ID).
			Order("pts_us").Take(&frames[i]).Error; err != nil {
			t.Fatalf("generation %d plan member frame: %v", i+1, err)
		}
		inputs[i] = task04GapReportInput(fixture, plans[i], task04VideoRequirements(t, fixture, plans[i]), nil, []core.Evidence{task04FrameEvidence(frames[i])})
	}
	start := make(chan struct{})
	errCh := make(chan error, len(fixtures))
	var wg sync.WaitGroup
	for i := range fixtures {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			fixture := fixtures[i]
			_, err := generate_snapshot.Create(fixture.ctx, fixture.h.AdminDB, inputs[i], time.Now().UTC())
			errCh <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent snapshot generation failed: %v", err)
		}
	}
	for i, fixture := range fixtures {
		var snapshot database.ReportSnapshot
		if err := fixture.h.AdminDB.Where("tenant_id=? AND inspection_id=? AND video_plan_id=?", fixture.tenantID, fixture.inspectionID, plans[i].ID).Take(&snapshot).Error; err != nil {
			t.Fatalf("generation %d snapshot missing: %v", i+1, err)
		}
		got := task04StoredSnapshot(t, fixture, plans[i])
		if snapshot.VideoPlanID == nil || *snapshot.VideoPlanID != plans[i].ID || len(got.Evidence) != 1 || got.Evidence[0].FrameID != frames[i].ID.String() {
			t.Fatalf("generation %d snapshot membership leaked or changed: plan=%s snapshotPlan=%v evidence=%+v", i+1, plans[i].ID, snapshot.VideoPlanID, got.Evidence)
		}
		var memberCount int64
		if err := fixture.h.AdminDB.Model(&database.VideoPlanItemFrame{}).
			Joins("JOIN analysis.video_plan_items ON analysis.video_plan_items.id = analysis.video_plan_item_frames.plan_item_id AND analysis.video_plan_items.tenant_id = analysis.video_plan_item_frames.tenant_id").
			Where("analysis.video_plan_items.tenant_id=? AND analysis.video_plan_items.plan_id=? AND analysis.video_plan_item_frames.frame_id=?", fixture.tenantID, plans[i].ID, frames[i].ID).Count(&memberCount).Error; err != nil {
			t.Fatal(err)
		}
		if memberCount != 1 {
			t.Fatalf("snapshot %d evidence frame %s is not exclusively present in its plan membership", i+1, frames[i].ID)
		}
	}
}
