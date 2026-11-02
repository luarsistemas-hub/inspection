package handle_state_changed

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	handleready "inspection/services/inspection/internal/features/reports/handle_report_ready"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestStateChangedInvalidatesAndIsIdempotent(t *testing.T) {
	db := stateChangedDB(t)
	tenantID, inspectionID, assetID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO inspections.inspections (id,tenant_id,status) VALUES (?,?,?)", inspectionID, tenantID, "CANCELED").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO dashboard.inspections (id,tenant_id,inspection_id,asset_id,classification,status,invalidated,sequence,updated_at) VALUES (?,?,?,?,?,?,?,?,?)", identity.NewID(), tenantID, inspectionID, assetID, "CRITICAL", "COMPLETED", false, 1, time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	caseRow := database.TriageCase{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, Status: "IN_REVIEW", Classification: "CRITICAL", ReasonCodes: []byte("[]"), ReportVersion: 1, Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := db.Create(&caseRow).Error; err != nil {
		t.Fatal(err)
	}
	handler, err := Setup(Dependencies{Now: func() time.Time { return time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	responsibilityID := identity.NewID()
	if err := db.Exec("INSERT INTO schedules.reminder_plans (id,tenant_id,inspection_id,remind_at,status) VALUES (?,?,?,?,?)", identity.NewID(), tenantID, inspectionID, time.Now().UTC(), "PLANNED").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO invitations.external_sessions (id,tenant_id,responsibility_id,revoked_at) VALUES (?,?,?,NULL)", identity.NewID(), tenantID, responsibilityID).Error; err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"inspectionId": inspectionID, "responsibilityId": responsibilityID, "to": "CANCELED", "revokeSessions": true})
	envelope := events.RawEnvelope{TenantID: tenantID, Type: "inspection.state_changed.v1", Payload: payload}
	for range 2 {
		if err := db.Transaction(func(tx *gorm.DB) error { return handler(context.Background(), tx, envelope) }); err != nil {
			t.Fatal(err)
		}
	}
	var projection database.DashboardInspection
	if err := db.Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Take(&projection).Error; err != nil {
		t.Fatal(err)
	}
	if !projection.Invalidated || projection.Status != "CANCELED" {
		t.Fatalf("projection not invalidated: %+v", projection)
	}
	var got database.TriageCase
	if err := db.Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Take(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != "ARCHIVED" || got.Version != 2 {
		t.Fatalf("triage did not archive exactly once: %+v", got)
	}
	var reminderStatus string
	if err := db.Raw("SELECT status FROM schedules.reminder_plans WHERE tenant_id=? AND inspection_id=?", tenantID, inspectionID).Scan(&reminderStatus).Error; err != nil || reminderStatus != "CANCELED" {
		t.Fatalf("reminder status=%q err=%v, want CANCELED", reminderStatus, err)
	}
	var revokedAt *time.Time
	if err := db.Raw("SELECT revoked_at FROM invitations.external_sessions WHERE tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).Scan(&revokedAt).Error; err != nil || revokedAt == nil {
		t.Fatalf("session revoked_at=%v err=%v, want revoked", revokedAt, err)
	}
	var auditCount int64
	if err := db.Model(&database.TriageCaseEvent{}).Where("tenant_id=? AND case_id=? AND kind='ARCHIVED'", tenantID, got.ID).Count(&auditCount).Error; err != nil || auditCount != 1 {
		t.Fatalf("archive events=%d err=%v", auditCount, err)
	}
	if err := handleready.UpsertDashboardInspection(db, database.DashboardInspection{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, AssetID: assetID, Classification: "CRITICAL", Status: "COMPLETED", Sequence: 2, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Take(&projection).Error; err != nil {
		t.Fatal(err)
	}
	if !projection.Invalidated {
		t.Fatal("report.ready after state_changed cleared invalidation")
	}
}

func TestStateChangedUpdatesProjection(t *testing.T) {
	db := stateChangedDB(t)
	tenantID, inspectionID, assetID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO inspections.inspections (id,tenant_id,status) VALUES (?,?,?)", inspectionID, tenantID, "INVALIDATED").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO dashboard.inspections (id,tenant_id,inspection_id,asset_id,classification,status,invalidated,sequence,updated_at) VALUES (?,?,?,?,?,?,?,?,?)", identity.NewID(), tenantID, inspectionID, assetID, "ATTENTION", "COMPLETED", false, 1, time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	handler, err := Setup(Dependencies{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"inspectionId": inspectionID})
	if err := db.Transaction(func(tx *gorm.DB) error {
		return handler(context.Background(), tx, events.RawEnvelope{TenantID: tenantID, Payload: payload})
	}); err != nil {
		t.Fatal(err)
	}
	var projection database.DashboardInspection
	if err := db.Raw("SELECT id,tenant_id,inspection_id,asset_id,classification,status,invalidated,sequence,updated_at FROM dashboard.inspections WHERE tenant_id=? AND inspection_id=?", tenantID, inspectionID).Scan(&projection).Error; err != nil {
		t.Fatal(err)
	}
	if !projection.Invalidated || projection.Status != "INVALIDATED" {
		t.Fatalf("invalidated state not projected: %+v", projection)
	}
}

func TestStateChangedRevokesSessionsAndCancelsReminders(t *testing.T) {
	db := stateChangedDB(t)
	tenantID, inspectionID, responsibilityID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO inspections.inspections (id,tenant_id,status) VALUES (?,?,?)", inspectionID, tenantID, "INVALIDATED").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO schedules.reminder_plans (id,tenant_id,inspection_id,remind_at,status) VALUES (?,?,?,?,?)", identity.NewID(), tenantID, inspectionID, time.Now().UTC(), "PLANNED").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO invitations.external_sessions (id,tenant_id,responsibility_id,revoked_at) VALUES (?,?,?,NULL)", identity.NewID(), tenantID, responsibilityID).Error; err != nil {
		t.Fatal(err)
	}
	handler, err := Setup(Dependencies{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"inspectionId": inspectionID, "responsibilityId": responsibilityID, "to": "INVALIDATED", "revokeSessions": true})
	if err := db.Transaction(func(tx *gorm.DB) error {
		return handler(context.Background(), tx, events.RawEnvelope{TenantID: tenantID, Type: "inspection.state_changed.v1", Payload: payload})
	}); err != nil {
		t.Fatal(err)
	}
	var reminderStatus string
	if err := db.Raw("SELECT status FROM schedules.reminder_plans WHERE tenant_id=? AND inspection_id=?", tenantID, inspectionID).Scan(&reminderStatus).Error; err != nil || reminderStatus != "CANCELED" {
		t.Fatalf("reminder status=%q err=%v, want CANCELED", reminderStatus, err)
	}
	var revokedAt *time.Time
	if err := db.Raw("SELECT revoked_at FROM invitations.external_sessions WHERE tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).Scan(&revokedAt).Error; err != nil || revokedAt == nil {
		t.Fatalf("session revoked_at=%v err=%v, want revoked", revokedAt, err)
	}
}

func stateChangedDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"dashboard", "inspections", "schedules", "invitations"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`CREATE TABLE inspections.inspections (id blob primary key,tenant_id blob not null,status text not null)`,
		`CREATE TABLE dashboard.inspections (id blob primary key,tenant_id blob not null,inspection_id blob not null,project_id blob,asset_id blob not null,classification text not null,status text not null,invalidated numeric not null default 0,sequence integer not null,updated_at datetime,unique(tenant_id,inspection_id))`,
		`CREATE TABLE dashboard.triage_cases (id blob primary key,tenant_id blob not null,inspection_id blob not null,status text not null,assignee_id blob,classification text not null,reason_codes blob,report_version integer not null,version integer not null,updated_at datetime,created_at datetime,unique(tenant_id,inspection_id))`,
		`CREATE TABLE dashboard.triage_case_events (id blob primary key,tenant_id blob not null,case_id blob not null,actor_id blob not null,kind text not null,client_mutation_id text not null default '',body text not null,created_at datetime)`,
		`CREATE TABLE schedules.reminder_plans (id blob primary key,tenant_id blob not null,inspection_id blob not null,remind_at datetime,status text not null)`,
		`CREATE TABLE invitations.external_sessions (id blob primary key,tenant_id blob not null,responsibility_id blob,revoked_at datetime)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}
