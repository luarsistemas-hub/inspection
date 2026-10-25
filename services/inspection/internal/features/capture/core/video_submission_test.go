package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
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
	for _, schema := range []string{"capture", "templates", "invitations", "media", "messaging", "inspections"} {
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
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "VIDEO", ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1}).Error; err != nil {
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
	draft := database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "RECAPTURE", MediaKind: "VIDEO", ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1}
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
	for _, schema := range []string{"capture", "templates", "invitations", "media", "messaging", "inspections"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.TemplateVersion{}, &database.RequirementAnswer{}, &database.ProcessingAcceptance{}, &database.MediaObject{}, &database.SubmissionVersion{}, &database.VideoAttempt{}, &database.VideoPreparationJob{}, &database.ExternalSession{}, &database.Invitation{}, &database.OutboxIntent{}, &database.Responsibility{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenant, responsibility := identity.NewID(), identity.NewID()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "VIDEO", ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1}).Error; err != nil {
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

func TestSubmitVideoInspectionFinalizesOnceAndReplayReturnsExistingVersionUT120(t *testing.T) {
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

func TestSubmitEnvironmentVideoReturnsAllExpectedPendingKeys(t *testing.T) {
	db, service, tenant, responsibility := videoInspectionSubmitFixture(t, nil)
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	templateVersionID := identity.NewID()
	if err := db.Create(&database.TemplateVersion{ID: templateVersionID, TenantID: tenant, TemplateID: identity.NewID(), VersionNumber: 1, SchemaVersion: 2, DefinitionJSON: json.RawMessage(`{"schemaVersion":2,"isVideo":true}`), CanonicalDigest: "task3-video-v2", Status: "PUBLISHED", IdempotencyKey: "task3-video-v2", PublishedAt: time.Now().UTC(), CreatedBy: identity.NewID()}).Error; err != nil {
		t.Fatal(err)
	}
	requirements := []Requirement{{Key: "env:kitchen", Required: true, Expected: true, ImpossibilityAllowed: true, MinimumMedia: 1, MaximumMedia: 3}, {Key: "env:bedroom", Required: true, Expected: true, ImpossibilityAllowed: true, MinimumMedia: 1, MaximumMedia: 3}}
	encoded, _ := json.Marshal(requirements)
	if err := db.Model(&draft).Updates(map[string]any{"template_version_id": templateVersionID, "requirements": encoded}).Error; err != nil {
		t.Fatal(err)
	}
	_, err := service.Submit(context.Background(), tenant, responsibility, false)
	var pending PendingRequirementError
	if !errors.As(err, &pending) || !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("video submit error=%v; want INVALID_STATE with PendingRequirementError", err)
	}
	if len(pending.Keys) != 2 || pending.Keys[0] != "env:kitchen" || pending.Keys[1] != "env:bedroom" {
		t.Fatalf("video pending keys=%v; want every expected environment", pending.Keys)
	}
}

func TestSubmitEnvironmentVideoAllowsMultipleClipsPerEnvironmentAndPreservesCompleteness(t *testing.T) {
	db, service, tenant, responsibility := videoInspectionSubmitFixture(t, nil)
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	templateVersionID := identity.NewID()
	now := time.Now().UTC()
	if err := db.Create(&database.TemplateVersion{ID: templateVersionID, TenantID: tenant, TemplateID: identity.NewID(), VersionNumber: 1, SchemaVersion: 2, DefinitionJSON: json.RawMessage(`{"schemaVersion":2,"isVideo":true}`), CanonicalDigest: "task3-video-v2-multi-clip", Status: "PUBLISHED", IdempotencyKey: "task3-video-v2-multi-clip", PublishedAt: now, CreatedBy: identity.NewID()}).Error; err != nil {
		t.Fatal(err)
	}
	requirements := []Requirement{
		{Key: "env:kitchen", Required: true, Expected: true, ImpossibilityAllowed: true, MinimumMedia: 1, MaximumMedia: 3},
		{Key: "env:new:bedroom", Required: true, AddedDuringCapture: true, ImpossibilityAllowed: true, MinimumMedia: 1, MaximumMedia: 3},
	}
	encoded, _ := json.Marshal(requirements)
	if err := db.Model(&draft).Updates(map[string]any{"template_version_id": templateVersionID, "requirements": encoded}).Error; err != nil {
		t.Fatal(err)
	}
	inspectionID := identity.NewID()
	if err := db.Create(&database.Responsibility{ID: responsibility, TenantID: tenant, InspectionID: inspectionID, ParticipantID: identity.NewID(), Status: "IN_PROGRESS", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		attemptID, mediaID := identity.NewID(), identity.NewID()
		if err := db.Create(&database.VideoAttempt{ID: attemptID, TenantID: tenant, ResponsibilityID: responsibility, RequirementKey: stringPtr("env:kitchen"), Kind: "MAIN", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1024, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: identity.NewID().String(), PayloadDigest: strings.Repeat("c", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenant, ResponsibilityID: responsibility, VideoAttemptID: &attemptID, MediaKind: "VIDEO", RequirementKey: "env:kitchen", Status: "RECEIVED", ContentType: "video/webm", SHA256: strings.Repeat("d", 64), SizeBytes: 12, Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	submission, err := service.Submit(context.Background(), tenant, responsibility, true)
	if err != nil {
		t.Fatalf("two clips for env:kitchen were rejected: %v", err)
	}
	if submission.Complete || !submission.RequiresAttention {
		t.Fatalf("incomplete environment video submission = complete:%v attention:%v; want false/true", submission.Complete, submission.RequiresAttention)
	}
	var payload struct {
		Clips             []events.CaptureVideoClip `json:"clips"`
		Complete          bool                      `json:"complete"`
		RequiresAttention bool                      `json:"requiresAttention"`
	}
	if err := json.Unmarshal(submission.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Clips) != 2 || payload.Clips[0].RequirementKey != "env:kitchen" || payload.Clips[1].RequirementKey != "env:kitchen" || payload.Complete || !payload.RequiresAttention {
		t.Fatalf("immutable submission payload = %+v", payload)
	}
	var outbox database.OutboxIntent
	if err := db.Where("tenant_id=? AND type=?", tenant, "capture.video_submitted.v2").Take(&outbox).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := events.DefaultRegistry().Validate(outbox.Payload); err != nil {
		t.Fatalf("two clips with one requirement key failed event validation: %v", err)
	}
}

func TestSubmitV2WithoutEnvironmentsReturnsNoEnvironmentsUT113(t *testing.T) {
	db, service, tenant, responsibility := videoInspectionSubmitFixture(t, nil)
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	templateVersionID := identity.NewID()
	if err := db.Create(&database.TemplateVersion{ID: templateVersionID, TenantID: tenant, TemplateID: identity.NewID(), VersionNumber: 1, SchemaVersion: 2, DefinitionJSON: json.RawMessage(`{"schemaVersion":2,"isVideo":true}`), CanonicalDigest: "task3-video-v2-empty", Status: "PUBLISHED", IdempotencyKey: "task3-video-v2-empty", PublishedAt: time.Now().UTC(), CreatedBy: identity.NewID()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&draft).Update("template_version_id", templateVersionID).Error; err != nil {
		t.Fatal(err)
	}
	_, err := service.Submit(context.Background(), tenant, responsibility, false)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidState || appErr.Field != "requirements" || appErr.Safe != "NO_ENVIRONMENTS" {
		t.Fatalf("v2 submit without environments = %v; want INVALID_STATE requirements/NO_ENVIRONMENTS", err)
	}
}

func TestSubmitEnvironmentVideoWithoutClipsEmitsEvaluatedCompleteness(t *testing.T) {
	db, service, tenant, responsibility := videoInspectionSubmitFixture(t, nil)
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	templateVersionID := identity.NewID()
	if err := db.Create(&database.TemplateVersion{ID: templateVersionID, TenantID: tenant, TemplateID: identity.NewID(), VersionNumber: 1, SchemaVersion: 2, DefinitionJSON: json.RawMessage(`{"schemaVersion":2,"isVideo":true}`), CanonicalDigest: "task3-video-v2-empty-clips", Status: "PUBLISHED", IdempotencyKey: "task3-video-v2-empty-clips", PublishedAt: now, CreatedBy: identity.NewID()}).Error; err != nil {
		t.Fatal(err)
	}
	requirements := []Requirement{
		{Key: "env:locked", Required: true, Expected: true, ImpossibilityAllowed: true, MinimumMedia: 1, MaximumMedia: 3},
		{Key: "env:new:bedroom", Required: true, AddedDuringCapture: true, ImpossibilityAllowed: true, MinimumMedia: 1, MaximumMedia: 3},
	}
	encoded, _ := json.Marshal(requirements)
	if err := db.Model(&draft).Updates(map[string]any{"template_version_id": templateVersionID, "requirements": encoded}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: tenant, DraftID: draft.ID, RequirementKey: "env:locked", MediaIDs: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), ImpossibilityReason: "UNSAFE", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	inspectionID := identity.NewID()
	if err := db.Create(&database.Responsibility{ID: responsibility, TenantID: tenant, InspectionID: inspectionID, ParticipantID: identity.NewID(), Status: "IN_PROGRESS", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	submission, err := service.Submit(context.Background(), tenant, responsibility, true)
	if err != nil {
		t.Fatal(err)
	}
	if submission.Complete || !submission.RequiresAttention {
		t.Fatalf("submission completeness = complete:%v attention:%v; want false/true", submission.Complete, submission.RequiresAttention)
	}
	var outbox database.OutboxIntent
	if err := db.Where("tenant_id=? AND type='capture.submitted.v1'", tenant).Take(&outbox).Error; err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Payload struct {
			Complete          bool `json:"complete"`
			RequiresAttention bool `json:"requiresAttention"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(outbox.Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Payload.Complete || !envelope.Payload.RequiresAttention {
		t.Fatalf("outbox completeness = complete:%v attention:%v; want false/true", envelope.Payload.Complete, envelope.Payload.RequiresAttention)
	}
}

func TestSubmitOriginEnvironmentVideoReturnsStableInvalidState(t *testing.T) {
	db, service, tenant, responsibility := videoInspectionSubmitFixture(t, nil)
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	templateVersionID := identity.NewID()
	if err := db.Create(&database.TemplateVersion{ID: templateVersionID, TenantID: tenant, TemplateID: identity.NewID(), VersionNumber: 1, SchemaVersion: 2, DefinitionJSON: json.RawMessage(`{"schemaVersion":2,"isVideo":true}`), CanonicalDigest: "task3-origin-video-v2", Status: "PUBLISHED", IdempotencyKey: "task3-origin-video-v2", PublishedAt: time.Now().UTC(), CreatedBy: identity.NewID()}).Error; err != nil {
		t.Fatal(err)
	}
	requirements, _ := json.Marshal([]Requirement{{Key: "env:new:room", Required: true, AddedDuringCapture: true, ImpossibilityAllowed: true, MinimumMedia: 1, MaximumMedia: 3}})
	if err := db.Model(&draft).Updates(map[string]any{"kind": "ORIGIN", "template_version_id": templateVersionID, "requirements": requirements}).Error; err != nil {
		t.Fatal(err)
	}
	_, err := service.Submit(context.Background(), tenant, responsibility, true)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidState || appErr.Field != "responsibility" || appErr.Safe != "environment video capture is unavailable for origin drafts" {
		t.Fatalf("origin v2 video submit = %v; want stable INVALID_STATE", err)
	}
}

func stringPtr(value string) *string { return &value }

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
	if err := db.Create(&database.CaptureDraft{ID: draftID, TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "VIDEO", ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{"gpsRequired":false,"allowGallery":false}`), Requirements: []byte(`[{"key":"video:main","maximumMedia":1}]`), Status: "OPEN", Version: 1}).Error; err != nil {
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
