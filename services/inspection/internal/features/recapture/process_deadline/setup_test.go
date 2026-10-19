package process_deadline

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestVideoReceiptStateDefersUntilPreparationAndCoverageTerminate(t *testing.T) {
	db := deadlineDB(t)
	tenantID, responsibilityID, draftID := identity.NewID(), identity.NewID(), identity.NewID()
	request := database.RecaptureRequest{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, DeadlineAt: time.Now().UTC()}
	mediaID, preparationID := identity.NewID(), identity.NewID()
	seedVideoReceipt(t, db, tenantID, responsibilityID, draftID, mediaID, request.DeadlineAt.Add(-time.Minute))
	if err := db.Exec("INSERT INTO media.video_preparation_jobs (id,tenant_id,media_id,status,generation) VALUES (?,?,?,?,?)", preparationID, tenantID, mediaID, "EXTRACTING", 1).Error; err != nil {
		t.Fatal(err)
	}
	deferExpiry, terminal, err := videoReceiptState(context.Background(), db, tenantID, request)
	if err != nil || !deferExpiry || terminal {
		t.Fatalf("in-progress preparation should defer expiry: defer=%t terminal=%t err=%v", deferExpiry, terminal, err)
	}
	if err := db.Exec("UPDATE media.video_preparation_jobs SET status='FAILED' WHERE id=?", preparationID).Error; err != nil {
		t.Fatal(err)
	}
	deferExpiry, terminal, err = videoReceiptState(context.Background(), db, tenantID, request)
	if err != nil || deferExpiry || !terminal {
		t.Fatalf("terminal preparation failure should close the request: defer=%t terminal=%t err=%v", deferExpiry, terminal, err)
	}
}

func TestVideoReceiptStateDefersWhileCoverageRunsThenClosesWithoutCoverage(t *testing.T) {
	db := deadlineDB(t)
	tenantID, responsibilityID, draftID := identity.NewID(), identity.NewID(), identity.NewID()
	request := database.RecaptureRequest{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, DeadlineAt: time.Now().UTC()}
	mediaID, preparationID, coverageID := identity.NewID(), identity.NewID(), identity.NewID()
	seedVideoReceipt(t, db, tenantID, responsibilityID, draftID, mediaID, request.DeadlineAt.Add(-time.Minute))
	if err := db.Exec("INSERT INTO media.video_preparation_jobs (id,tenant_id,media_id,status,generation) VALUES (?,?,?,?,?)", preparationID, tenantID, mediaID, "READY", 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO recapture.video_coverage_jobs (id,tenant_id,preparation_id,status) VALUES (?,?,?,?)", coverageID, tenantID, preparationID, "PROCESSING").Error; err != nil {
		t.Fatal(err)
	}
	deferExpiry, terminal, err := videoReceiptState(context.Background(), db, tenantID, request)
	if err != nil || !deferExpiry || terminal {
		t.Fatalf("active visual evaluation should defer expiry: defer=%t terminal=%t err=%v", deferExpiry, terminal, err)
	}
	if err := db.Exec("UPDATE recapture.video_coverage_jobs SET status='COMPLETED' WHERE id=?", coverageID).Error; err != nil {
		t.Fatal(err)
	}
	deferExpiry, terminal, err = videoReceiptState(context.Background(), db, tenantID, request)
	if err != nil || deferExpiry || !terminal {
		t.Fatalf("completed evaluation without request finalization should close as unresolved: defer=%t terminal=%t err=%v", deferExpiry, terminal, err)
	}
}

func TestPhotoReceiptStateKeepsExistingDeadlineBehavior(t *testing.T) {
	db := deadlineDB(t)
	tenantID, responsibilityID, draftID := identity.NewID(), identity.NewID(), identity.NewID()
	request := database.RecaptureRequest{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, DeadlineAt: time.Now().UTC()}
	if err := db.Exec("INSERT INTO capture.capture_drafts (id,tenant_id,responsibility_id,kind,media_kind) VALUES (?,?,?,?,?)", draftID, tenantID, responsibilityID, "RECAPTURE", "PHOTO").Error; err != nil {
		t.Fatal(err)
	}
	deferExpiry, terminal, err := videoReceiptState(context.Background(), db, tenantID, request)
	if err != nil || deferExpiry || terminal {
		t.Fatalf("photo request should retain normal expiry: defer=%t terminal=%t err=%v", deferExpiry, terminal, err)
	}
}

func deadlineDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"capture", "media", "recapture"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`CREATE TABLE capture.capture_drafts (id text PRIMARY KEY, tenant_id text, responsibility_id text, kind text, media_kind text)`,
		`CREATE TABLE capture.submission_versions (id text PRIMARY KEY, tenant_id text, draft_id text, responsibility_id text, version_number integer, payload blob, submitted_at datetime)`,
		`CREATE TABLE media.video_preparation_jobs (id text PRIMARY KEY, tenant_id text, media_id text, status text, generation integer)`,
		`CREATE TABLE recapture.video_coverage_jobs (id text PRIMARY KEY, tenant_id text, preparation_id text, status text)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func seedVideoReceipt(t *testing.T, db *gorm.DB, tenantID, responsibilityID, draftID, mediaID identity.ID, submittedAt time.Time) {
	t.Helper()
	if err := db.Exec("INSERT INTO capture.capture_drafts (id,tenant_id,responsibility_id,kind,media_kind) VALUES (?,?,?,?,?)", draftID, tenantID, responsibilityID, "RECAPTURE", "VIDEO").Error; err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"mediaKind": "VIDEO", "mediaId": mediaID})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO capture.submission_versions (id,tenant_id,draft_id,responsibility_id,version_number,payload,submitted_at) VALUES (?,?,?,?,?,?,?)", identity.NewID(), tenantID, draftID, responsibilityID, 1, payload, submittedAt).Error; err != nil {
		t.Fatal(err)
	}
}
