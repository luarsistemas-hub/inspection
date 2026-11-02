//go:build integration

package prepare_video

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

type gatedExtractionDecoder struct {
	inner   video.Decoder
	entered chan struct{}
	release chan struct{}
	phase   func() bool
	gate    sync.Once
	open    sync.Once
}

func (d *gatedExtractionDecoder) Probe(ctx context.Context, path string, limits video.Limits) (video.Probe, error) {
	return d.inner.Probe(ctx, path, limits)
}

func (d *gatedExtractionDecoder) Decode(ctx context.Context, path string, limits video.Limits, yield func(video.Frame) error) error {
	if d.phase != nil && d.phase() {
		d.gate.Do(func() { close(d.entered) })
		select {
		case <-d.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return d.inner.Decode(ctx, path, limits, yield)
}

func (d *gatedExtractionDecoder) releaseGate() { d.open.Do(func() { close(d.release) }) }

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
	decoder.phase = func() bool {
		var job database.VideoPreparationJob
		return h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error == nil && job.Status == "PROCESSING" && job.Phase == "EXTRACTING"
	}
	t.Cleanup(decoder.releaseGate)
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
		decoder.gate = sync.Once{}
		decoder.open = sync.Once{}
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
			decoder.releaseGate()
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
	decoder.releaseGate()
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

type providerCredentialFailureGateway struct{}

func (providerCredentialFailureGateway) CompleteStructured(context.Context, llm.StructuredRequest) (llm.StructuredResult, error) {
	return llm.StructuredResult{}, llm.NewError(llm.CodeAuthentication, 401, errors.New("qa-secret-123 authentication failed"))
}

// TestVV_IT_149PreparationExecutorMapsProviderCredentialFailureSafely injects
// authentication failure at the actual semantic gateway used by RunDue.
func TestVV_IT_149PreparationExecutorMapsProviderCredentialFailureSafely(t *testing.T) {
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
	executor := checkpointExecutorWithGateway(t, h, video.FFmpeg{Threads: 1}, time.Now, providerCredentialFailureGateway{})
	deadline := time.After(45 * time.Second)
	for {
		var job database.VideoPreparationJob
		if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error
		}); err != nil {
			t.Fatal(err)
		}
		if job.Status == "FAILED" {
			if job.ErrorCode != "PROVIDER_CONFIGURATION" {
				t.Fatalf("provider credential failure code=%q, want PROVIDER_CONFIGURATION", job.ErrorCode)
			}
			var jobText, outboxText string
			if err := h.AdminDB.Raw("SELECT row_to_json(j)::text FROM media.video_preparation_jobs j WHERE tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Scan(&jobText).Error; err != nil || jobText == "" {
				t.Fatalf("load preparation job text columns: %v", err)
			}
			if err := h.AdminDB.Raw("SELECT coalesce(string_agg(row_to_json(o)::text, E'\\n'), '') FROM messaging.outbox o WHERE tenant_id=?", fixture.tenantID).Scan(&outboxText).Error; err != nil {
				t.Fatalf("load tenant outbox: %v", err)
			}
			for _, secret := range []string{"qa-secret-123", "authentication failed"} {
				if strings.Contains(jobText, secret) || strings.Contains(outboxText, secret) {
					t.Fatalf("provider credential detail %q persisted: job=%s outbox=%s", secret, jobText, outboxText)
				}
			}
			return
		}
		handled, runErr := executor.RunDue(ctx)
		if runErr != nil {
			t.Fatalf("run preparation executor: %v", runErr)
		}
		if !handled {
			var pending database.VideoPreparationJob
			if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&pending).Error; err != nil {
				t.Fatalf("RunDue returned handled=false for preparation %s; load status/error_code: %v", fixture.preparationID, err)
			}
			t.Fatalf("RunDue returned handled=false for preparation %s (status=%s error_code=%s)", fixture.preparationID, pending.Status, pending.ErrorCode)
		}
		select {
		case <-deadline:
			t.Fatalf("preparation did not reach provider gateway; last status=%s phase=%s", job.Status, job.Phase)
		default:
		}
	}
}
