package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSubmitVideoWithoutConfirmedReceiptReturnsInvalidStateUT006(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, schema := range []string{"capture", "invitations", "media", "messaging"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.ProcessingAcceptance{}, &database.MediaObject{}, &database.SubmissionVersion{}, &database.VideoPreparationJob{}, &database.OutboxIntent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenant, responsibility := identity.NewID(), identity.NewID()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, DisclosureVersion: "v1", VideoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	service := Service{DB: db, Now: func() time.Time { return now }, Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) }}
	_, err = service.Submit(context.Background(), tenant, responsibility, false)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidState {
		t.Fatalf("submission without received source was accepted: %v", err)
	}
}

type submissionFinalizerFunc func(context.Context, *gorm.DB, database.CaptureDraft, database.SubmissionVersion) error

func (f submissionFinalizerFunc) Finalize(ctx context.Context, tx *gorm.DB, draft database.CaptureDraft, submission database.SubmissionVersion) error {
	return f(ctx, tx, draft, submission)
}

func TestSubmitVideoRecapturePersistsReceiptThroughFinalizer(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, schema := range []string{"capture", "invitations", "media", "messaging"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.ProcessingAcceptance{}, &database.MediaObject{}, &database.SubmissionVersion{}, &database.VideoAttempt{}, &database.VideoPreparationJob{}, &database.ExternalSession{}, &database.Invitation{}, &database.OutboxIntent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenant, responsibility := identity.NewID(), identity.NewID()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	draft := database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "RECAPTURE", MediaKind: "VIDEO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, DisclosureVersion: "v1", VideoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	attemptID, mediaID := identity.NewID(), identity.NewID()
	if err := db.Create(&database.VideoAttempt{ID: attemptID, TenantID: tenant, ResponsibilityID: responsibility, Kind: "SUPPLEMENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1024, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "attempt", PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenant, ResponsibilityID: responsibility, VideoAttemptID: &attemptID, MediaKind: "VIDEO", RequirementKey: "video:supplement", Status: "RECEIVED", ContentType: "video/webm", SHA256: strings.Repeat("b", 64), SizeBytes: 12, Flags: []byte(`[]`), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	called := false
	finalizer := submissionFinalizerFunc(func(_ context.Context, _ *gorm.DB, submitted database.CaptureDraft, version database.SubmissionVersion) error {
		called = submitted.ID == draft.ID && version.DraftID == draft.ID
		return nil
	})
	service := Service{DB: db, Finalizer: finalizer, Now: func() time.Time { return now }, Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) }}
	if _, err := service.Submit(context.Background(), tenant, responsibility, false); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("video recapture submission bypassed its finalizer")
	}
}

func videoInspectionSubmitFixture(t *testing.T, finalizer SubmissionFinalizer) (*gorm.DB, Service, identity.ID, identity.ID) {
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
	for _, schema := range []string{"capture", "invitations", "media", "messaging"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.ProcessingAcceptance{}, &database.MediaObject{}, &database.SubmissionVersion{}, &database.VideoAttempt{}, &database.VideoPreparationJob{}, &database.ExternalSession{}, &database.Invitation{}, &database.OutboxIntent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenant, responsibility := identity.NewID(), identity.NewID()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, DisclosureVersion: "v1", VideoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	attemptID, mediaID := identity.NewID(), identity.NewID()
	if err := db.Create(&database.VideoAttempt{ID: attemptID, TenantID: tenant, ResponsibilityID: responsibility, Kind: "MAIN", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1024, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "attempt", PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenant, ResponsibilityID: responsibility, VideoAttemptID: &attemptID, MediaKind: "VIDEO", RequirementKey: "video:main", Status: "RECEIVED", ContentType: "video/webm", SHA256: strings.Repeat("b", 64), SizeBytes: 12, Flags: []byte(`[]`), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	service := Service{DB: db, Finalizer: finalizer, Now: func() time.Time { return now }, Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return db.Transaction(fn) }}
	return db, service, tenant, responsibility
}

func TestSubmitVideoInspectionFinalizesOnceAndReplayReturnsExistingVersion(t *testing.T) {
	finalizeCalls := 0
	finalizer := submissionFinalizerFunc(func(_ context.Context, _ *gorm.DB, draft database.CaptureDraft, submission database.SubmissionVersion) error {
		finalizeCalls++
		if draft.Kind != "INSPECTION" || submission.DraftID != draft.ID {
			t.Fatalf("unexpected finalized submission: draft=%+v submission=%+v", draft, submission)
		}
		return nil
	})
	db, service, tenant, responsibility := videoInspectionSubmitFixture(t, finalizer)
	first, err := service.Submit(context.Background(), tenant, responsibility, false)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.Submit(context.Background(), tenant, responsibility, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != replay.ID || finalizeCalls != 1 {
		t.Fatalf("submission replay = %s and finalizer calls %d; want same version and one call", replay.ID, finalizeCalls)
	}
	var count int64
	if err := db.Model(&database.SubmissionVersion{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("submission versions = %d; want 1", count)
	}
	var outboxEvents int64
	if err := db.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type=?", tenant, "capture.video_submitted.v1").Count(&outboxEvents).Error; err != nil {
		t.Fatal(err)
	}
	if outboxEvents != 1 {
		t.Fatalf("video submission outbox events = %d; want 1", outboxEvents)
	}
}

func TestSubmitVideoInspectionFinalizerFailureRollsBackSubmission(t *testing.T) {
	wantErr := apperror.New(apperror.InvalidState, "inspection", "inspection no longer accepts capture")
	db, service, tenant, responsibility := videoInspectionSubmitFixture(t, submissionFinalizerFunc(func(context.Context, *gorm.DB, database.CaptureDraft, database.SubmissionVersion) error {
		return wantErr
	}))
	if _, err := service.Submit(context.Background(), tenant, responsibility, false); !errors.Is(err, wantErr) {
		t.Fatalf("submit error = %v; want %v", err, wantErr)
	}
	var count int64
	if err := db.Model(&database.SubmissionVersion{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("submission versions after finalizer failure = %d; want 0", count)
	}
}

func TestSaveVideoMetadataAfterUploadReservedRequirement(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, schema := range []string{"capture", "media"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.MediaObject{}, &database.RequirementAnswer{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenant, responsibility, draftID, mediaID, attemptID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&database.CaptureDraft{ID: draftID, TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{"gpsRequired":false,"allowGallery":false}`), Requirements: []byte(`[{"key":"video:main","maximumMedia":1}]`), Status: "OPEN", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenant, ResponsibilityID: responsibility, VideoAttemptID: &attemptID, MediaKind: "VIDEO", RequirementKey: "video:main", Status: "RECEIVED", ContentType: "video/webm", SHA256: strings.Repeat("a", 64), SizeBytes: 10, Flags: []byte(`[]`), AttentionItems: []byte(`[]`)}).Error; err != nil {
		t.Fatal(err)
	}
	service := Service{DB: db, Now: func() time.Time { return now }, Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) }}
	input := MetadataInput{TenantID: tenant, ResponsibilityID: responsibility, MediaID: mediaID, RequirementKey: "video:main", Description: "Vídeo contínuo", CaptureSource: "CAMERA"}
	first, err := service.SaveMetadata(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.CapturedAt == nil || first.Description != input.Description {
		t.Fatalf("metadata was not saved: %+v", first)
	}
	if _, err := service.SaveMetadata(context.Background(), input); err != nil {
		t.Fatalf("idempotent metadata replay failed: %v", err)
	}
}
