//go:build integration

package prepare_video

import (
	"context"
	"os"
	"testing"
	"time"

	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

type gatedExtractionDecoder struct {
	inner   video.Decoder
	entered chan struct{}
	release chan struct{}
	calls   int
}

func (d *gatedExtractionDecoder) Probe(ctx context.Context, path string, limits video.Limits) (video.Probe, error) {
	return d.inner.Probe(ctx, path, limits)
}

func (d *gatedExtractionDecoder) Decode(ctx context.Context, path string, limits video.Limits, yield func(video.Frame) error) error {
	d.calls++
	if d.calls == 2 {
		close(d.entered)
		select {
		case <-d.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return d.inner.Decode(ctx, path, limits, yield)
}

func (d *gatedExtractionDecoder) Playback(ctx context.Context, source, target string, limits video.Limits) error {
	return d.inner.Playback(ctx, source, target, limits)
}

// TestVV_IT_147PreparationExecutorTransitionsReceivedExtractingReady checks
// the durable state at the real decoder's extraction checkpoint and on completion.
func TestVV_IT_147PreparationExecutorTransitionsReceivedExtractingReady(t *testing.T) {
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
	fixture := seedPreparationFixture(t, h, syntheticH264Clip(t), "video/mp4", "CURRENT", "CAMERA")
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	var initial database.VideoPreparationJob
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&initial).Error
	}); err != nil {
		t.Fatal(err)
	}
	if initial.Status != "QUEUED" || initial.Phase != "RECEIVED" {
		t.Fatalf("initial preparation=%s/%s, want QUEUED/RECEIVED", initial.Status, initial.Phase)
	}
	decoder := &gatedExtractionDecoder{inner: video.FFmpeg{Threads: 1}, entered: make(chan struct{}), release: make(chan struct{})}
	executor := checkpointExecutor(t, h, decoder, time.Now)
	type runResult struct {
		handled bool
		err     error
	}
	var done chan runResult
	reachedExtraction := false
	timeout := time.NewTimer(20 * time.Second)
	defer timeout.Stop()
	for attempt := 1; attempt <= 32; attempt++ {
		decoder.entered = make(chan struct{})
		decoder.release = make(chan struct{})
		decoder.calls = 0
		done = make(chan runResult, 1)
		go func(result chan<- runResult) {
			handled, runErr := executor.RunDue(ctx)
			result <- runResult{handled: handled, err: runErr}
		}(done)
		select {
		case <-decoder.entered:
			var current database.VideoPreparationJob
			if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
				return tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&current).Error
			}); err != nil {
				t.Fatal(err)
			}
			if current.Status == "PROCESSING" && current.Phase == "EXTRACTING" {
				reachedExtraction = true
				break
			}
			close(decoder.release)
			result := <-done
			if result.err != nil || !result.handled {
				t.Fatalf("RunDue handling unrelated job: handled=%t err=%v", result.handled, result.err)
			}
		case result := <-done:
			if result.err != nil || !result.handled {
				t.Fatalf("RunDue returned before reaching fixture extraction: handled=%t err=%v", result.handled, result.err)
			}
		case <-timeout.C:
			t.Fatal("preparation never reached extraction")
		}
		if reachedExtraction {
			break
		}
	}
	if !reachedExtraction {
		t.Fatal("RunDue did not reach the fixture preparation after 32 jobs")
	}
	var extracting database.VideoPreparationJob
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&extracting).Error
	}); err != nil {
		t.Fatal(err)
	}
	if extracting.Status != "PROCESSING" || extracting.Phase != "EXTRACTING" {
		t.Fatalf("paused preparation=%s/%s, want PROCESSING/EXTRACTING", extracting.Status, extracting.Phase)
	}
	close(decoder.release)
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("complete preparation: %v", result.err)
		}
		if !result.handled {
			t.Fatal("RunDue completed without handling the preparation")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("preparation did not finish")
	}
	var ready database.VideoPreparationJob
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&ready).Error
	}); err != nil {
		t.Fatal(err)
	}
	if ready.Status != "READY" || ready.Phase != "READY" {
		t.Fatalf("completed preparation=%s/%s, want READY/READY", ready.Status, ready.Phase)
	}
}
