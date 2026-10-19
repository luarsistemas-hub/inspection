//go:build integration

package prepare_video

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

func TestFrameBankCommitFreezesOriginAndCurrentExpiryAtFixedT0IT308(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	clip := syntheticH264Clip(t)
	origin := seedPreparationFixture(t, h, clip, "video/mp4", "ORIGIN", "CAMERA")
	current := seedPreparationFixture(t, h, clip, "video/mp4", "CURRENT", "CAMERA")
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for _, fixture := range []preparationFixture{origin, current} {
		if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Update("next_attempt_at", t0).Error
		}); err != nil {
			t.Fatal(err)
		}
	}
	executor := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return t0 })
	for i := 0; i < 2; i++ {
		if worked, err := executor.RunDue(ctx); err != nil || !worked {
			t.Fatalf("RunDue(%d) = %t, %v", i, worked, err)
		}
	}
	wantByMedia := map[string]time.Time{
		origin.mediaID.String():  t0.Add(30 * 24 * time.Hour),
		current.mediaID.String(): t0.Add(5 * 24 * time.Hour),
	}
	for _, fixture := range []preparationFixture{origin, current} {
		if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
			var source database.VideoSource
			if err := tx.Where("tenant_id=? AND media_id=?", fixture.tenantID, fixture.mediaID).Take(&source).Error; err != nil {
				return err
			}
			want := wantByMedia[fixture.mediaID.String()]
			if source.FirstFrameReadyAt == nil || !source.FirstFrameReadyAt.Equal(t0) || source.SourceExpiryAt == nil || !source.SourceExpiryAt.Equal(want) {
				t.Errorf("source %s readiness=%v expiry=%v; want T0=%s expiry=%s", fixture.mediaID, source.FirstFrameReadyAt, source.SourceExpiryAt, t0, want)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

type failFramePutStore struct {
	objectstore.Client
	failed bool
}

func (s *failFramePutStore) Put(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
	if !s.failed && len(key) >= len("video-frames/") && key[:len("video-frames/")] == "video-frames/" {
		s.failed = true
		return errors.New("injected frame store failure")
	}
	return s.Client.Put(ctx, bucket, key, body, size, contentType)
}

func (s *failFramePutStore) Delete(ctx context.Context, bucket, key string) error {
	return s.Client.(objectstore.Deleter).Delete(ctx, bucket, key)
}

func TestExtractionFailureBeforeFirstReadinessKeepsExpiryNullAndCleanupVisibleIT309(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture := seedPreparationFixture(t, h, syntheticH264Clip(t), "video/mp4", "ORIGIN", "CAMERA")
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	executor := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return fixture.now })
	executor.deps.Store.Store.Client = &failFramePutStore{Client: h.Store.Client}
	if worked, err := executor.RunDue(ctx); err != nil || !worked {
		t.Fatalf("RunDue = %t, %v", worked, err)
	}
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		var source database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=?", fixture.tenantID, fixture.mediaID).Take(&source).Error; err != nil {
			return err
		}
		var pending []string
		if err := json.Unmarshal(job.PendingObjectKeys, &pending); err != nil {
			return err
		}
		if job.Status != "QUEUED" || job.Phase != "EXTRACTING" || job.ErrorCode != "PREPARATION_FAILED" || len(pending) == 0 {
			t.Errorf("failed extraction cleanup state not visible: status=%s phase=%s code=%s pending=%v", job.Status, job.Phase, job.ErrorCode, pending)
		}
		if source.FirstFrameReadyAt != nil || source.SourceExpiryAt != nil {
			t.Errorf("failed extraction invented readiness/expiry: first=%v expiry=%v", source.FirstFrameReadyAt, source.SourceExpiryAt)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
