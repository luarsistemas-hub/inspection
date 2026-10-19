//go:build integration

package prepare_video

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	expirevideo "inspection/services/inspection/internal/features/retention/expire_video_sources"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

func TestWebMPlaybackProxySharesOriginalLineageAndExpiryIT596(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	path := filepath.Join(t.TempDir(), "source.webm")
	command := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=5:duration=1", "-threads", "1", "-c:v", "libvpx-vp9", "-b:v", "300k", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate VP9 source: %v: %s", err, output)
	}
	clip, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := seedPreparationFixture(t, h, clip, "video/webm", "CURRENT", "FILE")
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	executor := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return fixture.now })
	if worked, err := executor.RunDue(ctx); err != nil || !worked {
		t.Fatalf("RunDue = %t, %v", worked, err)
	}
	var source database.VideoSource
	var derivative database.MediaDerivative
	var original database.MediaObject
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND media_id=?", fixture.tenantID, fixture.mediaID).Take(&source).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.mediaID).Take(&original).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND media_id=? AND kind='VIDEO_PLAYBACK'", fixture.tenantID, fixture.mediaID).Take(&derivative).Error
	}); err != nil {
		var job database.VideoPreparationJob
		_ = h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error
		})
		t.Fatalf("proxy persistence: %v; job=%s/%s code=%s", err, job.Status, job.Phase, job.ErrorCode)
	}
	if source.Codec != "vp9" || source.SourceExpiryAt == nil || derivative.MediaID != original.ID || derivative.ContentType != "video/mp4" || !strings.HasSuffix(derivative.Profile, original.SHA256) || derivative.SHA256 == "" || derivative.SizeBytes <= 0 {
		t.Fatalf("proxy lineage incomplete: codec=%s expiry=%v media=%s profile=%s", source.Codec, source.SourceExpiryAt, derivative.MediaID, derivative.Profile)
	}
	if _, err := h.Store.Client.Head(ctx, h.Store.Bucket, derivative.ObjectKey); err != nil {
		t.Fatalf("proxy object missing before expiry: %v", err)
	}
	expire, err := expirevideo.Setup(expirevideo.Dependencies{DB: h.DB, Store: h.Store, Now: func() time.Time { return source.SourceExpiryAt.Add(time.Second) }})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := expire.RunDue(ctx, fixture.tenantID, source.SourceExpiryAt.Add(time.Second), 1)
	if err != nil || claimed != 1 {
		t.Fatalf("expire source/proxy: claimed=%d err=%v", claimed, err)
	}
	for _, key := range []string{original.ObjectKey, derivative.ObjectKey} {
		reader, _, err := (objectstore.VideoStore{Store: h.Store}).Open(ctx, key)
		if reader != nil {
			_ = reader.Close()
		}
		if !errors.Is(err, objectstore.ErrNotFound) {
			t.Errorf("expired original/proxy remains at %s: %v", key, err)
		}
	}
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var retained int64
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND source_media_id=?", fixture.tenantID, fixture.mediaID).Count(&retained).Error; err != nil {
			return err
		}
		if retained == 0 {
			t.Error("source expiry removed retained frames")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
