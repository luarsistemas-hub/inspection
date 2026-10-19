package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func videoUploadDB(t *testing.T) (*gorm.DB, func(context.Context, identity.ID, func(*gorm.DB) error) error) {
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
	for _, schema := range []string{"capture", "media", "inspections"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("CREATE TABLE inspections.responsibilities (id TEXT PRIMARY KEY, tenant_id TEXT, status TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&database.VideoAttempt{}, &database.CaptureDraft{}, &database.MediaObject{}, &database.MultipartUpload{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	return db, func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) }
}

func TestVideoPresignRejectsBatchAboveSixteenUT017(t *testing.T) {
	db, within := videoUploadDB(t)
	tenant, responsibility, mediaID, uploadID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenant, ResponsibilityID: responsibility, ObjectKey: "private/video", ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: int64(17 * objectstore.PartSizeBytes), Status: "UPLOADING", MediaKind: "VIDEO", RequirementKey: "video:main"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.MultipartUpload{ID: uploadID, TenantID: tenant, MediaID: mediaID, UploadID: "remote", ObjectKey: "private/video", Status: "UPLOADING", ExpiresAt: now.Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	service := Service{DB: db, Store: objectstore.Store{Bucket: "private", Client: &reconciliationClient{}}, Now: func() time.Time { return now }, Within: within}
	parts := make([]int, 17)
	for i := range parts {
		parts[i] = i + 1
	}
	_, err := service.Presign(context.Background(), tenant, responsibility, mediaID, parts)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidInput {
		t.Fatalf("17-part batch was not rejected as INVALID_INPUT: %v", err)
	}
}

func TestVideoUploadReplayWithChangedHashConflictsUT018(t *testing.T) {
	db, within := videoUploadDB(t)
	tenant, responsibility, attemptID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.VideoAttempt{ID: attemptID, TenantID: tenant, ResponsibilityID: responsibility, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: objectstore.MaxVideoBytes, AcquisitionSource: "CAMERA", Status: "ACTIVE", IdempotencyKey: "attempt", PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	service := Service{DB: db, Store: objectstore.Store{Bucket: "private", Client: &reconciliationClient{}}, Now: func() time.Time { return now }, Within: within}
	if _, err := service.CreateVideoWithAttempt(context.Background(), tenant, responsibility, attemptID, "video/mp4", strings.Repeat("a", 64), 1024, "upload-key"); err != nil {
		t.Fatal(err)
	}
	_, err := service.CreateVideoWithAttempt(context.Background(), tenant, responsibility, attemptID, "video/mp4", strings.Repeat("b", 64), 1024, "upload-key")
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.Conflict {
		t.Fatalf("changed replay hash was accepted: %v", err)
	}
}

func TestIT012PhotoUploadRequiresPhotoDraft(t *testing.T) {
	t.Run("rejects VIDEO draft before replay or object store", func(t *testing.T) {
		db, within := videoUploadDB(t)
		tenant, responsibility := identity.NewID(), identity.NewID()
		if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1}).Error; err != nil {
			t.Fatal(err)
		}
		mediaID, uploadID := identity.NewID(), identity.NewID()
		if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenant, ResponsibilityID: responsibility, ObjectKey: "private/stale-photo", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 12, Status: "UPLOADING", IdempotencyKey: "photo-key"}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&database.MultipartUpload{ID: uploadID, TenantID: tenant, MediaID: mediaID, UploadID: "stale-upload", ObjectKey: "private/stale-photo", Status: "UPLOADING", ExpiresAt: time.Now().Add(time.Hour)}).Error; err != nil {
			t.Fatal(err)
		}
		client := &uploadAdmissionClient{}
		service := Service{DB: db, Store: objectstore.Store{Bucket: "private", Client: client}, Within: within}
		for _, key := range []string{"new-key", "photo-key"} {
			_, err := service.CreateWithKey(context.Background(), tenant, responsibility, "image/jpeg", strings.Repeat("a", 64), 12, key)
			if code(err) != apperror.InvalidInput {
				t.Fatalf("key %q returned %v, want INVALID_INPUT", key, err)
			}
		}
		if client.creates != 0 {
			t.Fatalf("object store CreateMultipart called %d times", client.creates)
		}
		var count int64
		if err := db.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("unexpected media rows: count=%d err=%v", count, err)
		}
	})

	t.Run("PHOTO draft remains admissible", func(t *testing.T) {
		db, within := videoUploadDB(t)
		tenant, responsibility := identity.NewID(), identity.NewID()
		if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "PHOTO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1}).Error; err != nil {
			t.Fatal(err)
		}
		client := &uploadAdmissionClient{}
		service := Service{DB: db, Store: objectstore.Store{Bucket: "private", Client: client}, Within: within}
		if _, err := service.CreateWithKey(context.Background(), tenant, responsibility, "image/jpeg", strings.Repeat("b", 64), 12, "photo-key"); err != nil {
			t.Fatalf("PHOTO upload rejected: %v", err)
		}
		if client.creates != 1 {
			t.Fatalf("object store CreateMultipart calls=%d, want 1", client.creates)
		}
	})
}

func TestPhotoUploadReplayAfterDraftSubmittedReturnsExistingUpload(t *testing.T) {
	db, within := videoUploadDB(t)
	tenant, responsibility := identity.NewID(), identity.NewID()
	mediaID, multipartID := identity.NewID(), identity.NewID()
	hash := strings.Repeat("a", 64)
	expiresAt := time.Now().UTC().Add(time.Hour)
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "PHOTO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "SUBMITTED", Version: 2}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenant, ResponsibilityID: responsibility, ObjectKey: "private/existing-photo", ContentType: "image/jpeg", SHA256: hash, SizeBytes: 12, Status: "UPLOADING", IdempotencyKey: "photo-key"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.MultipartUpload{ID: multipartID, TenantID: tenant, MediaID: mediaID, UploadID: "existing-multipart", ObjectKey: "private/existing-photo", Status: "UPLOADING", ExpiresAt: expiresAt}).Error; err != nil {
		t.Fatal(err)
	}
	client := &uploadAdmissionClient{}
	service := Service{DB: db, Store: objectstore.Store{Bucket: "private", Client: client}, Within: within}

	got, err := service.CreateWithKey(context.Background(), tenant, responsibility, "image/jpeg", hash, 12, "photo-key")
	if err != nil {
		t.Fatalf("PHOTO replay after submission failed: %v", err)
	}
	if got.MediaID != mediaID || got.UploadID != "existing-multipart" || got.ObjectKey != "private/existing-photo" || !got.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("replay returned a different upload: got=%+v", got)
	}
	if client.creates != 0 {
		t.Fatalf("object store CreateMultipart called %d times", client.creates)
	}
}

func TestVideoUploadRejectsPhotoDraftIT012(t *testing.T) {
	db, within := videoUploadDB(t)
	tenant, responsibility, attemptID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "PHOTO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.VideoAttempt{ID: attemptID, TenantID: tenant, ResponsibilityID: responsibility, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: objectstore.MaxVideoBytes, AcquisitionSource: "CAMERA", Status: "ACTIVE", IdempotencyKey: "attempt", PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	client := &uploadAdmissionClient{}
	service := Service{DB: db, Store: objectstore.Store{Bucket: "private", Client: client}, Now: func() time.Time { return now }, Within: within}
	_, err := service.CreateVideoWithAttempt(context.Background(), tenant, responsibility, attemptID, "video/mp4", strings.Repeat("a", 64), 1024, "video-key")
	if code(err) != apperror.InvalidInput {
		t.Fatalf("VIDEO upload returned %v, want INVALID_INPUT", err)
	}
	if client.creates != 0 {
		t.Fatalf("object store CreateMultipart called %d times", client.creates)
	}
	var count int64
	if err := db.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("unexpected media rows: count=%d err=%v", count, err)
	}
}

type uploadAdmissionClient struct {
	reconciliationClient
	creates int
}

func (c *uploadAdmissionClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	c.creates++
	return "multipart-id", nil
}

func TestVV_IT_103ThreeRoomCurrentCaptureAdmitsOneMainVideo(t *testing.T) {
	db, within := videoUploadDB(t)
	tenant, responsibility, attemptID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	rooms := []string{"kitchen", "bathroom", "bedroom"}
	reference, err := json.Marshal(map[string]any{"comparisonMode": "CHECKLIST_ONLY", "mediaKind": "VIDEO", "observedRooms": rooms})
	if err != nil {
		t.Fatal(err)
	}
	requirements, err := json.Marshal([]map[string]any{{"key": "video:main", "evidenceKind": "VIDEO", "required": true, "minimumCount": 1, "maximumCount": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: identity.NewID(), ReferencePayload: reference, PolicyPayload: []byte(`{}`), Requirements: requirements, Status: "OPEN", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.VideoAttempt{ID: attemptID, TenantID: tenant, ResponsibilityID: responsibility, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: objectstore.MaxVideoBytes, AcquisitionSource: "CAMERA", Status: "ACTIVE", IdempotencyKey: "vv-it103-attempt", PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	client := &uploadAdmissionClient{}
	service := Service{DB: db, Store: objectstore.Store{Bucket: "private", Client: client}, Now: func() time.Time { return now }, Within: within}
	upload, err := service.CreateVideoWithAttempt(context.Background(), tenant, responsibility, attemptID, "video/mp4", strings.Repeat("b", 64), 1024, "vv-it103-main")
	if err != nil {
		t.Fatalf("three-room capture did not accept its one continuous source: %v", err)
	}
	var media database.MediaObject
	if err := db.Where("tenant_id=? AND id=?", tenant, upload.MediaID).Take(&media).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=? AND media_kind='VIDEO'", tenant, responsibility).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if media.RequirementKey != "video:main" || count != 1 || client.creates != 1 {
		t.Fatalf("three room scope created per-room uploads: requirement=%s media=%d multipart admissions=%d", media.RequirementKey, count, client.creates)
	}
}

func TestVideoUploadRateLimitsAfterOneHundredUnfinishedReceiptsIT421(t *testing.T) {
	db, within := videoUploadDB(t)
	tenant, responsibility, attemptID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.VideoAttempt{ID: attemptID, TenantID: tenant, ResponsibilityID: responsibility, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: objectstore.MaxVideoBytes, AcquisitionSource: "CAMERA", Status: "ACTIVE", IdempotencyKey: "attempt", PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	receipts := make([]database.MediaObject, 100)
	for i := range receipts {
		receipts[i] = database.MediaObject{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, ObjectKey: "private/unfinished-" + identity.NewID().String(), ContentType: "video/mp4", SHA256: strings.Repeat("b", 64), SizeBytes: 1024, Status: "UPLOADING", MediaKind: "VIDEO", IdempotencyKey: "unfinished-" + identity.NewID().String(), RequirementKey: "video:main"}
	}
	if err := db.Create(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	service := Service{DB: db, Store: objectstore.Store{Bucket: "private", Client: &reconciliationClient{}}, Now: func() time.Time { return now }, Within: within}
	if _, err := service.CreateVideoWithAttempt(context.Background(), tenant, responsibility, attemptID, "video/mp4", strings.Repeat("c", 64), 1024, "one-hundred-first"); code(err) != apperror.RateLimited {
		t.Fatalf("101st unfinished receipt was not RATE_LIMITED: %v", err)
	}
}

// IT-130: a previously saved attempt cannot start an upload after its capture closes.
func TestClosedCaptureCannotCreateVideoUploadIT130(t *testing.T) {
	db, within := videoUploadDB(t)
	tenant, responsibility, attemptID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "SUBMITTED", Version: 2}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.VideoAttempt{ID: attemptID, TenantID: tenant, ResponsibilityID: responsibility, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: objectstore.MaxVideoBytes, AcquisitionSource: "CAMERA", Status: "ACTIVE", IdempotencyKey: "attempt", PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	service := Service{DB: db, Store: objectstore.Store{Bucket: "private", Client: &reconciliationClient{}}, Now: func() time.Time { return now }, Within: within}
	_, err := service.CreateVideoWithAttempt(context.Background(), tenant, responsibility, attemptID, "video/mp4", strings.Repeat("a", 64), 1024, "saved-draft-upload")
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidState {
		t.Fatalf("closed capture admitted a video upload: %v", err)
	}
	var count int64
	if err := db.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("closed capture created media: count=%d err=%v", count, err)
	}
}

func TestVideoCompletionRecordsReceivedReceiptAfterUncertainRemoteResponse(t *testing.T) {
	db, within := videoUploadDB(t)
	if err := db.Exec("ATTACH DATABASE ':memory:' AS messaging").Error; err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&database.VideoPreparationJob{}, &database.OutboxIntent{}, &database.UploadPart{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenantID, responsibilityID, attemptID, mediaID, uploadID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	size := int64(2 * objectstore.PartSizeBytes)
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.VideoAttempt{ID: attemptID, TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: objectstore.MaxVideoBytes, AcquisitionSource: "CAMERA", Status: "ACTIVE", IdempotencyKey: "attempt", PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenantID, ResponsibilityID: responsibilityID, ObjectKey: "tenant/opaque/video", ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: size, Status: "UPLOADING", MediaKind: "VIDEO", VideoAttemptID: &attemptID, IdempotencyKey: "video-upload", RequirementKey: "video:main", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.MultipartUpload{ID: uploadID, TenantID: tenantID, MediaID: mediaID, UploadID: "remote-upload", ObjectKey: "tenant/opaque/video", Status: "UPLOADING", ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	client := &receiptReconciliationClient{head: objectstore.Head{Size: size, ContentType: "video/mp4", ChecksumSHA256: strings.Repeat("a", 64)}}
	service := Service{DB: db, Store: objectstore.Store{Bucket: "private", Client: client}, Now: func() time.Time { return now }, Within: within}
	parts := []objectstore.Part{{Number: 1, ETag: "etag-1"}, {Number: 2, ETag: "etag-2"}}
	media, err := service.Complete(context.Background(), tenantID, responsibilityID, mediaID, parts)
	if err != nil {
		t.Fatal(err)
	}
	if media.Status != "RECEIVED" || client.completions != 1 || client.headCalls != 1 {
		t.Fatalf("uncertain completion was not reconciled into a received receipt: media=%+v client=%+v", media, client)
	}
	var stored database.MediaObject
	if err := db.Where("tenant_id=? AND id=?", tenantID, mediaID).First(&stored).Error; err != nil || stored.Status != "RECEIVED" {
		t.Fatalf("received state was not persisted: %+v err=%v", stored, err)
	}
	var upload database.MultipartUpload
	if err := db.Where("tenant_id=? AND media_id=?", tenantID, mediaID).First(&upload).Error; err != nil || upload.Status != "COMPLETED" || upload.CompletedAt == nil {
		t.Fatalf("multipart completion was not persisted: %+v err=%v", upload, err)
	}
	var attempt database.VideoAttempt
	if err := db.Where("tenant_id=? AND id=?", tenantID, attemptID).First(&attempt).Error; err != nil || attempt.Status != "RECEIVED" {
		t.Fatalf("attempt receipt was not persisted: %+v err=%v", attempt, err)
	}
	var outboxCount int64
	if err := db.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type=?", tenantID, "media.upload_completed.v1").Count(&outboxCount).Error; err != nil || outboxCount != 1 {
		t.Fatalf("receipt event count=%d err=%v", outboxCount, err)
	}
	var preparation database.VideoPreparationJob
	if err := db.Where("tenant_id=? AND media_id=?", tenantID, mediaID).Take(&preparation).Error; err != nil || preparation.Status != "QUEUED" || preparation.SubmissionID != nil {
		t.Fatalf("receipt did not schedule pre-submission preparation: job=%+v err=%v", preparation, err)
	}
	status, err := service.Reconcile(context.Background(), tenantID, responsibilityID, mediaID)
	if err != nil || status.Status != "RECEIVED" || len(status.Received) != 2 || status.Received[0].Number != 1 || status.Received[1].Number != 2 {
		t.Fatalf("receipt status did not expose the server-confirmed parts: status=%+v err=%v", status, err)
	}
}

type receiptReconciliationClient struct {
	reconciliationClient
	head        objectstore.Head
	completions int
	headCalls   int
}

func (c *receiptReconciliationClient) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	c.completions++
	return errors.New("completion response lost")
}

func (c *receiptReconciliationClient) Head(context.Context, string, string) (objectstore.Head, error) {
	c.headCalls++
	return c.head, nil
}
