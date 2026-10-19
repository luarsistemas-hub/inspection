package main

import (
	"context"
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestReportVideoFrameEvidenceKeepsExactPTSUT055(t *testing.T) {
	frameID, sourceID, preparationID := identity.NewID(), identity.NewID(), identity.NewID()
	item := database.VideoPlanItem{RequirementKey: "video:sink"}
	membership := database.VideoPlanItemFrame{Role: "ORIGIN"}
	frame := database.VideoFrame{ID: frameID, SourceMediaID: sourceID, PreparationID: preparationID, PTSMicros: 70123456, SHA256: strings.Repeat("a", 64)}
	source := database.VideoSource{Status: "EXPIRED"}
	evidence := reportVideoFrameEvidence(item, membership, frame, source)
	if evidence.PTSMicros == nil || *evidence.PTSMicros != 70123456 || evidence.TimestampMS == nil || *evidence.TimestampMS != 70123 {
		t.Fatalf("exact PTS or display timestamp lost: %+v", evidence)
	}
	if evidence.FrameID != frameID.String() || evidence.SourceMediaID != sourceID.String() || evidence.PreparationID != preparationID.String() || evidence.Role != "REFERENCE" || evidence.SourceAvailability != "EXPIRED" {
		t.Fatalf("frame lineage or origin role lost: %+v", evidence)
	}
}

func TestReportG2IncludesCarriedG1AndNewComponentJobs(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS analysis").Error; err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE analysis.video_plans (id text PRIMARY KEY, tenant_id text, inspection_id text, parent_plan_id text, status text, generation integer)`,
		`CREATE TABLE analysis.video_plan_items (id text PRIMARY KEY, tenant_id text, plan_id text, component_id text, carried_result_job_id text, requirement_key text, outcome text, reason text, input_digest text, origin_frame_ids blob, current_frame_ids blob, core_groups blob, current_groups blob, created_at datetime)`,
		`CREATE TABLE analysis.comparison_jobs (id text PRIMARY KEY, tenant_id text, inspection_id text, video_plan_id text, video_plan_item_id text, video_core_group integer, requirement_key text, status text, updated_at datetime)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, inspectionID, g1ID, g2ID, carryID, newID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO analysis.video_plans (id,tenant_id,inspection_id,parent_plan_id,status,generation) VALUES (?,?,?,?,?,?)`, g1ID, tenantID, inspectionID, nil, "SEALED", 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO analysis.video_plans (id,tenant_id,inspection_id,parent_plan_id,status,generation) VALUES (?,?,?,?,?,?)`, g2ID, tenantID, inspectionID, g1ID, "SEALED", 2).Error; err != nil {
		t.Fatal(err)
	}
	for _, job := range []struct {
		id, plan identity.ID
		key      string
	}{{carryID, g1ID, "video:kitchen"}, {newID, g2ID, "video:bathroom"}} {
		if err := db.Exec(`INSERT INTO analysis.comparison_jobs (id,tenant_id,inspection_id,video_plan_id,requirement_key,status,updated_at) VALUES (?,?,?,?,?,?,CURRENT_TIMESTAMP)`, job.id, tenantID, inspectionID, job.plan, job.key, "COMPLETED").Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id,component_id,carried_result_job_id,requirement_key,outcome,reason,input_digest,origin_frame_ids,current_frame_ids,core_groups,current_groups,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP)`, identity.NewID(), tenantID, g2ID, identity.NewID(), carryID, "video:kitchen", "MATCHED", "", "digest", []byte("[]"), []byte("[]"), []byte("[]"), []byte("[]")).Error; err != nil {
		t.Fatal(err)
	}
	jobs, err := loadReportVideoPlanJobs(context.Background(), db, tenantID, inspectionID, g2ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].ID != newID && jobs[1].ID != newID || jobs[0].ID != carryID && jobs[1].ID != carryID {
		t.Fatalf("G2 report jobs did not include carried kitchen and new bathroom results: %+v", jobs)
	}
}
