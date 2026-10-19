package submit

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type unusedFinalizer struct{}

func (unusedFinalizer) Finalize(context.Context, *gorm.DB, database.CaptureDraft, database.SubmissionVersion) error {
	return nil
}

func TestSubmitRejectsExpiredDeadlineBeforeCaptureSubmission(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:recapture-submit-deadline?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS recapture").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE recapture.requests (id text PRIMARY KEY, tenant_id text, inspection_id text, responsibility_id text, status text, deadline_at datetime, idempotency_key text, payload_digest text, created_at datetime, updated_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	tenantID, requestID, responsibilityID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	request := database.RecaptureRequest{ID: requestID, TenantID: tenantID, InspectionID: identity.NewID(), ResponsibilityID: responsibilityID, Status: "REQUESTED", DeadlineAt: now.Add(-time.Second), IdempotencyKey: "expired"}
	if err := db.Create(&request).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := Setup(Dependencies{
		DB: db, Bus: bus, Capture: capturecore.Service{DB: db, Finalizer: unusedFinalizer{}},
		Now:    func() time.Time { return now },
		Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) },
	}); err != nil {
		t.Fatal(err)
	}
	_, err = bus.Send(context.Background(), Command{TenantID: tenantID, ResponsibilityID: responsibilityID, RequestID: requestID})
	if !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("expired submit error = %v, want INVALID_STATE", err)
	}
	var stored database.RecaptureRequest
	if err := db.Where("tenant_id=? AND id=?", tenantID, requestID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "REQUESTED" {
		t.Fatalf("expired request advanced to %q", stored.Status)
	}
}
