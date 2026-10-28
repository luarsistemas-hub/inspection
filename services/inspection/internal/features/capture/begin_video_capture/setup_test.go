package begin_video_capture

import (
	"context"
	"errors"
	"strings"
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

func videoCaptureDB(t *testing.T) (*gorm.DB, identity.ID, identity.ID, time.Time) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, schema := range []string{"capture", "media", "invitations", "inspections", "reports", "audit", "platform", "messaging", "templates"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.TemplateVersion{}, &database.VideoAttempt{}, &database.VideoPolicy{}, &database.VideoAdmission{}, &database.ProcessingAcceptance{}, &database.Invitation{}, &database.ExternalSession{}, &database.MediaObject{}, &database.Responsibility{}, &database.ReportSnapshot{}, &database.SubmissionVersion{}, &database.VideoPreparationJob{}, &database.OutboxIntent{}, &database.AuditEvent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenant, responsibility := identity.NewID(), identity.NewID()
	inspectionID := identity.NewID()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&database.Responsibility{ID: responsibility, TenantID: tenant, InspectionID: inspectionID, Status: "ACTIVE"}).Error; err != nil {
		t.Fatal(err)
	}
	templateVersionID := identity.NewID()
	if err := db.Create(&database.TemplateVersion{ID: templateVersionID, TenantID: tenant, TemplateID: identity.NewID(), VersionNumber: 1, SchemaVersion: 1, DefinitionJSON: []byte(`{}`), CanonicalDigest: "v1", Status: "ACTIVE", IdempotencyKey: "legacy-v1", PublishedAt: now, CreatedBy: identity.NewID()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: templateVersionID, ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.VideoAdmission{ID: 1, Enabled: false, Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, DisclosureVersion: "v1", VideoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.Invitation{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, TokenHash: []byte("hash"), DeliveryIntents: []byte(`[]`), Status: "ACTIVE", ExpiresAt: now.Add(48 * time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	return db, tenant, responsibility, now
}

func inlineWithin(db *gorm.DB) func(context.Context, identity.ID, func(*gorm.DB) error) error {
	return func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) }
}

func TestAdmitFreezesLimitsAndExpiresAtUT005(t *testing.T) {
	db, tenant, responsibility, now := videoCaptureDB(t)
	policy := database.VideoPolicy{TenantID: tenant, MaxDurationMinutes: 15, OriginRetentionDays: 30, CurrentRetentionDays: 5, Version: 4}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatal(err)
	}
	attempt, err := admit(context.Background(), db, inlineWithin(db), Command{TenantID: tenant, ResponsibilityID: responsibility, AcquisitionSource: "camera", MutationID: "a1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.MaxDurationSeconds != 900 || attempt.MaxBytes != MaxBytes || attempt.PolicyVersion != 4 || !attempt.ExpiresAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("limits were not frozen: %+v", attempt)
	}
}

// IT-129: photo-only acceptance cannot authorize a later video attempt.
func TestOldPhotoConsentDoesNotAuthorizeVideoCaptureIT129(t *testing.T) {
	db, tenant, responsibility, now := videoCaptureDB(t)
	if err := db.Model(&database.ProcessingAcceptance{}).Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Updates(map[string]any{"photo_processing": true, "video_processing": false}).Error; err != nil {
		t.Fatal(err)
	}
	command := Command{TenantID: tenant, ResponsibilityID: responsibility, AcquisitionSource: "CAMERA", MutationID: "video-after-photo-consent"}
	_, err := admit(context.Background(), db, inlineWithin(db), command, now)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidInput {
		t.Fatalf("old photo-only consent authorized video: %v", err)
	}
	if err := db.Model(&database.ProcessingAcceptance{}).Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Update("video_processing", true).Error; err != nil {
		t.Fatal(err)
	}
	if attempt, err := admit(context.Background(), db, inlineWithin(db), command, now); err != nil || attempt.ID == (identity.ID{}) {
		t.Fatalf("current video consent did not authorize capture: attempt=%+v err=%v", attempt, err)
	}
}

func TestCameraOnlyPolicyRejectsFileVideoCaptureIT132(t *testing.T) {
	db, tenant, responsibility, now := videoCaptureDB(t)
	if err := db.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Update("policy_payload", []byte(`{"allowGallery":false}`)).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := Setup(Dependencies{DB: db, Bus: bus, Within: inlineWithin(db), Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	_, err := bus.Send(context.Background(), Command{TenantID: tenant, ResponsibilityID: responsibility, AcquisitionSource: "FILE", MutationID: "camera-only-file"})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidInput {
		t.Fatalf("camera-only policy accepted FILE capture: %v", err)
	}
	var attempts int64
	if err := db.Model(&database.VideoAttempt{}).Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Count(&attempts).Error; err != nil || attempts != 0 {
		t.Fatalf("rejected FILE request created %d attempts: %v", attempts, err)
	}
}

func TestVideoAttemptReplayAndNewCaptureIgnoreHistoricalAdmissionStateUT078(t *testing.T) {
	db, tenant, responsibility, now := videoCaptureDB(t)
	command := Command{TenantID: tenant, ResponsibilityID: responsibility, AcquisitionSource: "CAMERA", MutationID: "accepted-video-replay"}
	accepted, err := admit(context.Background(), db, inlineWithin(db), command, now)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := admit(context.Background(), db, inlineWithin(db), command, now.Add(time.Minute))
	if err != nil || replayed.ID != accepted.ID || replayed.Status != "ACTIVE" {
		t.Fatalf("accepted attempt could not resume with historical admission disabled: accepted=%+v replayed=%+v err=%v", accepted, replayed, err)
	}
	if err := db.Model(&database.VideoAttempt{}).Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Update("status", "EXPIRED").Error; err != nil {
		t.Fatal(err)
	}
	newAttempt, err := admit(context.Background(), db, inlineWithin(db), Command{TenantID: tenant, ResponsibilityID: responsibility, AcquisitionSource: "CAMERA", MutationID: "accepted-video-after-disable"}, now.Add(time.Minute))
	if err != nil || newAttempt.ID == accepted.ID {
		t.Fatalf("new video attempt was rejected by historical admission state: attempt=%+v err=%v", newAttempt, err)
	}
}

func TestAdmitReplacesRejectedVideoAndPreservesHistoryUT007(t *testing.T) {
	db, tenant, responsibility, now := videoCaptureDB(t)
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).First(&draft).Error; err != nil {
		t.Fatal(err)
	}
	draft.Status = "SUBMITTED"
	if err := db.Save(&draft).Error; err != nil {
		t.Fatal(err)
	}
	priorSubmission := database.SubmissionVersion{ID: identity.NewID(), TenantID: tenant, DraftID: draft.ID, ResponsibilityID: responsibility, VersionNumber: 1, Complete: true, Payload: []byte(`{"generation":1}`), PayloadDigest: strings.Repeat("b", 64), SubmittedAt: now.Add(-time.Minute)}
	if err := db.Create(&priorSubmission).Error; err != nil {
		t.Fatal(err)
	}
	oldID := identity.NewID()
	if err := db.Create(&database.MediaObject{ID: oldID, TenantID: tenant, ResponsibilityID: responsibility, ObjectKey: "private/old", ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 1, Status: "REJECTED", MediaKind: "VIDEO", RequirementKey: "video:main"}).Error; err != nil {
		t.Fatal(err)
	}
	attempt, err := admit(context.Background(), db, inlineWithin(db), Command{TenantID: tenant, ResponsibilityID: responsibility, AcquisitionSource: "CAMERA", MutationID: "replacement", ReplacesMediaID: &oldID}, now)
	if err != nil || attempt.ID == (identity.ID{}) {
		t.Fatalf("replacement not admitted: %+v %v", attempt, err)
	}
	var count int64
	if err := db.Model(&database.MediaObject{}).Where("tenant_id=? AND id=? AND status='REJECTED'", tenant, oldID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("rejected history changed: count=%d err=%v", count, err)
	}
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).First(&draft).Error; err != nil || draft.Status != "OPEN" {
		t.Fatalf("replacement did not open a new capture generation: draft=%+v err=%v", draft, err)
	}
	mediaID := identity.NewID()
	attemptID := attempt.ID
	if err := db.Model(&database.VideoAttempt{}).Where("tenant_id=? AND id=?", tenant, attemptID).Update("status", "RECEIVED").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenant, ResponsibilityID: responsibility, ObjectKey: "private/replacement", ContentType: "video/mp4", SHA256: strings.Repeat("c", 64), SizeBytes: 100, Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main"}).Error; err != nil {
		t.Fatal(err)
	}
	submissionService := capturecore.Service{DB: db, Now: func() time.Time { return now }, Within: inlineWithin(db)}
	newSubmission, err := submissionService.Submit(context.Background(), tenant, responsibility, false)
	if err != nil || newSubmission.VersionNumber != 2 {
		t.Fatalf("replacement generation was not submitted: %+v %v", newSubmission, err)
	}
}

func TestAdmitRejectsReplacementAfterReportUT008(t *testing.T) {
	db, tenant, responsibility, now := videoCaptureDB(t)
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).First(&draft).Error; err != nil {
		t.Fatal(err)
	}
	var responsibilityRow database.Responsibility
	if err := db.Where("tenant_id=? AND id=?", tenant, responsibility).First(&responsibilityRow).Error; err != nil {
		t.Fatal(err)
	}
	inspectionID := responsibilityRow.InspectionID
	if err := db.Create(&database.ReportSnapshot{ID: identity.NewID(), TenantID: tenant, InspectionID: inspectionID, Mode: "AUTO", Classification: "OK", JSONDigest: "d", HTMLDigest: "d", VersionNumber: 1, CanonicalJSON: []byte(`{}`), HTML: []byte("<p>report</p>")}).Error; err != nil {
		t.Fatal(err)
	}
	oldID := identity.NewID()
	if err := db.Create(&database.MediaObject{ID: oldID, TenantID: tenant, ResponsibilityID: responsibility, ObjectKey: "private/old", ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 1, Status: "REJECTED", MediaKind: "VIDEO", RequirementKey: "video:main"}).Error; err != nil {
		t.Fatal(err)
	}
	_, err := admit(context.Background(), db, inlineWithin(db), Command{TenantID: tenant, ResponsibilityID: responsibility, AcquisitionSource: "CAMERA", MutationID: "replacement", ReplacesMediaID: &oldID}, now)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidState {
		t.Fatalf("replacement after completed report was accepted: %v", err)
	}
}

func TestSetupValidatesDependenciesUT009(t *testing.T) {
	if err := Setup(Dependencies{Bus: mediator.New()}); err == nil {
		t.Fatal("missing db accepted")
	}
	if err := Setup(Dependencies{DB: &gorm.DB{}}); err == nil {
		t.Fatal("missing bus accepted")
	}
}
