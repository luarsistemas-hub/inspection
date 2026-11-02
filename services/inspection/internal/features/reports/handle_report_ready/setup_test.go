package handle_report_ready

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupRequiresNotificationAndTriageAdapters(t *testing.T) {
	if handler, err := Setup(Dependencies{}); err == nil || handler != nil {
		t.Fatalf("Setup() returned handler=%t err=%v, want missing dependency error", handler != nil, err)
	}
}

func TestDashboardInvalidationIsMonotonic(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS dashboard").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE dashboard.inspections (id text primary key, tenant_id text not null, inspection_id text not null, project_id text, asset_id text not null, classification text not null, status text not null, invalidated boolean not null, sequence integer not null, updated_at datetime, UNIQUE(tenant_id, inspection_id))").Error; err != nil {
		t.Fatal(err)
	}
	tenantID, inspectionID, assetID := identity.NewID(), identity.NewID(), identity.NewID()
	row := database.DashboardInspection{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, AssetID: assetID, Classification: "CRITICAL", Status: "CANCELED", Invalidated: true, Sequence: 2, UpdatedAt: time.Now().UTC()}
	if err := UpsertDashboardInspection(db, row); err != nil {
		t.Fatal(err)
	}
	row.ID = identity.NewID()
	row.Status = "COMPLETED"
	row.Invalidated = false
	row.Sequence = 3
	if err := UpsertDashboardInspection(db, row); err != nil {
		t.Fatal(err)
	}
	var got bool
	if err := db.Raw("SELECT invalidated FROM dashboard.inspections WHERE tenant_id=? AND inspection_id=?", tenantID, inspectionID).Scan(&got).Error; err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("a later report.ready event cleared an earlier invalidation")
	}
}

func TestReportReadyKeepsCanceledInspectionInvalidated(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"dashboard", "inspections", "analysis", "reports"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, ddl := range []string{
		`CREATE TABLE inspections.inspections (id text primary key,tenant_id text not null,status text not null,project_id text,asset_id text)`,
		`CREATE TABLE analysis.classification_runs (id text primary key,tenant_id text not null,inspection_id text not null,classification text not null,reason_codes blob,created_at datetime)`,
		`CREATE TABLE reports.report_snapshots (id text primary key,tenant_id text not null,inspection_id text not null,version_number integer not null,created_at datetime)`,
		`CREATE TABLE dashboard.inspections (id text primary key,tenant_id text not null,inspection_id text not null,project_id text,asset_id text not null,classification text not null,status text not null,invalidated boolean not null,sequence integer not null,updated_at datetime,UNIQUE(tenant_id,inspection_id))`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, inspectionID, assetID, snapshotID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO inspections.inspections (id,tenant_id,status,asset_id) VALUES (?,?,?,?)", inspectionID, tenantID, "CANCELED", assetID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO analysis.classification_runs (id,tenant_id,inspection_id,classification,reason_codes,created_at) VALUES (?,?,?,?,?,?)", identity.NewID(), tenantID, inspectionID, "CRITICAL", []byte("[]"), time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO reports.report_snapshots (id,tenant_id,inspection_id,version_number,created_at) VALUES (?,?,?,?,?)", snapshotID, tenantID, inspectionID, 1, time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	triageCalled := false
	notificationCalled := false
	handler, err := Setup(Dependencies{
		InAppNotification: func(context.Context, *gorm.DB, events.RawEnvelope) error { notificationCalled = true; return nil },
		UpsertTriage: func(context.Context, *gorm.DB, identity.ID, identity.ID, database.ClassificationRun, int, time.Time) error {
			triageCalled = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"inspectionId": inspectionID, "snapshotId": snapshotID})
	if err := handler(context.Background(), db, events.RawEnvelope{TenantID: tenantID, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	var projection database.DashboardInspection
	if err := db.Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Take(&projection).Error; err != nil {
		t.Fatal(err)
	}
	if projection.Status != "CANCELED" || !projection.Invalidated || !notificationCalled || triageCalled {
		t.Fatalf("projection=%+v notification=%t triage=%t", projection, notificationCalled, triageCalled)
	}
}
