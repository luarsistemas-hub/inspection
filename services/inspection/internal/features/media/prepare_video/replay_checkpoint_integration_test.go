//go:build integration

package prepare_video

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/sensitivecontent"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

type pauseAtExtractionDecoder struct {
	inner   video.Decoder
	entered chan struct{}
	calls   int
}

func (d *pauseAtExtractionDecoder) Probe(ctx context.Context, path string, limits video.Limits) (video.Probe, error) {
	return d.inner.Probe(ctx, path, limits)
}

func (d *pauseAtExtractionDecoder) Decode(ctx context.Context, path string, limits video.Limits, yield func(video.Frame) error) error {
	d.calls++
	if d.calls == 2 {
		close(d.entered)
		<-ctx.Done()
		return ctx.Err()
	}
	return d.inner.Decode(ctx, path, limits, yield)
}

func (d *pauseAtExtractionDecoder) Playback(ctx context.Context, source, target string, limits video.Limits) error {
	return d.inner.Playback(ctx, source, target, limits)
}

func successfulScopeGateway() llm.Gateway {
	return scopeGatewayFunc(func(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
		observations := make([]video.ScopeObservation, 0, len(request.Images))
		for _, frame := range request.Images {
			observations = append(observations, video.ScopeObservation{FrameID: frame.EvidenceID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", Landmarks: []string{}})
		}
		payload, err := json.Marshal(map[string]any{"observations": observations, "unresolvedFrameIds": []string{}})
		return llm.StructuredResult{JSON: payload}, err
	})
}

func checkpointExecutor(t *testing.T, h *harness.Harness, decoder video.Decoder, now func() time.Time) *Executor {
	t.Helper()
	executor, err := Setup(Dependencies{DB: h.DB, ClaimDB: h.AdminDB, Store: objectstore.VideoStore{Store: h.Store}, Decoder: decoder, Detector: sensitivecontent.NewDisabledDetector(), Gateway: successfulScopeGateway(), Now: now, TempDir: os.TempDir(), Limits: video.WorkerConfig{Concurrency: 1, DecoderThreads: 1, MaxBytes: 1 << 30, ScratchBytes: 4 << 30, RequiredFreeBytes: 8 << 30, StepTimeout: time.Minute, LeaseDuration: 120 * time.Second, HeartbeatInterval: 30 * time.Second, PollInterval: 2 * time.Second, ShutdownTimeout: 30 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func TestExtractingDoesNotStartSourceExpiryIT150(t *testing.T) {
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
	decoder := &pauseAtExtractionDecoder{inner: video.FFmpeg{Threads: 1}, entered: make(chan struct{})}
	executor := checkpointExecutor(t, h, decoder, time.Now)
	done := make(chan error, 1)
	go func() { _, err := executor.RunDue(ctx); done <- err }()
	select {
	case <-decoder.entered:
	case <-time.After(20 * time.Second):
		t.Fatal("preparation never reached EXTRACTING")
	}
	if err := h.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		var source database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=?", fixture.tenantID, fixture.mediaID).Take(&source).Error; err != nil {
			return err
		}
		if job.Status != "PROCESSING" || job.Phase != "EXTRACTING" || source.FirstFrameReadyAt != nil || source.SourceExpiryAt != nil || source.RetentionDays != nil {
			t.Errorf("EXTRACTING incorrectly starts expiry: status=%s phase=%s first=%v expiry=%v days=%v", job.Status, job.Phase, source.FirstFrameReadyAt, source.SourceExpiryAt, source.RetentionDays)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled RunDue: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled decoder did not stop")
	}
	if err := h.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Update("status", "CANCELED").Error
	}); err != nil {
		t.Fatal(err)
	}
}

func TestVV_IT_246InterruptedSupplementExtractionKeepsRC1PendingAndResumesCheckpoint(t *testing.T) {
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
	fixture := seedPreparationFixture(t, h, syntheticH264Clip(t), "video/mp4", "SUPPLEMENT", "CAMERA")
	requestID := markPreparationAsSupplement(t, h, fixture)
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	decoder := &pauseAtExtractionDecoder{inner: video.FFmpeg{Threads: 1}, entered: make(chan struct{})}
	executor := checkpointExecutor(t, h, decoder, func() time.Time { return fixture.now })
	done := make(chan error, 1)
	go func() { _, err := executor.RunDue(ctx); done <- err }()
	select {
	case <-decoder.entered:
	case <-time.After(20 * time.Second):
		t.Fatal("supplement preparation never reached EXTRACTING")
	}
	if err := h.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		var request database.RecaptureRequest
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, requestID).Take(&request).Error; err != nil {
			return err
		}
		var frameCount int64
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Count(&frameCount).Error; err != nil {
			return err
		}
		if job.Status != "PROCESSING" || job.Phase != "EXTRACTING" || len(job.PendingObjectKeys) == 0 || request.Status != "REQUESTED" || frameCount != 0 {
			t.Errorf("interrupted supplement state: job=%s/%s pending=%d RC1=%s frames=%d", job.Status, job.Phase, len(job.PendingObjectKeys), request.Status, frameCount)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled supplement RunDue: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled supplement decoder did not stop")
	}
	// A worker restart can reclaim the interrupted attempt; preserve its
	// durable checkpoint while making it due again.
	replayAt := fixture.now.Add(time.Minute)
	if err := h.AdminDB.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Updates(map[string]any{"status": "QUEUED", "phase": "EXTRACTING", "next_attempt_at": replayAt, "lease_token": nil, "lease_expires_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
	deferUnrelatedPreparationJobs(t, h, fixture, replayAt.Add(48*time.Hour))
	if worked, err := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return replayAt }).RunDue(context.Background()); err != nil || !worked {
		t.Fatalf("supplement checkpoint replay = %t, %v", worked, err)
	}
	if err := h.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		var request database.RecaptureRequest
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, requestID).Take(&request).Error; err != nil {
			return err
		}
		var source database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=?", fixture.tenantID, fixture.mediaID).Take(&source).Error; err != nil {
			return err
		}
		var frames int64
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Count(&frames).Error; err != nil {
			return err
		}
		if job.Status != "READY" || frames == 0 || request.Status != "REQUESTED" || source.FirstFrameReadyAt == nil || !source.FirstFrameReadyAt.Equal(replayAt) || source.RetentionDays == nil || *source.RetentionDays != 30 || source.SourceExpiryAt == nil || !source.SourceExpiryAt.Equal(replayAt.Add(30*24*time.Hour)) {
			t.Errorf("resumed supplement state: job=%s/%s frames=%d RC1=%s first=%v retention=%v expiry=%v", job.Status, job.Phase, frames, request.Status, source.FirstFrameReadyAt, source.RetentionDays, source.SourceExpiryAt)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOriginCompletionReplayPreservesFirstAnchorIT055AndFrameManifestIT173(t *testing.T) {
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
	// RunDue polls all tenants; keep unrelated fixtures outside this replay window.
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	first := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return fixture.now })
	if worked, err := first.RunDue(ctx); err != nil || !worked {
		t.Fatalf("first RunDue = %t, %v", worked, err)
	}
	var firstDigest string
	var firstFrameIDs []string
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		firstDigest = job.ManifestDigest
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Order("id").Pluck("id", &firstFrameIDs).Error; err != nil {
			return err
		}
		return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Updates(map[string]any{"status": "QUEUED", "next_attempt_at": fixture.now.Add(24 * time.Hour)}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if len(firstFrameIDs) == 0 {
		t.Fatal("first preparation committed no frame IDs")
	}
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	replayAt := fixture.now.Add(24 * time.Hour)
	var targetDue, unrelatedDue int64
	if err := h.AdminDB.Model(&database.VideoPreparationJob{}).Where("id=? AND status='QUEUED' AND next_attempt_at<=?", fixture.preparationID, replayAt).Count(&targetDue).Error; err != nil {
		t.Fatal(err)
	}
	if targetDue != 1 {
		t.Fatalf("target replay was not due: id=%s due_count=%d", fixture.preparationID, targetDue)
	}
	if err := h.AdminDB.Model(&database.VideoPreparationJob{}).Where("id<>? AND ((status='QUEUED' AND next_attempt_at<=?) OR (status='PROCESSING' AND lease_expires_at<=?))", fixture.preparationID, replayAt, replayAt).Count(&unrelatedDue).Error; err != nil {
		t.Fatal(err)
	}
	if unrelatedDue != 0 {
		t.Fatalf("replay has %d unrelated due jobs", unrelatedDue)
	}
	replay := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return replayAt })
	if worked, err := replay.RunDue(ctx); err != nil || !worked {
		t.Fatalf("replay RunDue = %t, %v", worked, err)
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
		var scope, events int64
		var replayFrameIDs []string
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Order("id").Pluck("id", &replayFrameIDs).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Count(&scope).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='media.video_prepared.v1' AND correlation_id=?", fixture.tenantID, fixture.preparationID.String()).Count(&events).Error; err != nil {
			return err
		}
		if job.Status != "READY" || job.ManifestDigest != firstDigest || source.FirstFrameReadyAt == nil || !source.FirstFrameReadyAt.Equal(fixture.now) || source.SourceExpiryAt == nil || !source.SourceExpiryAt.Equal(fixture.now.Add(30*24*time.Hour)) || scope != 1 || events != 1 || len(replayFrameIDs) != len(firstFrameIDs) {
			t.Errorf("replay changed durable origin: status=%s phase=%s code=%s attempts=%d digest=%q first=%v expiry=%v scope=%d events=%d frameIDs=%v firstFrameIDs=%v", job.Status, job.Phase, job.ErrorCode, job.Attempts, job.ManifestDigest, source.FirstFrameReadyAt, source.SourceExpiryAt, scope, events, replayFrameIDs, firstFrameIDs)
		}
		for i := range firstFrameIDs {
			if i >= len(replayFrameIDs) || replayFrameIDs[i] != firstFrameIDs[i] {
				t.Errorf("replay changed visible frame ID at index %d: got=%v want=%v", i, replayFrameIDs, firstFrameIDs)
				break
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func deferUnrelatedPreparationJobs(t *testing.T, h *harness.Harness, fixture preparationFixture, until time.Time) {
	t.Helper()
	if err := h.AdminDB.Model(&database.VideoPreparationJob{}).Where("id<>? AND status='QUEUED'", fixture.preparationID).Update("next_attempt_at", until).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.VideoPreparationJob{}).Where("id<>? AND status='PROCESSING' AND lease_expires_at<=?", fixture.preparationID, until).Update("lease_expires_at", until).Error; err != nil {
		t.Fatal(err)
	}
}
