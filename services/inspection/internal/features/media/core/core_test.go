package core

import (
	"context"
	"errors"
	"image"
	"io"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/sensitivecontent"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUT032SupportedBoundaries(t *testing.T) {
	for _, kind := range []string{"image/jpeg", "image/png", "image/webp", "image/heic", "image/heif"} {
		if err := ValidateAdmission(kind, objectstore.MaxOriginalBytes, 199); err != nil {
			t.Fatalf("%s boundary rejected: %v", kind, err)
		}
	}
}

func TestUT033RejectedMediaBoundaries(t *testing.T) {
	for _, tc := range []struct {
		kind   string
		size   int64
		active int
	}{{"text/plain", 1, 0}, {"image/jpeg", objectstore.MaxOriginalBytes + 1, 0}, {"image/jpeg", 1, 200}} {
		if ValidateAdmission(tc.kind, tc.size, tc.active) == nil {
			t.Fatalf("accepted invalid input: %+v", tc)
		}
	}
}

func TestIT193ScreeningAttemptLimitIsExplicit(t *testing.T) {
	if MaxScreeningAttempts != 3 {
		t.Fatalf("unexpected screening attempt limit: %d", MaxScreeningAttempts)
	}
}

func TestScreeningDetectorFailuresConsumeRetryBudget(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"media", "messaging"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.MediaObject{}, &database.ScreeningRun{}, &database.OutboxIntent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenantID, mediaID := identity.NewID(), identity.NewID()
	if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenantID, ResponsibilityID: identity.NewID(), ObjectKey: "private/original", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 1, Status: "VERIFIED"}).Error; err != nil {
		t.Fatal(err)
	}
	service := Service{
		DB:       db,
		Detector: sensitivecontent.Detector{Model: failingModel{}},
		Now:      func() time.Time { return time.Unix(100, 0).UTC() },
		Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
			return fn(db)
		},
	}
	for attempt := 0; attempt < MaxScreeningAttempts; attempt++ {
		if _, err := service.Screen(context.Background(), tenantID, mediaID, []byte("invalid")); err == nil || code(err) != apperror.DependencyUnavailable {
			t.Fatalf("attempt %d returned %v", attempt+1, err)
		}
	}
	if _, err := service.Screen(context.Background(), tenantID, mediaID, []byte("invalid")); code(err) != apperror.RateLimited {
		t.Fatalf("retry budget was not enforced: %v", err)
	}
	var attempts int64
	if err := db.Model(&database.ScreeningRun{}).Where("tenant_id=? AND media_id=?", tenantID, mediaID).Count(&attempts).Error; err != nil || attempts != MaxScreeningAttempts {
		t.Fatalf("persisted attempts=%d err=%v", attempts, err)
	}
}

func TestIT032MultipartReconciliationPersistsProviderStateAndReturnsOnlyMissingParts(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"media", "capture"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.MediaObject{}, &database.MultipartUpload{}, &database.UploadPart{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	if err := db.Exec("CREATE UNIQUE INDEX media.idx_upload_parts_reconciliation ON upload_parts (tenant_id, upload_id, part)").Error; err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	tenantID, responsibilityID, mediaID, uploadID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "INSPECTION", Requirements: []byte(`[]`), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Status: "OPEN", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenantID, ResponsibilityID: responsibilityID, ObjectKey: "tenant/object", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: objectstore.PartSizeBytes + 1, Status: "UPLOADING", Flags: []byte(`[]`), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.MultipartUpload{ID: uploadID, TenantID: tenantID, MediaID: mediaID, UploadID: "remote-upload", ObjectKey: "tenant/object", Status: "UPLOADING", ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.UploadPart{ID: identity.NewID(), TenantID: tenantID, UploadID: uploadID, Part: 2, ETag: "stale-etag", SizeBytes: 1, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	client := &reconciliationClient{parts: []objectstore.UploadedPart{{Number: 1, ETag: "etag-1", SizeBytes: objectstore.PartSizeBytes}}}
	service := Service{DB: db, Store: objectstore.Store{Bucket: "private", Client: client}, Now: func() time.Time { return now }, Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) }}

	state, err := service.Reconcile(context.Background(), tenantID, responsibilityID, mediaID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "UPLOADING" || len(state.Received) != 1 || len(state.MissingParts) != 1 || state.MissingParts[0] != 2 {
		t.Fatalf("unexpected recovery state: %+v", state)
	}
	var persisted database.UploadPart
	if err := db.Where("tenant_id=? AND upload_id=? AND part=?", tenantID, uploadID, 1).First(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.ETag != "etag-1" || persisted.SizeBytes != objectstore.PartSizeBytes {
		t.Fatalf("provider state was not persisted: %+v", persisted)
	}
	var stale int64
	if err := db.Model(&database.UploadPart{}).Where("tenant_id=? AND upload_id=? AND part=?", tenantID, uploadID, 2).Count(&stale).Error; err != nil || stale != 0 {
		t.Fatalf("stale local part survived reconciliation: count=%d err=%v", stale, err)
	}
	if _, err := service.Presign(context.Background(), tenantID, responsibilityID, mediaID, []int{1}); code(err) != apperror.InvalidState {
		t.Fatalf("received part was presigned again: %v", err)
	}
	if _, err := service.Presign(context.Background(), tenantID, responsibilityID, mediaID, []int{2}); err != nil {
		t.Fatalf("missing part could not be presigned: %v", err)
	}
}

type reconciliationClient struct {
	parts []objectstore.UploadedPart
}

func (c *reconciliationClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (c *reconciliationClient) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "", nil
}
func (c *reconciliationClient) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (c *reconciliationClient) AbortMultipart(context.Context, string, string, string) error {
	return nil
}
func (c *reconciliationClient) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{}, nil
}
func (c *reconciliationClient) Get(context.Context, string, string) (io.ReadCloser, error) {
	return nil, nil
}
func (c *reconciliationClient) Put(context.Context, string, string, io.Reader, int64, string) error {
	return nil
}
func (c *reconciliationClient) ListMultipartParts(context.Context, string, string, string) ([]objectstore.UploadedPart, error) {
	return c.parts, nil
}

type failingModel struct{}

func (failingModel) Detect(context.Context, image.Image) ([]sensitivecontent.Region, error) {
	return nil, errors.New("detector unavailable")
}

func (failingModel) Bytes() []byte { return []byte("failing-model") }

func code(err error) apperror.Code {
	value, _, _ := apperror.Public(err)
	return value
}
