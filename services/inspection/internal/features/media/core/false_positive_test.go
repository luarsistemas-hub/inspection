package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestIT191IT194IT195IT197FalsePositiveIsScopedAuditedAndIdempotent(t *testing.T) {
	f := newFalsePositiveFixture(t)
	if err := f.service.DeclareFalsePositive(context.Background(), f.tenantID, f.responsibilityID, f.mediaID, " "); code(err) != apperror.InvalidInput {
		t.Fatalf("blank false-positive reason accepted: %v", err)
	}
	if err := f.service.DeclareFalsePositive(context.Background(), f.tenantID, identity.NewID(), f.mediaID, "not mine"); code(err) != apperror.NotFound && code(err) != apperror.InvalidState {
		t.Fatalf("foreign responsibility changed media: %v", err)
	}
	if err := f.service.DeclareFalsePositive(context.Background(), f.tenantID, f.responsibilityID, f.mediaID, "participant reviewed image"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeclareFalsePositive(context.Background(), f.tenantID, f.responsibilityID, f.mediaID, "participant reviewed image"); err != nil {
		t.Fatalf("same declaration was not idempotent: %v", err)
	}
	if err := f.service.DeclareFalsePositive(context.Background(), f.tenantID, f.responsibilityID, f.mediaID, "different reason"); code(err) != apperror.Conflict {
		t.Fatalf("conflicting declaration was accepted: %v", err)
	}
	var media database.MediaObject
	if err := f.db.First(&media, "id=?", f.mediaID).Error; err != nil || media.Status != "READY" || !strings.Contains(string(media.Flags), "SENSITIVE_CONTENT_FALSE_POSITIVE") {
		t.Fatalf("false-positive media state is wrong: %+v %v", media, err)
	}
	var run database.ScreeningRun
	if err := f.db.First(&run, "media_id=?", f.mediaID).Error; err != nil || run.Status != "OVERRIDDEN" || run.FalsePositiveReason == "" {
		t.Fatalf("screening override was not persisted: %+v %v", run, err)
	}
	var audits int64
	if err := f.db.Model(&database.AuditEvent{}).Where("target_id=?", f.mediaID.String()).Count(&audits).Error; err != nil || audits != 1 {
		t.Fatalf("audit count=%d err=%v", audits, err)
	}
}

type falsePositiveFixture struct {
	db                                  *gorm.DB
	service                             Service
	tenantID, responsibilityID, mediaID identity.ID
}

func newFalsePositiveFixture(t *testing.T) falsePositiveFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"capture", "media", "audit"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.MediaObject{}, &database.ScreeningRun{}, &database.AuditEvent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	now := time.Unix(1000, 0).UTC()
	tenantID, responsibilityID, mediaID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "INSPECTION", TemplateVersionID: identity.NewID(), ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[]`), Status: "OPEN", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenantID, ResponsibilityID: responsibilityID, ObjectKey: "private/original", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 10, Status: "SCREENED", Flags: json.RawMessage(`[]`)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.ScreeningRun{ID: identity.NewID(), TenantID: tenantID, MediaID: mediaID, Status: "BLOCKED", ModelDigest: "model", Regions: json.RawMessage(`[{"kind":"FACE"}]`), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	return falsePositiveFixture{db: db, service: Service{DB: db, Now: func() time.Time { return now }, Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}}, tenantID: tenantID, responsibilityID: responsibilityID, mediaID: mediaID}
}
