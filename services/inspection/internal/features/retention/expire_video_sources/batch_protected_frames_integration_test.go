//go:build integration

package expire_video_sources

import (
	"context"
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

func TestSweepHundredSourcesInBatchesKeepsProtectedFramesIT322(t *testing.T) {
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
	type item struct {
		mediaID, prepID, frameID identity.ID
		sourceKey, frameKey      string
	}
	items := make([]item, 100)
	for i := range items {
		items[i] = item{mediaID: identity.NewID(), prepID: identity.NewID(), frameID: identity.NewID()}
		items[i].sourceKey = fmt.Sprintf("it322/%s/source-%03d.mp4", fixture.TenantID, i)
		items[i].frameKey = fmt.Sprintf("it322/%s/frame-%03d.jpg", fixture.TenantID, i)
		for _, object := range []struct{ key, contentType string }{{items[i].sourceKey, "video/mp4"}, {items[i].frameKey, "image/jpeg"}} {
			if err := h.Store.Client.Put(ctx, h.Store.Bucket, object.key, strings.NewReader("protected-bytes"), int64(len("protected-bytes")), object.contentType); err != nil {
				t.Fatal(err)
			}
			key := object.key
			t.Cleanup(func() { _ = h.Store.Delete(context.Background(), key) })
		}
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		for i, item := range items {
			digest := strings.Repeat("a", 64)
			media := database.MediaObject{ID: item.mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: item.sourceKey, ContentType: "video/mp4", SHA256: digest, SizeBytes: 15, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: fmt.Sprintf("it322-media-%d", i), AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: due}
			if err := tx.Create(&media).Error; err != nil {
				return err
			}
			ready := due.Add(-24 * time.Hour)
			if err := tx.Create(&database.VideoPreparationJob{ID: item.prepID, TenantID: fixture.TenantID, MediaID: item.mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: ready, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &ready, CreatedAt: ready}).Error; err != nil {
				return err
			}
			days := 5
			if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: item.mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &ready, RetentionDays: &days, SourceExpiryAt: &due, Status: "RETAINED", CreatedAt: ready, UpdatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.VideoFrame{ID: item.frameID, TenantID: fixture.TenantID, SourceMediaID: item.mediaID, PreparationID: item.prepID, PTSMicros: 0, SHA256: digest, ObjectKey: item.frameKey, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 15, CreatedAt: ready}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.VideoEvidenceReference{ID: identity.NewID(), TenantID: fixture.TenantID, PreparationID: item.prepID, OwnerType: "REPORT_SNAPSHOT", OwnerID: identity.NewID(), CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	for batch := 0; batch < 4; batch++ {
		claimed, err := executor.RunDue(ctx, fixture.TenantID, now, 25)
		if err != nil || claimed != 25 {
			t.Fatalf("batch %d claimed=%d err=%v; want 25", batch+1, claimed, err)
		}
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		for _, item := range items {
			var source database.VideoSource
			if err := tx.Where("tenant_id=? AND media_id=?", fixture.TenantID, item.mediaID).Take(&source).Error; err != nil {
				return err
			}
			if source.Status != "EXPIRED" || source.OriginalDeletedAt == nil {
				t.Errorf("source %s status=%s deletedAt=%v", item.mediaID, source.Status, source.OriginalDeletedAt)
			}
			var frames int64
			if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND id=?", fixture.TenantID, item.frameID).Count(&frames).Error; err != nil {
				return err
			}
			if frames != 1 {
				t.Errorf("protected frame %s row count=%d", item.frameID, frames)
			}
			reader, _, err := (objectstore.VideoStore{Store: h.Store}).Open(ctx, item.frameKey)
			if err != nil {
				t.Errorf("protected frame %s bytes unavailable: %v", item.frameID, err)
				continue
			}
			if err := reader.Close(); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
