//go:build integration

package schedule_video_plan

import (
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	buildrequest "inspection/services/inspection/internal/features/analysis/build_request"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestVV_IT_145BlockedScreeningBeforeDispatchMakesNoImageGatewayRequest(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	plan := runTask04Plan(t, fixture)
	prepareTask04FrameObjects(t, fixture)
	job := task04PlanJob(t, fixture, plan)
	// A reviewer flags C1 after the plan was sealed and before dispatch.
	now := time.Now().UTC()
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.tenantID, MediaID: fixture.currentMediaID(), Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 1, Height: 1, FrameRate: 1, ScreeningStatus: "BLOCKED", Status: "RETAINED", CreatedAt: now, UpdatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	builder, err := buildrequest.Setup(buildrequest.Dependencies{Store: fixture.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	gateway := &countingTask04Gateway{inner: &task04ConditionGateway{categories: []string{"CONSERVATION"}}}
	processor, err := processcomparison.Setup(processcomparison.Dependencies{Gateway: gateway, BuildRequest: builder, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(processcomparison.Payload{JobID: job.ID})
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return processor(fixture.ctx, tx, payload, fixture.tenantID) }); err != nil {
		t.Fatal(err)
	}
	var got database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, job.ID).Take(&got).Error; err != nil {
		t.Fatal(err)
	}
	var findings int64
	if err := fixture.h.AdminDB.Model(&database.FindingRecord{}).Where("tenant_id=? AND analysis_run_id IN (SELECT id FROM analysis.analysis_runs WHERE job_id=?)", fixture.tenantID, job.ID).Count(&findings).Error; err != nil {
		t.Fatal(err)
	}
	if gateway.calls != 0 || findings != 0 || got.Status == "COMPLETED" {
		t.Fatalf("blocked C1 reached the image gateway: calls=%d findings=%d status=%s", gateway.calls, findings, got.Status)
	}
}
