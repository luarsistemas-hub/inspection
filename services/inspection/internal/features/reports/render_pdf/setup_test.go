package render_pdf

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSnapshotEventSchedulesBothAudiencesIdempotentlyAndSkipsLegacy(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS reports").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.ReportSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE report_pdf_jobs (
		id blob PRIMARY KEY, tenant_id blob NOT NULL, snapshot_id blob NOT NULL, audience text NOT NULL, renderer_version integer NOT NULL,
		status text NOT NULL, attempts integer NOT NULL DEFAULT 0, next_attempt_at datetime NOT NULL,
		lease_token blob, lease_expires_at datetime, artifact_id blob, pending_object_key text NOT NULL DEFAULT '', last_error text NOT NULL DEFAULT '',
		created_at datetime, updated_at datetime, UNIQUE(tenant_id, snapshot_id, audience)
	)`).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	tenantID, inspectionID := identity.NewID(), identity.NewID()
	snapshot := database.ReportSnapshot{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, VersionNumber: 1, PDFRenderVersion: 1, CanonicalJSON: json.RawMessage(`{}`), HTML: json.RawMessage(`""`), CreatedAt: now}
	if err := db.Create(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	handler, err := Setup(Dependencies{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"snapshotId": snapshot.ID.String()})
	envelope := events.RawEnvelope{ID: identity.NewID(), TenantID: tenantID, Payload: payload}
	for range 2 {
		if err := handler(context.Background(), db, envelope); err != nil {
			t.Fatal(err)
		}
	}
	var jobs []database.ReportPDFJob
	if err := db.Raw("SELECT * FROM report_pdf_jobs").Scan(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].NextAttemptAt != now || jobs[1].NextAttemptAt != now {
		t.Fatalf("scheduled jobs = %#v", jobs)
	}

	snapshot.ID, snapshot.PDFRenderVersion = identity.NewID(), 0
	if err := db.Create(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	payload, _ = json.Marshal(map[string]string{"snapshotId": snapshot.ID.String()})
	envelope.Payload = payload
	if err := handler(context.Background(), db, envelope); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Raw("SELECT count(*) FROM report_pdf_jobs").Scan(&count).Error; err != nil || count != 2 {
		t.Fatalf("legacy snapshot scheduled jobs count=%d err=%v", count, err)
	}
}

func TestSetupExecutorRequiresDatabaseAndPrivateStore(t *testing.T) {
	if _, err := SetupExecutor(nil, objectstore.Store{}, DefaultLimits(), time.Now); err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := SetupExecutor(&gorm.DB{}, objectstore.Store{}, DefaultLimits(), time.Now); err == nil {
		t.Fatal("missing private store accepted")
	}
}

func rawEnvelope(tenantID identity.ID, payload []byte) events.RawEnvelope {
	return events.RawEnvelope{ID: identity.NewID(), TenantID: tenantID, Payload: payload}
}
