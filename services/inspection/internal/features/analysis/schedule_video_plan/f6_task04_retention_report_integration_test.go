//go:build integration

package schedule_video_plan

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	buildrequest "inspection/services/inspection/internal/features/analysis/build_request"
	classify "inspection/services/inspection/internal/features/analysis/classify_inspection"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	expirevideosources "inspection/services/inspection/internal/features/retention/expire_video_sources"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestVV_IT_311ExpiredActiveOriginalStillFeedsNewComparisonFromRetainedF01(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	var originPrep database.VideoPreparationJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.originPreparation).Take(&originPrep).Error; err != nil {
		t.Fatal(err)
	}
	var originMedia database.MediaObject
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, originPrep.MediaID).Take(&originMedia).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.Store.Client.Put(fixture.ctx, fixture.h.Store.Bucket, originMedia.ObjectKey, strings.NewReader("origin"), 6, "video/mp4"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fixture.h.Store.Delete(context.Background(), originMedia.ObjectKey) })
	now := time.Now().UTC().Truncate(time.Microsecond)
	firstReady, due := now.Add(-31*24*time.Hour), now.Add(-24*time.Hour)
	days := 30
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.tenantID, MediaID: originPrep.MediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 1, Height: 1, FrameRate: 1, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &firstReady, RetentionDays: &days, SourceExpiryAt: &due, Status: "RETAINED", CreatedAt: firstReady, UpdatedAt: firstReady}).Error
	}); err != nil {
		t.Fatal(err)
	}
	expiry, err := expirevideosources.Setup(expirevideosources.Dependencies{DB: fixture.h.DB, Store: fixture.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	// ADR-019: the ACTIVE origin owns its source, so the expiry job keeps the
	// original even past its fixed date.
	if claimed, err := expiry.RunDue(fixture.ctx, fixture.tenantID, now, 10); err != nil || claimed != 0 {
		t.Fatalf("O1 original expiry claimed=%d err=%v; want 0 while the origin is active", claimed, err)
	}
	// The original can still disappear by other means (e.g. a legal deletion);
	// the retained frame bank must keep feeding new comparisons afterwards.
	if err := fixture.h.Store.Delete(fixture.ctx, originMedia.ObjectKey); err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.AdminDB.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", fixture.tenantID, originPrep.MediaID).Updates(map[string]any{"status": "EXPIRED", "original_deleted_at": now}).Error; err != nil {
		t.Fatal(err)
	}
	var source database.VideoSource
	if err := fixture.h.AdminDB.Where("tenant_id=? AND media_id=?", fixture.tenantID, originPrep.MediaID).Take(&source).Error; err != nil {
		t.Fatal(err)
	}
	if source.Status != "EXPIRED" || source.OriginalDeletedAt == nil {
		t.Fatalf("O1 original status=%s deletedAt=%v, want EXPIRED", source.Status, source.OriginalDeletedAt)
	}
	// V3 is a new comparison built after O1's original is gone.
	plan := runTask04Plan(t, fixture)
	prepareTask04FrameObjects(t, fixture)
	var job database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND video_plan_id=?", fixture.tenantID, plan.ID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	gateway := processTask04Job(t, fixture, job)
	var originFrames []string
	for _, image := range gateway.request.Images {
		if image.Source == "ORIGIN" && image.DataURL != "" {
			originFrames = append(originFrames, image.EvidenceID)
		}
	}
	var retained database.VideoFrame
	if len(originFrames) != 1 || fixture.h.AdminDB.Where("tenant_id=? AND id=? AND preparation_id=?", fixture.tenantID, originFrames[0], fixture.originPreparation).Take(&retained).Error != nil || gateway.request.Mode != "COMPARE_ORIGIN_CURRENT" {
		t.Fatalf("V3 did not compare against retained F01: %+v", gateway.request.Images)
	}
}

func TestVV_IT_262FinalReportWithPendingRequiredJobStaysPending(t *testing.T) {
	fixture := newTask04PlanFixture(t, true, true)
	plan := runTask04Plan(t, fixture)
	var kitchen database.VideoPlanItem
	if err := fixture.h.AdminDB.Where("tenant_id=? AND plan_id=? AND component_id=?", fixture.tenantID, plan.ID, fixture.kitchenID).Take(&kitchen).Error; err != nil {
		t.Fatal(err)
	}
	var job database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND video_plan_id=? AND video_plan_item_id=?", fixture.tenantID, plan.ID, kitchen.ID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status == "COMPLETED" || job.Status == "INCONCLUSIVE" || job.Status == "FAILED" {
		t.Fatalf("kitchen job is already terminal: %s", job.Status)
	}
	handler, err := classify.Setup(classify.Dependencies{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	requestFinal := func() error {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"inspectionId": fixture.inspectionID, "force": true})
		return fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
			return handler(fixture.ctx, tx, events.RawEnvelope{ID: identity.NewID(), Type: "analysis.comparison_completed.v1", TenantID: fixture.tenantID, CorrelationID: "vv-it262-" + identity.NewID().String(), Payload: payload})
		})
	}
	if err := requestFinal(); !errors.Is(err, classify.ErrClassificationPending) {
		t.Fatalf("pending kitchen job classification error = %v, want ErrClassificationPending", err)
	}
	if runs, classified := task04ClassificationState(t, fixture, plan.ID); runs != 0 || classified != 0 {
		t.Fatalf("pending kitchen job still produced classification=%d classifiedEvents=%d", runs, classified)
	}
	var snapshots int64
	if err := fixture.h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", fixture.tenantID, fixture.inspectionID).Count(&snapshots).Error; err != nil {
		t.Fatal(err)
	}
	if snapshots != 0 {
		t.Fatalf("final report became available with a pending job: %d snapshots", snapshots)
	}
	if requirement := findTask04Requirement(task04VideoRequirements(t, fixture, plan), "kitchen — sink"); requirement == nil || requirement.AnalysisStatus != "PENDING" {
		t.Fatalf("pending kitchen requirement = %+v, want PENDING", requirement)
	}
	prepareTask04FrameObjects(t, fixture)
	processTask04Job(t, fixture, job)
	if err := requestFinal(); err != nil {
		t.Fatalf("terminal kitchen job classification failed: %v", err)
	}
	if runs, classified := task04ClassificationState(t, fixture, plan.ID); runs != 1 || classified != 1 {
		t.Fatalf("terminal jobs produced classification=%d classifiedEvents=%d, want 1/1", runs, classified)
	}
}

func processTask04Job(t *testing.T, fixture task04PlanFixture, job database.ComparisonJob) *task04ConditionGateway {
	t.Helper()
	builder, err := buildrequest.Setup(buildrequest.Dependencies{Store: fixture.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	gateway := &task04ConditionGateway{categories: []string{"CONSERVATION"}}
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
	if got.Status != "COMPLETED" {
		t.Fatalf("job %s status=%s, want COMPLETED", job.ID, got.Status)
	}
	return gateway
}

func task04ClassificationState(t *testing.T, fixture task04PlanFixture, planID identity.ID) (int64, int64) {
	t.Helper()
	var runs, classified int64
	if err := fixture.h.AdminDB.Model(&database.ClassificationRun{}).Where("tenant_id=? AND inspection_id=? AND video_plan_id=?", fixture.tenantID, fixture.inspectionID, planID).Count(&runs).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.AdminDB.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type=? AND payload::text LIKE ?", fixture.tenantID, "inspection.classified.v1", "%"+fixture.inspectionID.String()+"%").Count(&classified).Error; err != nil {
		t.Fatal(err)
	}
	return runs, classified
}

func findTask04Requirement(requirements []reportcore.Requirement, label string) *reportcore.Requirement {
	for i := range requirements {
		if requirements[i].Label == label {
			return &requirements[i]
		}
	}
	return nil
}
