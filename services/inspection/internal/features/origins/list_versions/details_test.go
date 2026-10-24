package list_versions

import (
	"context"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestLoadDetailsProjectsPhotoAndVideoReadinessInBatches(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:origin-version-details-"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"origins", "media", "capture"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.Origin{}, &database.OriginVersion{}, &database.OriginEvidence{}, &database.MediaObject{}, &database.VideoPreparationJob{}, &database.VideoSource{}, &database.VideoScopeItem{}, &database.VideoFrame{}, &database.CaptureDraft{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenantID, now := identity.NewID(), time.Now().UTC()
	photoOriginID, videoOriginID, photoID, pendingPhotoID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	queuedID, queuedMediaID, queuedJobID := identity.NewID(), identity.NewID(), identity.NewID()
	readyID, readyMediaID, readyJobID, frameID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	photoResp, pendingPhotoResponsibility, queuedResponsibility, readyResponsibility := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	manifest := strings.Repeat("a", 64)
	versions := []database.OriginVersion{
		{ID: photoID, TenantID: tenantID, OriginID: photoOriginID, VersionNumber: 1, ResponsibilityID: identity.Ptr(photoResp), Status: "ACTIVE", MediaKind: "PHOTO", IdempotencyKey: "photo", CreatedAt: now},
		{ID: pendingPhotoID, TenantID: tenantID, OriginID: photoOriginID, VersionNumber: 2, ResponsibilityID: identity.Ptr(pendingPhotoResponsibility), Status: "DRAFT", MediaKind: "PHOTO", IdempotencyKey: "photo-pending", CreatedAt: now},
		{ID: queuedID, TenantID: tenantID, OriginID: videoOriginID, VersionNumber: 1, ResponsibilityID: identity.Ptr(queuedResponsibility), Status: "DRAFT", MediaKind: "VIDEO", IdempotencyKey: "queued", CreatedAt: now},
		{ID: readyID, TenantID: tenantID, OriginID: videoOriginID, VersionNumber: 2, ResponsibilityID: identity.Ptr(readyResponsibility), Status: "ACTIVE", MediaKind: "VIDEO", VideoPreparationID: &readyJobID, ScopeManifestDigest: manifest, IdempotencyKey: "ready", CreatedAt: now},
	}
	for _, origin := range []database.Origin{
		{ID: photoOriginID, TenantID: tenantID, ActiveVersionID: &photoID, Version: 2, CreatedAt: now, UpdatedAt: now},
		{ID: videoOriginID, TenantID: tenantID, ActiveVersionID: &readyID, Version: 2, CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(&origin).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range versions {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: photoResp, Kind: "ORIGIN", MediaKind: "PHOTO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "SUBMITTED"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: pendingPhotoResponsibility, Kind: "ORIGIN", MediaKind: "PHOTO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "IN_PROGRESS"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: queuedResponsibility, Kind: "ORIGIN", MediaKind: "VIDEO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "IN_PROGRESS"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&database.OriginEvidence{ID: identity.NewID(), TenantID: tenantID, OriginVersionID: photoID, MediaID: identity.NewID(), Category: "room", Description: "room", CreatedAt: now},
		&database.MediaObject{ID: queuedMediaID, TenantID: tenantID, ResponsibilityID: queuedResponsibility, ObjectKey: "origin/queued", ContentType: "video/mp4", SHA256: strings.Repeat("b", 64), SizeBytes: 1, Status: "RECEIVED", MediaKind: "VIDEO", CreatedAt: now},
		&database.VideoPreparationJob{ID: queuedJobID, TenantID: tenantID, MediaID: queuedMediaID, Status: "QUEUED", Phase: "RECEIVED", NextAttemptAt: now, CreatedAt: now},
		&database.VideoSource{ID: identity.NewID(), TenantID: tenantID, MediaID: queuedMediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1, Width: 1, Height: 1, FrameRate: 30, Status: "RETAINED", CreatedAt: now, UpdatedAt: now},
		&database.MediaObject{ID: readyMediaID, TenantID: tenantID, ResponsibilityID: readyResponsibility, ObjectKey: "origin/ready", ContentType: "video/mp4", SHA256: strings.Repeat("c", 64), SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", CreatedAt: now},
		&database.VideoPreparationJob{ID: readyJobID, TenantID: tenantID, MediaID: readyMediaID, Status: "READY", Phase: "READY", ManifestDigest: manifest, NextAttemptAt: now, CreatedAt: now},
		&database.VideoSource{ID: identity.NewID(), TenantID: tenantID, MediaID: readyMediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1, Width: 1, Height: 1, FrameRate: 30, Status: "EXPIRED", CreatedAt: now, UpdatedAt: now},
		&database.VideoFrame{ID: frameID, TenantID: tenantID, SourceMediaID: readyMediaID, PreparationID: readyJobID, PTSMicros: 0, SHA256: strings.Repeat("d", 64), ObjectKey: "origin/frame.jpg", CreatedAt: now},
		&database.VideoScopeItem{ID: identity.NewID(), TenantID: tenantID, PreparationID: readyJobID, FrameID: frameID, EnvironmentCandidate: "room", ComponentLabel: "window", ViewLabel: "front", Visibility: "CLEAR", Landmarks: []byte(`[]`), CreatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	within := func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) }
	details, err := loadDetails(context.Background(), within, tenantID, versions)
	if err != nil {
		t.Fatal(err)
	}
	if !details[photoID].Selectable || !details[photoID].CaptureSubmitted || details[photoID].PreparationStatus != nil || details[photoID].SourceAvailability != nil {
		t.Fatalf("PHOTO projection=%+v", details[photoID])
	}
	if details[pendingPhotoID].CaptureSubmitted {
		t.Fatalf("pending PHOTO projection=%+v", details[pendingPhotoID])
	}
	if details[queuedID].Selectable || details[queuedID].CaptureSubmitted || details[queuedID].PreparationStatus == nil || *details[queuedID].PreparationStatus != "RECEIVED" || details[queuedID].SourceAvailability == nil || *details[queuedID].SourceAvailability != "RETAINED" {
		t.Fatalf("pending VIDEO projection=%+v", details[queuedID])
	}
	if !details[readyID].Selectable || details[readyID].PreparationStatus == nil || *details[readyID].PreparationStatus != "READY" || details[readyID].SourceAvailability == nil || *details[readyID].SourceAvailability != "EXPIRED" {
		t.Fatalf("ready VIDEO projection=%+v", details[readyID])
	}
}
