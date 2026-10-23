package classify_inspection

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	analysis "inspection/services/inspection/internal/features/analysis/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUnprocessedVideoPlanGapsKeepClassificationAtAttention(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS analysis").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE analysis.video_plan_items (id TEXT, tenant_id TEXT, plan_id TEXT, requirement_key TEXT, outcome TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	tenant, plan := identity.NewID(), identity.NewID()
	for _, key := range []string{"video:with-job", "video:without-job"} {
		if err := db.Exec("INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id,requirement_key,outcome) VALUES (?,?,?,?,?)", identity.NewID(), tenant, plan, key, "INCONCLUSIVE").Error; err != nil {
			t.Fatal(err)
		}
	}
	jobs := []database.ComparisonJob{{RequirementKey: "video:with-job"}}
	gaps, err := unprocessedVideoPlanFacts(context.Background(), db, tenant, plan, jobs)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 1 || !gaps[0].Inconclusive || analysis.Classify(append([]analysis.ComparisonFacts{{Terminal: true}}, gaps...)).Classification != analysis.ClassificationAttention {
		t.Fatalf("unprocessed video gap did not require attention: %+v", gaps)
	}
}

func TestVideoPlanWithOnlyGapsIsClassifiedAsAttention(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"inspections", "analysis", "recapture", "messaging"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`CREATE TABLE inspections.inspections (id text PRIMARY KEY, tenant_id text NOT NULL, media_kind text NOT NULL, status text NOT NULL, active_video_plan_id text, analysis_prompt_snapshot_id text, updated_at datetime, version integer NOT NULL DEFAULT 1)`,
		`CREATE TABLE analysis.video_plans (id text PRIMARY KEY, tenant_id text NOT NULL, inspection_id text NOT NULL, parent_plan_id text, status text NOT NULL, generation integer NOT NULL)`,
		`CREATE TABLE analysis.video_plan_items (id text PRIMARY KEY, tenant_id text NOT NULL, plan_id text NOT NULL, carried_result_job_id text, requirement_key text, outcome text)`,
		`CREATE TABLE analysis.comparison_jobs (id text PRIMARY KEY, tenant_id text NOT NULL, inspection_id text NOT NULL, video_plan_id text, requirement_key text, video_core_group integer, status text NOT NULL, updated_at datetime)`,
		`CREATE TABLE analysis.classification_runs (id text PRIMARY KEY, tenant_id text NOT NULL, inspection_id text NOT NULL, prompt_snapshot_id text, video_plan_id text, classification text, reason_codes text, created_at datetime)`,
		`CREATE TABLE recapture.requests (id text PRIMARY KEY, tenant_id text NOT NULL, inspection_id text NOT NULL, status text NOT NULL)`,
		`CREATE TABLE messaging.outbox (id text PRIMARY KEY, tenant_id text, type text, schema_version integer, payload blob, correlation_id text, causation_id text, status text, attempts integer, next_attempt_at datetime, claimed_at datetime, last_error text, published_at datetime, created_at datetime)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, inspectionID, planID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO inspections.inspections (id,tenant_id,media_kind,status,active_video_plan_id,analysis_prompt_snapshot_id) VALUES (?,?,?,?,?,?)`, inspectionID, tenantID, "VIDEO", "ANALYZING", planID, identity.NewID()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO analysis.video_plans (id,tenant_id,inspection_id,status,generation) VALUES (?,?,?,?,?)`, planID, tenantID, inspectionID, "SEALED", 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id,requirement_key,outcome) VALUES (?,?,?,?,?)`, identity.NewID(), tenantID, planID, "video:kitchen", "INCONCLUSIVE").Error; err != nil {
		t.Fatal(err)
	}
	handler, err := Setup(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]identity.ID{"inspectionId": inspectionID})
	envelope := events.RawEnvelope{ID: identity.NewID(), TenantID: tenantID, CorrelationID: identity.NewID().String(), Payload: payload}
	if err := db.Transaction(func(tx *gorm.DB) error { return handler(context.Background(), tx, envelope) }); err != nil {
		t.Fatalf("gap-only video plan was not classified: %v", err)
	}
	var classification string
	if err := db.Table("analysis.classification_runs").Select("classification").Where("tenant_id=? AND inspection_id=? AND video_plan_id=?", tenantID, inspectionID, planID).Scan(&classification).Error; err != nil {
		t.Fatal(err)
	}
	if classification != analysis.ClassificationAttention {
		t.Fatalf("gap-only video plan classification = %q, want %q", classification, analysis.ClassificationAttention)
	}
}

func TestLateG1CompletionCannotClassifyActiveG2UT049(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"inspections", "analysis"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`CREATE TABLE inspections.inspections (id text PRIMARY KEY, tenant_id text NOT NULL, media_kind text NOT NULL, status text NOT NULL, active_video_plan_id text)`,
		`CREATE TABLE analysis.video_plans (id text PRIMARY KEY, tenant_id text NOT NULL, inspection_id text NOT NULL, parent_plan_id text, status text NOT NULL, generation integer NOT NULL)`,
		`CREATE TABLE analysis.video_plan_items (id text PRIMARY KEY, tenant_id text NOT NULL, plan_id text NOT NULL, component_id text, carried_result_job_id text, requirement_key text, outcome text, reason text, input_digest text, origin_frame_ids blob, current_frame_ids blob, core_groups blob, current_groups blob, created_at datetime)`,
		`CREATE TABLE analysis.comparison_jobs (id text PRIMARY KEY, tenant_id text NOT NULL, inspection_id text NOT NULL, video_plan_id text, requirement_key text, video_core_group integer, status text NOT NULL, updated_at datetime)`,
		`CREATE TABLE analysis.classification_runs (id text PRIMARY KEY, tenant_id text NOT NULL, inspection_id text NOT NULL, video_plan_id text, created_at datetime)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, inspectionID, g1ID, g2ID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO inspections.inspections (id,tenant_id,media_kind,status,active_video_plan_id) VALUES (?,?,?,?,?)`, inspectionID, tenantID, "VIDEO", "ANALYZING", g2ID).Error; err != nil {
		t.Fatal(err)
	}
	for _, plan := range []struct {
		id         identity.ID
		generation int
	}{{g1ID, 1}, {g2ID, 2}} {
		if err := db.Exec(`INSERT INTO analysis.video_plans (id,tenant_id,inspection_id,status,generation) VALUES (?,?,?,?,?)`, plan.id, tenantID, inspectionID, "SEALED", plan.generation).Error; err != nil {
			t.Fatal(err)
		}
	}
	late := time.Now().UTC().Add(time.Minute)
	if err := db.Exec(`INSERT INTO analysis.comparison_jobs (id,tenant_id,inspection_id,video_plan_id,requirement_key,video_core_group,status,updated_at) VALUES (?,?,?,?,?,?,?,?)`, identity.NewID(), tenantID, inspectionID, g1ID, "video:kitchen", 0, "COMPLETED", late).Error; err != nil {
		t.Fatal(err)
	}
	handler, err := Setup(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]identity.ID{"inspectionId": inspectionID})
	envelope := events.RawEnvelope{ID: identity.NewID(), TenantID: tenantID, Payload: payload}
	err = db.Transaction(func(tx *gorm.DB) error { return handler(context.Background(), tx, envelope) })
	if !errors.Is(err, messaging.ErrPermanent) {
		t.Fatalf("late G1 event with no G2 jobs = %v, want no current-generation classification", err)
	}
	var status string
	if err := db.Table("inspections.inspections").Select("status").Where("tenant_id=? AND id=?", tenantID, inspectionID).Scan(&status).Error; err != nil {
		t.Fatal(err)
	}
	if status != "ANALYZING" {
		t.Fatalf("late G1 job changed inspection status to %q", status)
	}
	var runs int64
	if err := db.Table("analysis.classification_runs").Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Count(&runs).Error; err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Fatalf("late G1 job created %d classification runs", runs)
	}
}

func TestLoadVideoPlanJobsResolvesCarriedJobAcrossPlanChain(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"analysis"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
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
	tenantID, inspectionID, g1ID, g2ID, g3ID, itemG2ID, itemG3ID, jobID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO analysis.video_plans (id,tenant_id,inspection_id,parent_plan_id,status,generation) VALUES (?,?,?,?,?,?)`, g1ID, tenantID, inspectionID, nil, "SEALED", 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO analysis.video_plans (id,tenant_id,inspection_id,parent_plan_id,status,generation) VALUES (?,?,?,?,?,?)`, g2ID, tenantID, inspectionID, g1ID, "SEALED", 2).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO analysis.video_plans (id,tenant_id,inspection_id,parent_plan_id,status,generation) VALUES (?,?,?,?,?,?)`, g3ID, tenantID, inspectionID, g2ID, "SEALED", 3).Error; err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id, plan identity.ID
	}{{itemG2ID, g2ID}, {itemG3ID, g3ID}} {
		if err := db.Exec(`INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id,component_id,carried_result_job_id,requirement_key,outcome,reason,input_digest,origin_frame_ids,current_frame_ids,core_groups,current_groups,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.id, tenantID, item.plan, identity.NewID(), jobID, "video:kitchen", "MATCHED", "", "digest", []byte("[]"), []byte("[]"), []byte("[]"), []byte("[]"), time.Now()).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`INSERT INTO analysis.comparison_jobs (id,tenant_id,inspection_id,video_plan_id,requirement_key,status,updated_at) VALUES (?,?,?,?,?,?,?)`, jobID, tenantID, inspectionID, g1ID, "video:kitchen", "COMPLETED", time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	jobs, itemCount, err := loadVideoPlanJobs(context.Background(), db, tenantID, inspectionID, g3ID)
	if err != nil {
		t.Fatal(err)
	}
	if itemCount != 1 || len(jobs) != 1 || jobs[0].ID != jobID || jobs[0].Status != "COMPLETED" {
		t.Fatalf("resolved items=%d jobs=%+v", itemCount, jobs)
	}
}

func TestPendingRecaptureGateIncludesOnlyActiveRequestStates(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS recapture").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE recapture.requests (id text PRIMARY KEY, tenant_id text NOT NULL, inspection_id text NOT NULL, responsibility_id text NOT NULL, status text NOT NULL, deadline_at datetime, idempotency_key text, payload_digest text, created_at datetime, updated_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	tenantID, inspectionID := identity.NewID(), identity.NewID()
	for _, status := range []string{"REQUESTED", "ACCESSED", "RECEIVED", "SUBMITTED"} {
		if err := db.Create(&database.RecaptureRequest{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, ResponsibilityID: identity.NewID(), Status: status}).Error; err != nil {
			t.Fatal(err)
		}
		pending, err := hasPendingVideoRecapture(context.Background(), db, tenantID, inspectionID)
		if err != nil {
			t.Fatal(err)
		}
		if pending != (status != "SUBMITTED") {
			t.Fatalf("status %s gate=%t", status, pending)
		}
		if err := db.Exec("DELETE FROM recapture.requests WHERE tenant_id=? AND inspection_id=?", tenantID, inspectionID).Error; err != nil {
			t.Fatal(err)
		}
	}
}
