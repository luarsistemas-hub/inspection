//go:build integration

package schedule_video_plan

import (
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/platform/database"
)

func TestVV_IT_188ReplayedPlanItemComparisonDoesNotDuplicateResultOrReport(t *testing.T) {
	request := newTask04VideoRequest(t)
	consumer := request.consumer(request.fixture.h.DB, "vv-it188-"+identity.NewID().String())
	envelope := request.envelope(request.fixture.tenantID, request.payload())
	for delivery := 0; delivery < 2; delivery++ {
		if _, err := processTask04Delivery(t, consumer, envelope); err != nil {
			t.Fatalf("delivery %d: %v", delivery+1, err)
		}
	}
	if status, runs := request.state(t); status != "COMPLETED" || runs != 1 || request.gateway.calls != 1 {
		t.Fatalf("replayed comparison status=%s runs=%d calls=%d; want one result", status, runs, request.gateway.calls)
	}
	var plan database.VideoPlan
	if err := request.fixture.h.AdminDB.Where("tenant_id=? AND id=?", request.fixture.tenantID, *request.job.VideoPlanID).Take(&plan).Error; err != nil {
		t.Fatal(err)
	}
	first, err := generate_snapshot.Create(request.fixture.ctx, request.fixture.h.AdminDB, task04GapReportInput(request.fixture, plan, task04VideoRequirements(t, request.fixture, plan), nil, nil), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// The replayed delivery is acknowledged by the inbox; report generation
	// for the same result is idempotent.
	if processed, err := processTask04Delivery(t, consumer, envelope); processed || err != nil {
		t.Fatalf("third delivery processed=%t err=%v; want inbox replay", processed, err)
	}
	second, err := generate_snapshot.Create(request.fixture.ctx, request.fixture.h.AdminDB, task04GapReportInput(request.fixture, plan, task04VideoRequirements(t, request.fixture, plan), nil, nil), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var versions int64
	if err := request.fixture.h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", request.fixture.tenantID, request.fixture.inspectionID).Count(&versions).Error; err != nil {
		t.Fatal(err)
	}
	if first.SnapshotID != second.SnapshotID || versions != 1 {
		t.Fatalf("replay duplicated the visible report: first=%s second=%s versions=%d", first.SnapshotID, second.SnapshotID, versions)
	}
}

func TestVV_IT_261ReplayedSnapshotRequestForSealedG1ReturnsSameVersion(t *testing.T) {
	fixture := newTask04PlanFixture(t, true, true)
	plan := runTask04Plan(t, fixture)
	results := make([]generate_snapshot.Result, 0, 3)
	for replay := 0; replay < 3; replay++ {
		result, err := generate_snapshot.Create(fixture.ctx, fixture.h.AdminDB, task04GapReportInput(fixture, plan, task04VideoRequirements(t, fixture, plan), nil, nil), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, result)
	}
	var versions int64
	if err := fixture.h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=? AND video_plan_id=?", fixture.tenantID, fixture.inspectionID, plan.ID).Count(&versions).Error; err != nil {
		t.Fatal(err)
	}
	for _, result := range results[1:] {
		if result.SnapshotID != results[0].SnapshotID || result.Version != 1 || result.JSONDigest != results[0].JSONDigest {
			t.Fatalf("replayed G1 snapshot request returned another snapshot: %+v vs %+v", result, results[0])
		}
	}
	if versions != 1 {
		t.Fatalf("replayed G1 snapshot requests stored %d versions", versions)
	}
}
