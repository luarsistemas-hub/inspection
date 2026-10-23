package handle_classified

import (
	"context"
	"errors"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupRequiresNotificationAndSnapshotAdapters(t *testing.T) {
	if handler, err := Setup(Dependencies{}); err == nil || handler != nil {
		t.Fatalf("Setup() returned handler=%t err=%v, want missing dependency error", handler != nil, err)
	}
}

func TestClassifiedVideoJobsExcludePreviousPlanGenerations(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, statement := range []string{
		"ATTACH DATABASE ':memory:' AS analysis",
		"CREATE TABLE analysis.comparison_jobs (id TEXT, tenant_id TEXT, inspection_id TEXT, video_plan_id TEXT, requirement_key TEXT, video_core_group INTEGER)",
		"CREATE TABLE analysis.video_plan_items (id TEXT, tenant_id TEXT, plan_id TEXT, carried_result_job_id TEXT)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenant, inspection, oldPlan, currentPlan := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	oldJob, currentJob := identity.NewID(), identity.NewID()
	for _, row := range []struct{ id, plan identity.ID }{{oldJob, oldPlan}, {currentJob, currentPlan}} {
		if err := db.Exec("INSERT INTO analysis.comparison_jobs (id,tenant_id,inspection_id,video_plan_id,requirement_key,video_core_group) VALUES (?,?,?,?,?,?)", row.id, tenant, inspection, row.plan, "video:fireplace", 0).Error; err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := loadClassifiedJobs(context.Background(), db, tenant, inspection, &currentPlan)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != currentJob {
		t.Fatalf("video report included jobs from another plan: %+v", jobs)
	}
}

func TestReportIncludesFramesFromInconclusiveVideoItems(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, statement := range []string{
		"ATTACH DATABASE ':memory:' AS analysis",
		"ATTACH DATABASE ':memory:' AS media",
		"CREATE TABLE analysis.video_plan_items (id TEXT, tenant_id TEXT, plan_id TEXT, carried_result_job_id TEXT, requirement_key TEXT)",
		"CREATE TABLE analysis.video_plan_item_frames (id TEXT, tenant_id TEXT, plan_item_id TEXT, frame_id TEXT, role TEXT, core_group INTEGER, pair_order INTEGER)",
		"CREATE TABLE media.video_frames (id TEXT, tenant_id TEXT, source_media_id TEXT, preparation_id TEXT, pts_us INTEGER, sha256 TEXT)",
		"CREATE TABLE media.video_sources (id TEXT, tenant_id TEXT, media_id TEXT, status TEXT)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenant, plan := identity.NewID(), identity.NewID()
	source, preparation := identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO media.video_sources (id,tenant_id,media_id,status) VALUES (?,?,?,?)", identity.NewID(), tenant, source, "RETAINED").Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ key, role string }{{"video:gap", "ORIGIN"}, {"video:matched", "CURRENT"}} {
		itemID, frameID := identity.NewID(), identity.NewID()
		if err := db.Exec("INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id,requirement_key) VALUES (?,?,?,?)", itemID, tenant, plan, row.key).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO media.video_frames (id,tenant_id,source_media_id,preparation_id,pts_us,sha256) VALUES (?,?,?,?,?,?)", frameID, tenant, source, preparation, 1000000, "digest").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO analysis.video_plan_item_frames (id,tenant_id,plan_item_id,frame_id,role,core_group,pair_order) VALUES (?,?,?,?,?,?,?)", identity.NewID(), tenant, itemID, frameID, row.role, 0, 0).Error; err != nil {
			t.Fatal(err)
		}
	}
	evidence, err := reportAllVideoPlanEvidence(context.Background(), db, tenant, plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 2 || evidence[0].RequirementKey != "video:gap" || evidence[0].Role != "REFERENCE" || evidence[1].RequirementKey != "video:matched" || evidence[1].Role != "CURRENT" {
		t.Fatalf("video plan evidence omitted a gap or matched frame: %+v", evidence)
	}
}

func TestCarriedVideoEvidenceRequiresValidatedJob(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, statement := range []string{
		"ATTACH DATABASE ':memory:' AS analysis",
		"ATTACH DATABASE ':memory:' AS media",
		"CREATE TABLE analysis.video_plan_items (id TEXT, tenant_id TEXT, plan_id TEXT, carried_result_job_id TEXT, requirement_key TEXT)",
		"CREATE TABLE analysis.video_plan_item_frames (id TEXT, tenant_id TEXT, plan_item_id TEXT, frame_id TEXT, role TEXT, core_group INTEGER, pair_order INTEGER)",
		"CREATE TABLE media.video_frames (id TEXT, tenant_id TEXT, source_media_id TEXT, preparation_id TEXT, pts_us INTEGER, sha256 TEXT)",
		"CREATE TABLE media.video_sources (id TEXT, tenant_id TEXT, media_id TEXT, status TEXT)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenant, parentPlan, plan := identity.NewID(), identity.NewID(), identity.NewID()
	parentItem, carriedJob, frame, source := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id,requirement_key) VALUES (?,?,?,?)", []any{parentItem, tenant, parentPlan, "video:old-key"}},
		{"INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id,carried_result_job_id,requirement_key) VALUES (?,?,?,?,?)", []any{identity.NewID(), tenant, plan, carriedJob, "video:kitchen"}},
		{"INSERT INTO analysis.video_plan_item_frames (id,tenant_id,plan_item_id,frame_id,role,core_group,pair_order) VALUES (?,?,?,?,?,?,?)", []any{identity.NewID(), tenant, parentItem, frame, "CURRENT", 0, 0}},
		{"INSERT INTO media.video_frames (id,tenant_id,source_media_id,preparation_id,pts_us,sha256) VALUES (?,?,?,?,?,?)", []any{frame, tenant, source, identity.NewID(), 1000, "digest"}},
		{"INSERT INTO media.video_sources (id,tenant_id,media_id,status) VALUES (?,?,?,?)", []any{identity.NewID(), tenant, source, "RETAINED"}},
	} {
		if err := db.Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reportAllVideoPlanEvidence(context.Background(), db, tenant, plan, nil); !errors.Is(err, messaging.ErrPermanent) {
		t.Fatalf("carried item without a validated job = %v, want permanent error", err)
	}
	evidence, err := reportAllVideoPlanEvidence(context.Background(), db, tenant, plan, []database.ComparisonJob{{ID: carriedJob, VideoPlanItemID: &parentItem}})
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 1 || evidence[0].FrameID != frame.String() || evidence[0].RequirementKey != "video:kitchen" {
		t.Fatalf("carried item evidence = %+v", evidence)
	}
}
