package finalize_submission

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	origincore "inspection/services/inspection/internal/features/origins/core"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func inspectionDB(t *testing.T) *gorm.DB {
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
	for _, schema := range []string{"capture", "inspections", "media", "recapture"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.Inspection{}, &database.Responsibility{}, &database.MediaObject{}, &database.SubmissionVersion{}, &database.CaptureDraft{}, &database.RecaptureRequest{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	return db
}

func TestFinalizeVideoOriginDoesNotActivate(t *testing.T) {
	db := inspectionDB(t)
	finalizer, err := Setup(Dependencies{Origins: origincore.Service{DB: db}, Recapture: recapturecore.Service{DB: db}})
	if err != nil {
		t.Fatal(err)
	}
	if err := finalizer.Finalize(t.Context(), db, database.CaptureDraft{TenantID: identity.NewID(), Kind: "ORIGIN", MediaKind: "VIDEO"}, database.SubmissionVersion{}); err != nil {
		t.Fatalf("video origin submission should await explicit activation: %v", err)
	}
}

func TestFinalizeVideoRecaptureReceivesSupplement(t *testing.T) {
	db := inspectionDB(t)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	tenant, responsibilityID, requestID, draftID, submissionID, mediaID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Create(&database.RecaptureRequest{ID: requestID, TenantID: tenant, InspectionID: identity.NewID(), ResponsibilityID: responsibilityID, Status: "REQUESTED", DeadlineAt: now.Add(time.Hour), IdempotencyKey: "request", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.CaptureDraft{ID: draftID, TenantID: tenant, ResponsibilityID: responsibilityID, Kind: "RECAPTURE", MediaKind: "VIDEO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "SUBMITTED", Version: 2}).Error; err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"mediaKind": "VIDEO", "mediaId": mediaID})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.SubmissionVersion{ID: submissionID, TenantID: tenant, DraftID: draftID, ResponsibilityID: responsibilityID, VersionNumber: 1, Payload: payload, PayloadDigest: strings.Repeat("a", 64), SubmittedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenant, ResponsibilityID: responsibilityID, ObjectKey: "supplement", RequirementKey: "video:supplement", ContentType: "video/webm", SHA256: strings.Repeat("b", 64), SizeBytes: 10, Status: "RECEIVED", MediaKind: "VIDEO", Flags: []byte("[]")}).Error; err != nil {
		t.Fatal(err)
	}
	f, err := Setup(Dependencies{Origins: origincore.Service{DB: db}, Recapture: recapturecore.Service{DB: db, Now: func() time.Time { return now }}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Finalize(t.Context(), db, database.CaptureDraft{TenantID: tenant, ResponsibilityID: responsibilityID, Kind: "RECAPTURE", MediaKind: "VIDEO"}, database.SubmissionVersion{ID: submissionID}); err != nil {
		t.Fatal(err)
	}
	var request database.RecaptureRequest
	if err := db.First(&request, "id = ?", requestID).Error; err != nil {
		t.Fatal(err)
	}
	if request.Status != "RECEIVED" {
		t.Fatalf("recapture status = %s; want RECEIVED", request.Status)
	}
}

func TestFinalizeInspectionVideoCountsReceivedEvidenceAndTransitions(t *testing.T) {
	db := inspectionDB(t)
	tenant, responsibilityID, inspectionID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&database.Inspection{ID: inspectionID, TenantID: tenant, BusinessUnitID: identity.NewID(), AssetID: identity.NewID(), ParticipantID: identity.NewID(), TemplateID: identity.NewID(), TemplateVersionID: identity.NewID(), Source: "MANUAL", SourceKey: "video-submit", Status: "INVITED", DueAt: now, DeadlineAt: now, ReminderInstants: []byte("[]"), ContextSnapshot: []byte("{}")}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.Responsibility{ID: responsibilityID, TenantID: tenant, InspectionID: inspectionID, ParticipantID: identity.NewID(), Status: "INVITED", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.MediaObject{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibilityID, ObjectKey: "video", ContentType: "video/webm", SHA256: strings.Repeat("a", 64), SizeBytes: 10, Status: "RECEIVED", MediaKind: "VIDEO", Flags: []byte("[]")}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.MediaObject{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibilityID, ObjectKey: "photo", ContentType: "image/jpeg", SHA256: strings.Repeat("b", 64), SizeBytes: 10, Status: "READY", MediaKind: "PHOTO", Flags: []byte("[]")}).Error; err != nil {
		t.Fatal(err)
	}
	submitted := database.SubmissionVersion{SubmittedAt: now}
	draft := database.CaptureDraft{TenantID: tenant, ResponsibilityID: responsibilityID, Kind: "INSPECTION", MediaKind: "VIDEO"}
	if err := db.Transaction(func(tx *gorm.DB) error { return finalizeInspection(tx, draft, submitted) }); err != nil {
		t.Fatal(err)
	}
	var updated database.Inspection
	if err := db.First(&updated, "id = ?", inspectionID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.Status != "ANALYZING" || updated.EvidenceCount != 1 {
		t.Fatalf("inspection finalization = status %s, evidence_count %d; want ANALYZING, 1", updated.Status, updated.EvidenceCount)
	}
	var updatedResponsibility database.Responsibility
	if err := db.First(&updatedResponsibility, "id = ?", responsibilityID).Error; err != nil {
		t.Fatal(err)
	}
	if updatedResponsibility.Status != "SUBMITTED" {
		t.Fatalf("responsibility status = %s; want SUBMITTED", updatedResponsibility.Status)
	}
}

func TestFinalizeInspectionPhotoCountsOnlyReadyMedia(t *testing.T) {
	db := inspectionDB(t)
	tenant, responsibilityID, inspectionID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&database.Inspection{ID: inspectionID, TenantID: tenant, BusinessUnitID: identity.NewID(), AssetID: identity.NewID(), ParticipantID: identity.NewID(), TemplateID: identity.NewID(), TemplateVersionID: identity.NewID(), Source: "MANUAL", SourceKey: "photo-submit", Status: "INVITED", DueAt: now, DeadlineAt: now, ReminderInstants: []byte("[]"), ContextSnapshot: []byte("{}")}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.Responsibility{ID: responsibilityID, TenantID: tenant, InspectionID: inspectionID, ParticipantID: identity.NewID(), Status: "INVITED", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	for _, object := range []database.MediaObject{
		{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibilityID, ObjectKey: "ready", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 10, Status: "READY", MediaKind: "PHOTO", Flags: []byte("[]")},
		{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibilityID, ObjectKey: "received", ContentType: "image/jpeg", SHA256: strings.Repeat("b", 64), SizeBytes: 10, Status: "RECEIVED", MediaKind: "PHOTO", Flags: []byte("[]")},
	} {
		if err := db.Create(&object).Error; err != nil {
			t.Fatal(err)
		}
	}
	draft := database.CaptureDraft{TenantID: tenant, ResponsibilityID: responsibilityID, Kind: "INSPECTION", MediaKind: "PHOTO"}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return finalizeInspection(tx, draft, database.SubmissionVersion{SubmittedAt: now})
	}); err != nil {
		t.Fatal(err)
	}
	var updated database.Inspection
	if err := db.First(&updated, "id = ?", inspectionID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.EvidenceCount != 1 {
		t.Fatalf("photo evidence_count = %d; want 1", updated.EvidenceCount)
	}
}

func TestFinalizeInspectionClosedStateReturnsInvalidStateAndRollsBack(t *testing.T) {
	db := inspectionDB(t)
	tenant, responsibilityID, inspectionID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&database.Inspection{ID: inspectionID, TenantID: tenant, BusinessUnitID: identity.NewID(), AssetID: identity.NewID(), ParticipantID: identity.NewID(), TemplateID: identity.NewID(), TemplateVersionID: identity.NewID(), Source: "MANUAL", SourceKey: "closed", Status: "COMPLETED", DueAt: now, DeadlineAt: now, ReminderInstants: []byte("[]"), ContextSnapshot: []byte("{}")}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.Responsibility{ID: responsibilityID, TenantID: tenant, InspectionID: inspectionID, ParticipantID: identity.NewID(), Status: "INVITED", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	draft := database.CaptureDraft{TenantID: tenant, ResponsibilityID: responsibilityID, Kind: "INSPECTION", MediaKind: "VIDEO"}
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&database.SubmissionVersion{ID: identity.NewID(), TenantID: tenant, DraftID: identity.NewID(), ResponsibilityID: responsibilityID, VersionNumber: 1, Payload: []byte("{}"), PayloadDigest: strings.Repeat("c", 64), SubmittedAt: now}).Error; err != nil {
			return err
		}
		return finalizeInspection(tx, draft, database.SubmissionVersion{SubmittedAt: now})
	})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidState {
		t.Fatalf("closed inspection error = %v; want INVALID_STATE", err)
	}
	var count int64
	if err := db.Model(&database.SubmissionVersion{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("submission versions after rollback = %d; want 0", count)
	}
	var responsibility database.Responsibility
	if err := db.First(&responsibility, "id = ?", responsibilityID).Error; err != nil {
		t.Fatal(err)
	}
	if responsibility.Status != "INVITED" {
		t.Fatalf("responsibility status after rollback = %s; want INVITED", responsibility.Status)
	}
}
