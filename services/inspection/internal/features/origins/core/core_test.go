package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestIT091IT092IT095IT097ActivationPreservesHistory(t *testing.T) {
	f := newOriginActivationFixture(t)
	first, err := f.service.ActivateCompleted(context.Background(), f.tenantID, f.firstResponsibilityID)
	if err != nil || first.Status != "ACTIVE" {
		t.Fatalf("first valid origin was not activated: %+v %v", first, err)
	}
	replayed, err := f.service.ActivateCompleted(context.Background(), f.tenantID, f.firstResponsibilityID)
	if err != nil || replayed.ID != first.ID || replayed.Status != "ACTIVE" {
		t.Fatalf("activation replay changed the active version: %+v %v", replayed, err)
	}

	secondResponsibility := identity.NewID()
	secondVersion := database.OriginVersion{ID: identity.NewID(), TenantID: f.tenantID, OriginID: f.originID, VersionNumber: 2, ResponsibilityID: secondResponsibility, SupersedesID: &first.ID, Status: "DRAFT", IdempotencyKey: "origin-second", CreatedAt: f.now}
	secondDraft := database.CaptureDraft{ID: identity.NewID(), TenantID: f.tenantID, ResponsibilityID: secondResponsibility, Kind: "ORIGIN", TemplateVersionID: identity.NewID(), ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: mustJSONOrigin([]capturecore.Requirement{{Key: "room", Required: true, MinimumMedia: 1, MaximumMedia: 1}}), Status: "SUBMITTED", Version: 2, CreatedAt: f.now, UpdatedAt: f.now}
	secondMedia := database.MediaObject{ID: identity.NewID(), TenantID: f.tenantID, ResponsibilityID: secondResponsibility, ObjectKey: "origin/second", ContentType: "image/jpeg", SHA256: strings.Repeat("b", 64), SizeBytes: 10, Status: "READY", RequirementKey: "room", Description: "second view", CaptureSource: "CAMERA", Flags: json.RawMessage(`[]`), CreatedAt: f.now}
	for _, row := range []any{&secondVersion, &secondDraft, &secondMedia} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	second, err := f.service.ActivateCompleted(context.Background(), f.tenantID, secondResponsibility)
	if err != nil || second.Status != "ACTIVE" {
		t.Fatalf("replacement origin was not activated: %+v %v", second, err)
	}
	var prior database.OriginVersion
	if err := f.db.First(&prior, "id=?", first.ID).Error; err != nil || prior.Status != "SUPERSEDED" {
		t.Fatalf("prior origin history was rewritten incorrectly: %+v %v", prior, err)
	}
	var origin database.Origin
	if err := f.db.First(&origin, "id=?", f.originID).Error; err != nil || origin.ActiveVersionID == nil || *origin.ActiveVersionID != second.ID {
		t.Fatalf("active origin pointer is wrong: %+v %v", origin, err)
	}
	var evidence int64
	if err := f.db.Model(&database.OriginEvidence{}).Where("tenant_id=?", f.tenantID).Count(&evidence).Error; err != nil || evidence != 2 {
		t.Fatalf("origin evidence history count=%d err=%v", evidence, err)
	}
}

func TestIT091ActivationRejectsUndescribedOriginEvidence(t *testing.T) {
	f := newOriginActivationFixture(t)
	if err := f.db.Model(&database.MediaObject{}).Where("id=?", f.firstMediaID).Update("description", " ").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ActivateCompleted(context.Background(), f.tenantID, f.firstResponsibilityID); originCode(err) != apperror.InvalidState {
		t.Fatalf("undescribed origin evidence accepted: %v", err)
	}
}

type originActivationFixture struct {
	db                                                      *gorm.DB
	service                                                 Service
	tenantID, originID, firstResponsibilityID, firstMediaID identity.ID
	now                                                     time.Time
}

func newOriginActivationFixture(t *testing.T) originActivationFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"origins", "capture", "media", "messaging"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.Origin{}, &database.OriginVersion{}, &database.OriginEvidence{}, &database.CaptureDraft{}, &database.MediaObject{}, &database.OutboxIntent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	tenantID, originID, responsibilityID, mediaID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	origin := database.Origin{ID: originID, TenantID: tenantID, AssetID: identity.NewID(), TemplateID: identity.NewID(), Version: 1, CreatedAt: now, UpdatedAt: now}
	version := database.OriginVersion{ID: identity.NewID(), TenantID: tenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: responsibilityID, Status: "DRAFT", IdempotencyKey: "origin-first", CreatedAt: now}
	draft := database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "ORIGIN", TemplateVersionID: identity.NewID(), ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: mustJSONOrigin([]capturecore.Requirement{{Key: "room", Required: true, MinimumMedia: 1, MaximumMedia: 1}}), Status: "SUBMITTED", Version: 2, CreatedAt: now, UpdatedAt: now}
	media := database.MediaObject{ID: mediaID, TenantID: tenantID, ResponsibilityID: responsibilityID, ObjectKey: "origin/first", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 10, Status: "READY", RequirementKey: "room", Description: "first view", CaptureSource: "CAMERA", Flags: json.RawMessage(`[]`), CreatedAt: now}
	for _, row := range []any{&origin, &version, &draft, &media} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return originActivationFixture{db: db, service: Service{DB: db, Now: func() time.Time { return now }, Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}}, tenantID: tenantID, originID: originID, firstResponsibilityID: responsibilityID, firstMediaID: mediaID, now: now}
}

func mustJSONOrigin(value any) json.RawMessage {
	payload, _ := json.Marshal(value)
	return payload
}

func originCode(err error) apperror.Code {
	value, _, _ := apperror.Public(err)
	return value
}
