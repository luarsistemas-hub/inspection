//go:build integration

package expire_video_sources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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

type failOnceDeleteClient struct {
	objectstore.Client
	deleter objectstore.Deleter
	failKey string
	failed  bool
}

func (c *failOnceDeleteClient) Delete(ctx context.Context, bucket, key string) error {
	if key == c.failKey && !c.failed {
		c.failed = true
		return errors.New("injected proxy deletion failure")
	}
	return c.deleter.Delete(ctx, bucket, key)
}

func TestExpiryRetriesProxyAfterOriginalWasDeletedIT605(t *testing.T) {
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
	firstReady := now.Add(-48 * time.Hour)
	expiry := now.Add(-time.Hour)
	retentionDays := 5
	mediaID, attemptID, preparationID := identity.NewID(), identity.NewID(), identity.NewID()
	originalKey := fmt.Sprintf("video-sources/it605/%s", mediaID)
	proxyKey := fmt.Sprintf("video-proxies/it605/%s.mp4", mediaID)
	for _, key := range []string{originalKey, proxyKey} {
		if err := h.Store.Client.Put(ctx, h.Store.Bucket, key, strings.NewReader("video-bytes"), int64(len("video-bytes")), "video/mp4"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = h.Store.Delete(context.Background(), originalKey)
		_ = h.Store.Delete(context.Background(), proxyKey)
	})
	digestBytes := sha256.Sum256([]byte("video-bytes"))
	digest := hex.EncodeToString(digestBytes[:])
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "it605-attempt", PayloadDigest: digest, AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: originalKey, ContentType: "video/mp4", SHA256: digest, SizeBytes: int64(len("video-bytes")), Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "it605-media", AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &firstReady, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &firstReady, RetentionDays: &retentionDays, SourceExpiryAt: &expiry, Status: "RETAINED", CreatedAt: firstReady, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.MediaDerivative{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Kind: "VIDEO_PLAYBACK", ObjectKey: proxyKey, ContentType: "video/mp4", SHA256: digest, Width: 2, Height: 2, SizeBytes: int64(len("video-bytes")), Profile: "it605", CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	client := &failOnceDeleteClient{Client: h.Store.Client, deleter: h.Store.Client.(objectstore.Deleter), failKey: proxyKey}
	executor, err := Setup(Dependencies{DB: h.DB, Store: objectstore.Store{Bucket: h.Store.Bucket, Client: client}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := executor.RunDue(ctx, fixture.TenantID, now, 1)
	if claimed != 1 || err == nil {
		t.Fatalf("first sweep = claimed %d, error %v; want one claim and proxy failure", claimed, err)
	}
	var source database.VideoSource
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Take(&source).Error
	}); err != nil {
		t.Fatal(err)
	}
	if source.Status != "DELETING" || source.DeleteErrorCode != "SOURCE_DELETE_FAILED" || source.DeleteLeaseUntil == nil || source.DeleteLeaseUntil.After(now) {
		t.Fatalf("after partial failure source status=%s error=%s lease=%v; want retryable DELETING", source.Status, source.DeleteErrorCode, source.DeleteLeaseUntil)
	}
	if reader, _, err := (objectstore.VideoStore{Store: h.Store}).Open(ctx, originalKey); reader != nil || !errors.Is(err, objectstore.ErrNotFound) {
		if reader != nil {
			_ = reader.Close()
		}
		t.Fatalf("original after first sweep reader=%v err=%v; want deleted", reader, err)
	}
	if reader, _, err := (objectstore.VideoStore{Store: h.Store}).Open(ctx, proxyKey); err != nil {
		t.Fatalf("proxy should remain for retry: %v", err)
	} else {
		_ = reader.Close()
	}
	claimed, err = executor.RunDue(ctx, fixture.TenantID, now, 1)
	if claimed != 1 || err != nil {
		t.Fatalf("retry sweep = claimed %d, error %v; want cleanup completion", claimed, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Take(&source).Error
	}); err != nil {
		t.Fatal(err)
	}
	if source.Status != "EXPIRED" || source.DeleteErrorCode != "" || source.OriginalDeletedAt == nil {
		t.Fatalf("after retry source status=%s error=%s deletedAt=%v; want EXPIRED", source.Status, source.DeleteErrorCode, source.OriginalDeletedAt)
	}
	if reader, _, err := (objectstore.VideoStore{Store: h.Store}).Open(ctx, proxyKey); reader != nil || !errors.Is(err, objectstore.ErrNotFound) {
		if reader != nil {
			_ = reader.Close()
		}
		t.Fatalf("proxy after retry reader=%v err=%v; want deleted", reader, err)
	}
}
