//go:build integration

package prepare_video

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

type failFirstHeadStore struct {
	objectstore.Client
	failed bool
}

func (s *failFirstHeadStore) Head(ctx context.Context, bucket, key string) (objectstore.Head, error) {
	if !s.failed {
		s.failed = true
		return objectstore.Head{}, fmt.Errorf("temporary QA object store failure")
	}
	return s.Client.Head(ctx, bucket, key)
}

func (s *failFirstHeadStore) Delete(ctx context.Context, bucket, key string) error {
	return s.Client.(objectstore.Deleter).Delete(ctx, bucket, key)
}

func TestTransientStoreFailureRetriesRetainedOriginalIT148(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture := seedPreparationFixture(t, h, syntheticH264Clip(t), "video/mp4", "CURRENT", "CAMERA")
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	executor := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return fixture.now })
	executor.deps.Store.Store.Client = &failFirstHeadStore{Client: h.Store.Client}
	if worked, err := executor.RunDue(ctx); err != nil || !worked {
		t.Fatalf("first failed attempt: worked=%t err=%v", worked, err)
	}
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		if job.Status != "QUEUED" || job.ErrorCode != "PREPARATION_FAILED" || job.Attempts != 1 || job.NextAttemptAt.Before(fixture.now.Add(10*time.Second)) || job.ManifestDigest != "" {
			t.Errorf("transient store failure was not queued safely: status=%s code=%s attempts=%d next=%v digest=%q", job.Status, job.ErrorCode, job.Attempts, job.NextAttemptAt, job.ManifestDigest)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	retry := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return fixture.now.Add(time.Minute) })
	if worked, err := retry.RunDue(ctx); err != nil || !worked {
		t.Fatalf("retry retained original: worked=%t err=%v", worked, err)
	}
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		if job.Status != "READY" || job.Attempts != 2 || job.ManifestDigest == "" {
			t.Errorf("retry did not prepare retained original: status=%s attempts=%d digest=%q code=%s", job.Status, job.Attempts, job.ManifestDigest, job.ErrorCode)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestVV_IT_172FrameBankStorePutFailureRemainsRetryable(t *testing.T) {
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
	store := &failFramePutStoreIT172{Client: h.Store.Client}
	executor := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return fixture.now })
	executor.deps.Store.Store.Client = store
	if worked, err := executor.RunDue(ctx); err != nil || !worked {
		t.Fatalf("failed upload attempt: worked=%t err=%v", worked, err)
	}
	if !store.failed {
		t.Fatal("test did not inject failure in derived frame Store.Put")
	}
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		if job.Status != "QUEUED" || job.Phase == "FRAME_BANK_READY" || job.Phase == "READY" || job.ManifestDigest != "" || job.LeaseToken != nil {
			t.Errorf("failed frame upload advanced readiness: status=%s phase=%s digest=%q lease=%v", job.Status, job.Phase, job.ManifestDigest, job.LeaseToken)
		}
		var frames int64
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Count(&frames).Error; err != nil {
			return err
		}
		if frames != 0 {
			t.Errorf("failed frame upload persisted %d visible frames", frames)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	retry := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return fixture.now.Add(time.Minute) })
	if worked, err := retry.RunDue(ctx); err != nil || !worked {
		t.Fatalf("retry after frame upload failure: worked=%t err=%v", worked, err)
	}
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		if job.Status != "READY" || job.Phase != "READY" || job.ManifestDigest == "" || job.Attempts < 2 {
			t.Errorf("retry did not complete after transient Store.Put failure: status=%s phase=%s digest=%q attempts=%d", job.Status, job.Phase, job.ManifestDigest, job.Attempts)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

type failFramePutStoreIT172 struct {
	objectstore.Client
	failed bool
}

func (s *failFramePutStoreIT172) Put(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
	if !s.failed && len(key) >= len("video-frames/") && key[:len("video-frames/")] == "video-frames/" {
		s.failed = true
		return errors.New("injected F02 derived analysis object upload failure")
	}
	return s.Client.Put(ctx, bucket, key, body, size, contentType)
}

func (s *failFramePutStoreIT172) Delete(ctx context.Context, bucket, key string) error {
	return s.Client.(objectstore.Deleter).Delete(ctx, bucket, key)
}

type pauseAfterTwentyFrames struct {
	inner   video.Decoder
	entered chan struct{}
	calls   int
}

type pauseDuringDecode struct {
	inner   video.Decoder
	entered chan struct{}
	calls   int
}

func (d *pauseDuringDecode) Probe(ctx context.Context, path string, limits video.Limits) (video.Probe, error) {
	return d.inner.Probe(ctx, path, limits)
}

func (d *pauseDuringDecode) Decode(ctx context.Context, path string, limits video.Limits, yield func(video.Frame) error) error {
	d.calls++
	seen := 0
	return d.inner.Decode(ctx, path, limits, func(frame video.Frame) error {
		seen++
		if d.calls == 1 && seen == 10 {
			close(d.entered)
			<-ctx.Done()
			return ctx.Err()
		}
		return yield(frame)
	})
}

func (d *pauseDuringDecode) Playback(ctx context.Context, source, target string, limits video.Limits) error {
	return d.inner.Playback(ctx, source, target, limits)
}

func TestCancelledDecodeCleansTempAndRemainsRecoverableIT597(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	path := filepath.Join(t.TempDir(), "thirty-fps.mp4")
	command := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=30:duration=1", "-threads", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate decode clip: %v: %s", err, output)
	}
	clip, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := seedPreparationFixture(t, h, clip, "video/mp4", "CURRENT", "CAMERA")
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	decoder := &pauseDuringDecode{inner: video.FFmpeg{Threads: 1}, entered: make(chan struct{})}
	executor := checkpointExecutor(t, h, decoder, func() time.Time { return fixture.now })
	scratch := t.TempDir()
	executor.deps.TempDir = scratch
	done := make(chan error, 1)
	go func() { _, err := executor.RunDue(ctx); done <- err }()
	select {
	case <-decoder.entered:
	case <-time.After(20 * time.Second):
		t.Fatal("decoder did not reach the tenth frame")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled decoder: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled decoder subprocess did not stop")
	}
	entries, err := os.ReadDir(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("decoder left %d temporary files", len(entries))
	}
	if err := h.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		if job.Status != "QUEUED" || job.ManifestDigest != "" || job.LeaseToken != nil {
			t.Errorf("cancelled decode was not recoverable: status=%s digest=%q token=%v", job.Status, job.ManifestDigest, job.LeaseToken)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	restart := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return fixture.now.Add(time.Minute) })
	restart.deps.TempDir = scratch
	if worked, err := restart.RunDue(context.Background()); err != nil || !worked {
		t.Fatalf("restart after decode cancellation: worked=%t err=%v", worked, err)
	}
	if err := h.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		if job.Status != "READY" || job.ManifestDigest == "" || job.Attempts != 2 {
			t.Errorf("restart did not complete: status=%s digest=%q attempts=%d code=%s", job.Status, job.ManifestDigest, job.Attempts, job.ErrorCode)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionDeadlineRecordsTimeoutWithoutReadinessIT154(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture := seedPreparationFixture(t, h, syntheticH264Clip(t), "video/mp4", "CURRENT", "CAMERA")
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	decoder := &pauseAtExtractionDecoder{inner: video.FFmpeg{Threads: 1}, entered: make(chan struct{})}
	executor := checkpointExecutor(t, h, decoder, func() time.Time { return fixture.now })
	executor.deps.Limits.StepTimeout = 800 * time.Millisecond
	if worked, err := executor.RunDue(ctx); err != nil || !worked {
		t.Fatalf("deadline run: worked=%t err=%v", worked, err)
	}
	select {
	case <-decoder.entered:
	default:
		t.Fatal("deadline happened before the decoder entered extraction")
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
		if job.Status != "QUEUED" || job.ErrorCode != "TIMEOUT" || job.ManifestDigest != "" || source.FirstFrameReadyAt != nil {
			t.Errorf("deadline fabricated readiness: status=%s code=%s digest=%q anchor=%v", job.Status, job.ErrorCode, job.ManifestDigest, source.FirstFrameReadyAt)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func (d *pauseAfterTwentyFrames) Probe(ctx context.Context, path string, limits video.Limits) (video.Probe, error) {
	return d.inner.Probe(ctx, path, limits)
}

func (d *pauseAfterTwentyFrames) Decode(ctx context.Context, path string, limits video.Limits, yield func(video.Frame) error) error {
	d.calls++
	seen := 0
	return d.inner.Decode(ctx, path, limits, func(frame video.Frame) error {
		if d.calls == 2 && seen == 21 {
			close(d.entered)
			<-ctx.Done()
			return ctx.Err()
		}
		seen++
		return yield(frame)
	})
}

func (d *pauseAfterTwentyFrames) Playback(ctx context.Context, source, target string, limits video.Limits) error {
	return d.inner.Playback(ctx, source, target, limits)
}

func TestRestartAfterTwentyPersistedCandidatesIT054(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	path := filepath.Join(t.TempDir(), "twenty-two-seconds.mp4")
	command := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=1:duration=22", "-threads", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate long clip: %v: %s", err, output)
	}
	clip, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := seedPreparationFixture(t, h, clip, "video/mp4", "CURRENT", "CAMERA")
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	decoder := &pauseAfterTwentyFrames{inner: video.FFmpeg{Threads: 1}, entered: make(chan struct{})}
	executor := checkpointExecutor(t, h, decoder, func() time.Time { return fixture.now })
	done := make(chan error, 1)
	go func() { _, err := executor.RunDue(ctx); done <- err }()
	select {
	case <-decoder.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("extraction did not persist twenty candidates")
	}
	if err := h.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		var frames, scopes int64
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Count(&frames).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Count(&scopes).Error; err != nil {
			return err
		}
		var source database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=?", fixture.tenantID, fixture.mediaID).Take(&source).Error; err != nil {
			return err
		}
		if frames != 20 || scopes != 0 || source.FirstFrameReadyAt != nil {
			t.Errorf("partial checkpoint published readiness: frames=%d scopes=%d anchor=%v", frames, scopes, source.FirstFrameReadyAt)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled worker: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled worker did not stop")
	}
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	restart := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return fixture.now.Add(time.Minute) })
	if worked, err := restart.RunDue(context.Background()); err != nil || !worked {
		t.Fatalf("restart preparation: worked=%t err=%v", worked, err)
	}
	if err := h.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		var frames, scopes int64
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Count(&frames).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Count(&scopes).Error; err != nil {
			return err
		}
		if job.Status != "READY" || frames != 22 || scopes == 0 || job.ManifestDigest == "" || job.Attempts != 2 {
			t.Errorf("restart did not seal durable bank: status=%s frames=%d scopes=%d digest=%q attempts=%d code=%s", job.Status, frames, scopes, job.ManifestDigest, job.Attempts, job.ErrorCode)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredLeaseIsReclaimedBeforeReadinessIT157(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture := seedPreparationFixture(t, h, syntheticH264Clip(t), "video/mp4", "CURRENT", "CAMERA")
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	staleToken := identity.NewID()
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Updates(map[string]any{
			"status": "PROCESSING", "phase": "RECEIVED", "attempts": 1,
			"lease_token": staleToken, "lease_expires_at": fixture.now.Add(-time.Second),
		}).Error
	}); err != nil {
		t.Fatal(err)
	}
	executor := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return fixture.now })
	if worked, err := executor.RunDue(ctx); err != nil || !worked {
		t.Fatalf("reclaim expired lease: worked=%t err=%v", worked, err)
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
		if job.Status != "READY" || job.Attempts != 2 || job.LeaseToken != nil || job.ManifestDigest == "" || source.FirstFrameReadyAt == nil {
			t.Errorf("reclaimed job did not complete once: status=%s attempts=%d token=%v digest=%q anchor=%v", job.Status, job.Attempts, job.LeaseToken, job.ManifestDigest, source.FirstFrameReadyAt)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRestartDeletesRecordedOrphanButKeepsCommittedFrameIT593(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture := seedPreparationFixture(t, h, syntheticH264Clip(t), "video/mp4", "CURRENT", "CAMERA")
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	orphanKey := "video-frames/" + fixture.tenantID.String() + "/orphan-" + identity.NewID().String() + ".jpg"
	committedKey := "video-frames/" + fixture.tenantID.String() + "/committed-" + identity.NewID().String() + ".jpg"
	for _, key := range []string{orphanKey, committedKey} {
		if err := h.Store.Client.Put(ctx, h.Store.Bucket, key, bytes.NewReader([]byte("fixture")), 7, "image/jpeg"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Store.Client.(objectstore.Deleter).Delete(context.Background(), h.Store.Bucket, key) })
	}
	pending, _ := json.Marshal([]string{orphanKey, committedKey})
	staleToken := identity.NewID()
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.VideoFrame{ID: identity.NewID(), TenantID: fixture.tenantID, SourceMediaID: fixture.mediaID, PreparationID: fixture.preparationID, PTSMicros: 0, SHA256: "committed-digest", ObjectKey: committedKey, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 7, CreatedAt: fixture.now}).Error; err != nil {
			return err
		}
		return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Updates(map[string]any{
			"status": "PROCESSING", "attempts": 1, "lease_token": staleToken,
			"lease_expires_at": fixture.now.Add(-time.Second), "pending_object_keys": pending,
		}).Error
	}); err != nil {
		t.Fatal(err)
	}
	executor := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return fixture.now })
	r, claimed, err := executor.reserve(ctx)
	if err != nil || !claimed || r == nil || r.token == staleToken {
		t.Fatalf("restart did not claim new lease: claimed=%t reservation=%v err=%v", claimed, r, err)
	}
	if err := executor.reconcileOrphans(ctx, *r); err != nil {
		t.Fatal(err)
	}
	store := objectstore.VideoStore{Store: h.Store}
	if reader, _, err := store.Open(ctx, orphanKey); reader != nil || !errors.Is(err, objectstore.ErrNotFound) {
		if reader != nil {
			_ = reader.Close()
		}
		t.Fatalf("orphan still exists: %v", err)
	}
	reader, _, err := store.Open(ctx, committedKey)
	if err != nil {
		t.Fatalf("committed frame was removed: %v", err)
	}
	_ = reader.Close()
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		if job.Status != "PROCESSING" || string(job.PendingObjectKeys) != "[]" || job.ManifestDigest != "" {
			t.Errorf("reconciliation changed readiness or did not clear pending keys: status=%s keys=%s digest=%q", job.Status, job.PendingObjectKeys, job.ManifestDigest)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
