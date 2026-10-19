//go:build integration

package expire_video_sources

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

type missingOriginalStore struct {
	expiryStoreStub
	deleted []string
}

func (s *missingOriginalStore) Delete(_ context.Context, _, key string) error {
	s.deleted = append(s.deleted, key)
	return objectstore.ErrNotFound
}

func TestMissingOriginalExpiryKeepsFrameBankUT069(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	firstReady := now.Add(-7 * 24 * time.Hour)
	expiry := firstReady.Add(5 * 24 * time.Hour)
	attemptID, mediaID, submissionID := identity.NewID(), identity.NewID(), identity.NewID()
	preparationID, frameID := identity.NewID(), identity.NewID()
	digest := strings.Repeat("b", 64)
	originalKey := "tenant/" + fixture.TenantID.String() + "/media/" + mediaID.String()
	frameKey := "video-frames/" + fixture.TenantID.String() + "/" + frameID.String() + ".jpg"
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		attempt := database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "missing-original", PayloadDigest: digest, AdmittedAt: firstReady, ExpiresAt: now, CreatedAt: firstReady}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		media := database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: originalKey, ContentType: "video/mp4", SHA256: digest, SizeBytes: 100, Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "missing-original-video", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: firstReady}
		if err := tx.Create(&media).Error; err != nil {
			return err
		}
		submission := database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: fixture.DraftID, ResponsibilityID: fixture.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: digest, SubmittedAt: firstReady}
		if err := tx.Create(&submission).Error; err != nil {
			return err
		}
		preparation := database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: firstReady, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &firstReady, CreatedAt: firstReady}
		if err := tx.Create(&preparation).Error; err != nil {
			return err
		}
		days := 5
		source := database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &firstReady, RetentionDays: &days, SourceExpiryAt: &expiry, Status: "RETAINED", CreatedAt: firstReady, UpdatedAt: firstReady}
		if err := tx.Create(&source).Error; err != nil {
			return err
		}
		frame := database.VideoFrame{ID: frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: digest, ObjectKey: frameKey, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 10, CreatedAt: firstReady}
		if err := tx.Create(&frame).Error; err != nil {
			return err
		}
		return tx.Model(&database.MediaObject{}).Where("tenant_id=? AND id=?", fixture.TenantID, mediaID).Update("status", "READY").Error
	}); err != nil {
		t.Fatal(err)
	}
	client := &missingOriginalStore{}
	executor, err := Setup(Dependencies{DB: h.DB, Store: objectstore.Store{Bucket: "missing-original-test", Client: client}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := executor.RunDue(ctx, fixture.TenantID, now, 1)
	if err != nil || claimed != 1 {
		t.Fatalf("RunDue() = claimed=%d err=%v; want one successful expiration", claimed, err)
	}
	if len(client.deleted) != 1 || client.deleted[0] != originalKey {
		t.Fatalf("deleted keys=%v, want only missing original %q", client.deleted, originalKey)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var source database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Take(&source).Error; err != nil {
			return err
		}
		if source.Status != "EXPIRED" || source.OriginalDeletedAt == nil || !source.OriginalDeletedAt.Equal(now) {
			t.Errorf("source after missing-object deletion = status %s, deleted at %v", source.Status, source.OriginalDeletedAt)
		}
		var frames int64
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND id=? AND object_key=?", fixture.TenantID, frameID, frameKey).Count(&frames).Error; err != nil {
			return err
		}
		if frames != 1 {
			t.Errorf("retained frame count=%d, want 1", frames)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExpirySweepSkipsSourceWithoutFirstReadinessIT314(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(-time.Hour)
	mediaID, attemptID := identity.NewID(), identity.NewID()
	digest := strings.Repeat("d", 64)
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		attempt := database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "unready-source", PayloadDigest: digest, AdmittedAt: due, ExpiresAt: now.Add(time.Hour), CreatedAt: due}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		media := database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "tenant/" + fixture.TenantID.String() + "/media/" + mediaID.String(), ContentType: "video/mp4", SHA256: digest, SizeBytes: 100, Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "unready-source-video", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: due}
		if err := tx.Create(&media).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "PENDING", SourceExpiryAt: &due, Status: "RETAINED", CreatedAt: due, UpdatedAt: due}).Error
	}); err != nil {
		t.Fatal(err)
	}
	client := &missingOriginalStore{}
	executor, err := Setup(Dependencies{DB: h.DB, Store: objectstore.Store{Bucket: "unready-source-test", Client: client}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := executor.RunDue(ctx, fixture.TenantID, now, 25)
	if err != nil || claimed != 0 || len(client.deleted) != 0 {
		t.Fatalf("unready source sweep = claimed=%d deleted=%v err=%v", claimed, client.deleted, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var source database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Take(&source).Error; err != nil {
			return err
		}
		if source.Status != "RETAINED" || source.FirstFrameReadyAt != nil || source.OriginalDeletedAt != nil {
			t.Errorf("unready source changed during sweep: status=%s ready=%v deleted=%v", source.Status, source.FirstFrameReadyAt, source.OriginalDeletedAt)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExpirySweepBeforeBankValidationDoesNotInventReadinessDeadlineIT320(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	mediaID, preparationID := identity.NewID(), identity.NewID()
	digest := strings.Repeat("e", 64)
	key := "it320/source/" + mediaID.String()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: key, ContentType: "video/mp4", SHA256: digest, SizeBytes: 20, Status: "RECEIVED", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "it320-media-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, Status: "PROCESSING", Phase: "VALIDATING", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: digest, PendingObjectKeys: []byte(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", Status: "RETAINED", CreatedAt: now, UpdatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := executor.RunDue(ctx, fixture.TenantID, now.Add(24*time.Hour), 25)
	if err != nil || claimed != 0 {
		t.Fatalf("pre-validation sweep claimed=%d err=%v; want no claim", claimed, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var source database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Take(&source).Error; err != nil {
			return err
		}
		if source.FirstFrameReadyAt != nil || source.SourceExpiryAt != nil || source.Status != "RETAINED" || source.OriginalDeletedAt != nil {
			t.Errorf("sweep invented readiness/deadline or changed source: %+v", source)
		}
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, preparationID).Take(&job).Error; err != nil {
			return err
		}
		if job.Status != "PROCESSING" || job.Phase != "VALIDATING" {
			t.Errorf("validation job changed during sweep: status=%s phase=%s", job.Status, job.Phase)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
