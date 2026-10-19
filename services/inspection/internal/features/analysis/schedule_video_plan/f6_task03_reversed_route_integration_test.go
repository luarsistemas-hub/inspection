//go:build integration

package schedule_video_plan

import (
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	buildplan "inspection/services/inspection/internal/features/analysis/prepare_video_comparisons"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestVV_IT_076ReversedRouteKeepsDistinctSinkTimestamps(t *testing.T) {
	fixture := newTask04PlanFixture(t, true, true)
	now := time.Now().UTC().Truncate(time.Microsecond)
	currentBathroomFrame := identity.NewID()
	mapper := map[identity.ID]identity.ID{fixture.currentFrame: fixture.kitchenID, currentBathroomFrame: fixture.bathroomID}
	// The origin walks kitchen (10s) then bathroom (40s); the current capture
	// walks the reversed route, bathroom (10s) then kitchen (70s).
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var scopes []database.VideoScopeItem
		if err := tx.Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.originPreparation).Find(&scopes).Error; err != nil {
			return err
		}
		for _, scope := range scopes {
			mapper[scope.FrameID] = scope.ID
			pts := int64(10_000_000)
			if scope.ID == fixture.bathroomID {
				pts = 40_000_000
			}
			if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND id=?", fixture.tenantID, scope.FrameID).Update("pts_us", pts).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.currentFrame).Update("pts_us", int64(70_000_000)).Error; err != nil {
			return err
		}
		var kitchen database.VideoFrame
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.currentFrame).Take(&kitchen).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: currentBathroomFrame, TenantID: fixture.tenantID, SourceMediaID: kitchen.SourceMediaID, PreparationID: fixture.currentPreparation, PTSMicros: 10_000_000, SHA256: kitchen.SHA256, ObjectKey: "f6-current-frame/" + currentBathroomFrame.String(), Width: 1, Height: 1, Role: "CURRENT", SizeBytes: 1, CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fixture.tenantID, PreparationID: fixture.currentPreparation, FrameID: currentBathroomFrame, EnvironmentCandidate: "bathroom", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	scheduler := newTask04Scheduler(t, fixture, integrationMatcher{componentByFrame: mapper}, integrationMatcher{componentByFrame: mapper})
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return scheduler.Handle(fixture.ctx, tx, fixture.submittedEvent(t)) }); err != nil {
		t.Fatal(err)
	}
	// RunDue claims the oldest due plan of any tenant; repeat until this plan has run.
	var plan database.VideoPlan
	for attempt := 0; attempt < 5; attempt++ {
		if worked, err := scheduler.RunDue(fixture.ctx); err != nil || !worked {
			t.Fatalf("RunDue = %t, %v", worked, err)
		}
		if err := fixture.h.AdminDB.Where("tenant_id=? AND submission_id=?", fixture.tenantID, fixture.submissionID).Take(&plan).Error; err != nil {
			t.Fatal(err)
		}
		if plan.Attempts > 0 || plan.Status == "SEALED" {
			break
		}
	}
	if plan.Status != "SEALED" {
		t.Fatalf("plan status=%s, want SEALED", plan.Status)
	}
	want := map[identity.ID][2]int64{fixture.kitchenID: {10_000_000, 70_000_000}, fixture.bathroomID: {40_000_000, 10_000_000}}
	for componentID, pts := range want {
		var item database.VideoPlanItem
		if err := fixture.h.AdminDB.Where("tenant_id=? AND plan_id=? AND component_id=?", fixture.tenantID, plan.ID, componentID).Take(&item).Error; err != nil {
			t.Fatal(err)
		}
		var groups [][]buildplan.Pair
		if err := json.Unmarshal(item.CoreGroups, &groups); err != nil {
			t.Fatal(err)
		}
		if item.Outcome != buildplan.Matched || len(groups) != 1 || len(groups[0]) != 1 || groups[0][0].ComponentID != componentID {
			t.Fatalf("component %s outcome=%s groups=%+v; want one accepted same-component pair", componentID, item.Outcome, groups)
		}
		var origin, current database.VideoFrame
		if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, groups[0][0].OriginFrameID).Take(&origin).Error; err != nil {
			t.Fatal(err)
		}
		if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, groups[0][0].CurrentFrameID).Take(&current).Error; err != nil {
			t.Fatal(err)
		}
		if origin.PTSMicros != pts[0] || current.PTSMicros != pts[1] || origin.PreparationID != fixture.originPreparation || current.PreparationID != fixture.currentPreparation {
			t.Fatalf("component %s pair origin=%d current=%d; want origin=%d current=%d from distinct preparations", componentID, origin.PTSMicros, current.PTSMicros, pts[0], pts[1])
		}
	}
}
