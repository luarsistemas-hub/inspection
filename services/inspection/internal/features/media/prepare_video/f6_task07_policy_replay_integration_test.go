//go:build integration

package prepare_video

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

func TestVV_IT_312ReplayAfterPolicyChangeKeepsFirstReadinessAndFrozenDeadline(t *testing.T) {
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
	t0 := fixture.now
	setPolicy := func(days int, version int64) {
		t.Helper()
		policy := database.VideoPolicy{TenantID: fixture.tenantID, MaxDurationMinutes: 15, OriginRetentionDays: 30, CurrentRetentionDays: days, Version: version, UpdatedAt: time.Now().UTC()}
		if err := h.AdminDB.Save(&policy).Error; err != nil {
			t.Fatal(err)
		}
	}
	requeue := func(at time.Time) {
		t.Helper()
		if err := h.AdminDB.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Updates(map[string]any{"status": "QUEUED", "next_attempt_at": at, "lease_token": nil, "lease_expires_at": nil}).Error; err != nil {
			t.Fatal(err)
		}
		deferUnrelatedPreparationJobs(t, h, fixture, at.Add(48*time.Hour))
	}
	assertFrozen := func(stage string) {
		t.Helper()
		if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
			var job database.VideoPreparationJob
			if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
				return err
			}
			var source database.VideoSource
			if err := tx.Where("tenant_id=? AND media_id=?", fixture.tenantID, fixture.mediaID).Take(&source).Error; err != nil {
				return err
			}
			want := t0.Add(5 * 24 * time.Hour)
			if source.FirstFrameReadyAt == nil || !source.FirstFrameReadyAt.Equal(t0) || source.RetentionDays == nil || *source.RetentionDays != 5 || source.SourceExpiryAt == nil || !source.SourceExpiryAt.Equal(want) || job.FirstReadyAt == nil || !job.FirstReadyAt.Equal(t0) {
				t.Errorf("%s changed the frozen clock: status=%s first=%v days=%v expiry=%v jobFirst=%v; want first=%v days=5 expiry=%v", stage, job.Status, source.FirstFrameReadyAt, source.RetentionDays, source.SourceExpiryAt, job.FirstReadyAt, t0, want)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	setPolicy(5, 1)
	deferUnrelatedPreparationJobs(t, h, fixture, t0.Add(48*time.Hour))
	if worked, err := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return t0 }).RunDue(ctx); err != nil || !worked {
		t.Fatalf("first preparation RunDue = %t, %v", worked, err)
	}
	assertFrozen("first readiness")

	// The tenant policy changes after the deadline was frozen.
	setPolicy(7, 2)

	// Semantic retry: discovery fails transiently, then succeeds a day later.
	retryAt := t0.Add(24 * time.Hour)
	requeue(retryAt)
	failing := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return retryAt })
	failing.deps.Gateway = scopeGatewayFunc(func(context.Context, llm.StructuredRequest) (llm.StructuredResult, error) {
		return llm.StructuredResult{}, errors.New("provider unavailable")
	})
	if _, err := failing.RunDue(ctx); err != nil {
		t.Fatalf("semantic retry with failing provider: %v", err)
	}
	var retried database.VideoPreparationJob
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&retried).Error; err != nil {
		t.Fatal(err)
	}
	if retried.Status == "READY" || retried.ErrorCode != "PROVIDER_UNAVAILABLE" {
		t.Fatalf("semantic retry did not fail transiently: status=%s code=%s", retried.Status, retried.ErrorCode)
	}
	assertFrozen("failed semantic retry")

	// Extraction replay after the policy change.
	replayAt := t0.Add(2 * 24 * time.Hour)
	requeue(replayAt)
	if worked, err := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return replayAt }).RunDue(ctx); err != nil || !worked {
		t.Fatalf("replay RunDue = %t, %v", worked, err)
	}
	var job database.VideoPreparationJob
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != "READY" {
		t.Fatalf("replayed preparation status=%s code=%s, want READY", job.Status, job.ErrorCode)
	}
	assertFrozen("extraction replay")
}
