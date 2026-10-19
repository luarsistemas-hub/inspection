//go:build integration

package prepare_video

import (
	"context"
	"image"
	"image/color"
	"os"
	"testing"
	"time"

	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

// syntheticSecondsDecoder screens the real clip, then yields one candidate per
// presentation second during extraction.
type syntheticSecondsDecoder struct {
	inner   video.Decoder
	calls   int
	seconds int64
	// probeDuration, when set, reports the synthetic stream length.
	probeDuration time.Duration
}

func (d *syntheticSecondsDecoder) Probe(ctx context.Context, path string, limits video.Limits) (video.Probe, error) {
	probe, err := d.inner.Probe(ctx, path, limits)
	if err == nil && d.probeDuration > 0 {
		probe.Duration = d.probeDuration
	}
	return probe, err
}

func (d *syntheticSecondsDecoder) Decode(ctx context.Context, path string, limits video.Limits, yield func(video.Frame) error) error {
	d.calls++
	if d.calls == 1 {
		return d.inner.Decode(ctx, path, limits, yield)
	}
	pixels := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for x := 0; x < 16; x++ {
		for y := 0; y < 16; y++ {
			pixels.Set(x, y, color.RGBA{R: uint8(x * 16), G: uint8(y * 16), B: 128, A: 255})
		}
	}
	for second := int64(0); second < d.seconds; second++ {
		if err := yield(video.Frame{PTSMicros: second * 1_000_000, Width: 16, Height: 16, Pixels: pixels}); err != nil {
			return err
		}
	}
	return nil
}

func (d *syntheticSecondsDecoder) Playback(ctx context.Context, source, target string, limits video.Limits) error {
	return d.inner.Playback(ctx, source, target, limits)
}

func TestVV_IT_169SelectionAboveHardFrameCapFailsResourceLimitWithoutCoverage(t *testing.T) {
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
	deferUnrelatedPreparationJobs(t, h, fixture, time.Now().UTC().Add(time.Hour))
	t.Cleanup(func() {
		var keys []string
		_ = h.AdminDB.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Pluck("object_key", &keys).Error
		for _, key := range keys {
			_ = h.Store.Delete(context.Background(), key)
		}
	})
	decoder := &syntheticSecondsDecoder{inner: video.FFmpeg{Threads: 1}, seconds: maxRetainedFrames + 1}
	executor := checkpointExecutor(t, h, decoder, time.Now)
	executor.deps.DerivativeBankBytes = 1 << 40
	executor.deps.Limits.StepTimeout = 15 * time.Minute
	worked, err := executor.RunDue(ctx)
	if err != nil || !worked {
		t.Fatalf("RunDue = %t, %v", worked, err)
	}
	if decoder.calls < 2 {
		t.Fatalf("preparation never reached extraction: decode calls=%d", decoder.calls)
	}
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		var source database.VideoSource
		sourceErr := tx.Where("tenant_id=? AND media_id=?", fixture.tenantID, fixture.mediaID).Take(&source).Error
		var scope, ready int64
		if err := tx.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Count(&scope).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type=? AND payload::text LIKE ?", fixture.tenantID, "media.video_prepared.v1", "%"+fixture.preparationID.String()+"%").Count(&ready).Error; err != nil {
			return err
		}
		if job.Status != "FAILED" || job.ErrorCode != "RESOURCE_LIMIT" || job.ManifestDigest != "" || job.FirstReadyAt != nil || scope != 0 || ready != 0 || sourceErr == nil && source.FirstFrameReadyAt != nil {
			t.Errorf("over-cap selection claimed coverage: status=%s code=%s digest=%q firstReady=%v scope=%d readyEvents=%d sourceReady=%v", job.Status, job.ErrorCode, job.ManifestDigest, job.FirstReadyAt, scope, ready, source.FirstFrameReadyAt)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestVV_IT_004And622PreparesNineHundredBaselineFramesWithoutBillingExtraUploads(t *testing.T) {
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
	deferUnrelatedPreparationJobs(t, h, fixture, time.Now().UTC().Add(time.Hour))
	t.Cleanup(func() {
		var keys []string
		_ = h.AdminDB.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Pluck("object_key", &keys).Error
		for _, key := range keys {
			_ = h.Store.Delete(context.Background(), key)
		}
	})
	var mediaBefore int64
	if err := h.AdminDB.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&mediaBefore).Error; err != nil {
		t.Fatal(err)
	}
	executor := checkpointExecutor(t, h, &syntheticSecondsDecoder{inner: video.FFmpeg{Threads: 1}, seconds: 900, probeDuration: 900 * time.Second}, time.Now)
	executor.deps.Limits.StepTimeout = 10 * time.Minute
	if worked, err := executor.RunDue(ctx); err != nil || !worked {
		t.Fatalf("RunDue = %t, %v", worked, err)
	}
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		var baseline, distinctSeconds, media, videos int64
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=? AND role='BASELINE'", fixture.tenantID, fixture.preparationID).Count(&baseline).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Distinct("pts_us / 1000000").Count(&distinctSeconds).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&media).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=? AND media_kind='VIDEO'", fixture.tenantID, fixture.responsibilityID).Count(&videos).Error; err != nil {
			return err
		}
		if job.Status != "READY" || baseline != 900 || distinctSeconds != 900 || media != mediaBefore || videos != 1 {
			t.Errorf("900-second preparation: status=%s code=%s baseline=%d seconds=%d participantMedia=%d->%d videos=%d; want READY, 900, 900, unchanged media, one original video", job.Status, job.ErrorCode, baseline, distinctSeconds, mediaBefore, media, videos)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
