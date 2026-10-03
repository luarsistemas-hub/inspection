package main

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTriageReviewProjectionCreatesAndRefreshesCaseIdempotently(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS dashboard").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE dashboard.triage_cases (
		id text PRIMARY KEY, tenant_id text NOT NULL, inspection_id text NOT NULL, status text NOT NULL,
		assignee_id text, classification text NOT NULL, reason_codes blob NOT NULL,
		report_version integer NOT NULL, version integer NOT NULL, updated_at datetime NOT NULL, created_at datetime NOT NULL
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE UNIQUE INDEX dashboard.idx_triage_case_tenant_inspection ON triage_cases(tenant_id, inspection_id)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE dashboard.triage_case_events (
		id text PRIMARY KEY, tenant_id text NOT NULL, case_id text NOT NULL, actor_id text NOT NULL,
		kind text NOT NULL, client_mutation_id text NOT NULL DEFAULT '', body text NOT NULL, created_at datetime NOT NULL
	)`).Error; err != nil {
		t.Fatal(err)
	}

	tenantID, inspectionID := identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	classification := database.ClassificationRun{Classification: "ATTENTION", ReasonCodes: []byte(`["MISSING_EVIDENCE"]`)}
	if err := upsertTriageReviewCase(context.Background(), db, tenantID, inspectionID, classification, 1, now); err != nil {
		t.Fatal(err)
	}
	if err := upsertTriageReviewCase(context.Background(), db, tenantID, inspectionID, classification, 1, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	var triage database.TriageCase
	if err := db.Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).First(&triage).Error; err != nil {
		t.Fatal(err)
	}
	var events int64
	if err := db.Model(&database.TriageCaseEvent{}).Where("tenant_id=? AND case_id=?", tenantID, triage.ID).Count(&events).Error; err != nil {
		t.Fatal(err)
	}
	if triage.Status != "NEW" || triage.ReportVersion != 1 || triage.Version != 1 || events != 1 {
		t.Fatalf("replay changed initial review state: case=%+v events=%d", triage, events)
	}

	classification.Classification = "CRITICAL"
	if err := upsertTriageReviewCase(context.Background(), db, tenantID, inspectionID, classification, 2, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).First(&triage).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.TriageCaseEvent{}).Where("tenant_id=? AND case_id=?", tenantID, triage.ID).Count(&events).Error; err != nil {
		t.Fatal(err)
	}
	if triage.Status != "IN_REVIEW" || triage.Classification != "CRITICAL" || triage.ReportVersion != 2 || triage.Version != 2 || events != 2 {
		t.Fatalf("new report did not advance the review case: case=%+v events=%d", triage, events)
	}
}
