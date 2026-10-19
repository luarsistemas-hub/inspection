//go:build integration

package prepare_video

import (
	"context"
	"encoding/json"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/media/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/sensitivecontent"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

type flagNinthFrameModel struct{ calls int }

func (m *flagNinthFrameModel) Bytes() []byte { return []byte("qa-flag-ninth-frame") }

func (m *flagNinthFrameModel) Detect(_ context.Context, _ image.Image) ([]sensitivecontent.Region, error) {
	m.calls++
	if m.calls == 9 {
		return []sensitivecontent.Region{{Kind: "FACE", X: 0.1, Y: 0.1, Width: 0.3, Height: 0.3}}, nil
	}
	return nil, nil
}

func TestFalsePositiveRacesScreeningCompletionUsesCommittedDecisionIT141(t *testing.T) {
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
	mediaID, attemptID, preparationID, leaseToken := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "it141-attempt", PayloadDigest: "it141-digest", AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "it141/source.mp4", ContentType: "video/mp4", SHA256: "it141-digest", SizeBytes: 123, Status: "SCREENED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "it141-media", AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, Status: "PROCESSING", Phase: "SCREENING", PreparationVersion: 1, Generation: 1, Attempts: 1, NextAttemptAt: now, LeaseToken: &leaseToken, LeaseExpiresAt: ptrTime(now.Add(time.Minute)), PendingObjectKeys: []byte(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 96, Height: 64, FrameRate: 30, ScreeningStatus: "BLOCKED", Status: "RETAINED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ScreeningRun{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Status: "BLOCKED", ModelDigest: "screen-v1", Regions: []byte(`[]`), CreatedAt: now.Add(-time.Second)}).Error
	}); err != nil {
		t.Fatal(err)
	}
	media := database.MediaObject{ID: mediaID, TenantID: fixture.TenantID}
	job := database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, Status: "PROCESSING", LeaseToken: &leaseToken}
	executor := Executor{deps: Dependencies{DB: h.DB, Now: func() time.Time { return now }}}
	falsePositive := core.Service{DB: h.DB, Within: h.WithinTenant, Now: func() time.Time { return now }}
	start := make(chan struct{})
	screeningDone := make(chan error, 1)
	reviewDone := make(chan error, 1)
	go func() {
		<-start
		screeningDone <- executor.persistScreening(ctx, reservation{job: job, token: leaseToken}, media, nil, 0, false, "screen-v2", false)
	}()
	go func() {
		<-start
		reviewDone <- falsePositive.DeclareFalsePositive(ctx, fixture.TenantID, fixture.ResponsibilityID, mediaID, "QA confirmed wall poster")
	}()
	close(start)
	if err := <-screeningDone; err != nil {
		t.Fatalf("screening completion failed: %v", err)
	}
	if err := <-reviewDone; err != nil {
		code, _, _ := apperror.Public(err)
		if code != apperror.InvalidState {
			t.Fatalf("losing false-positive decision returned %s: %v", code, err)
		}
	}
	var source database.VideoSource
	var latest database.ScreeningRun
	var audits int64
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Take(&source).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Order("created_at desc,id desc").Take(&latest).Error; err != nil {
			return err
		}
		return tx.Model(&database.AuditEvent{}).Where("tenant_id=? AND target_id=? AND action='media.sensitive_false_positive_declared'", fixture.TenantID, mediaID.String()).Count(&audits).Error
	}); err != nil {
		t.Fatal(err)
	}
	if source.ScreeningStatus != "APPROVED" || (latest.Status != "APPROVED" && latest.Status != "OVERRIDDEN") || audits > 1 || latest.Status == "OVERRIDDEN" && audits != 1 {
		t.Fatalf("race produced inconsistent committed decision: source=%s latest=%s audits=%d", source.ScreeningStatus, latest.Status, audits)
	}
}

func ptrTime(value time.Time) *time.Time { return &value }

func TestNonSelectedSensitiveFrameBlocksThenAuditedOverrideReleasesIT135IT146IT598(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	path := filepath.Join(t.TempDir(), "thirty-fps.mp4")
	command := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=30:duration=1", "-threads", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate 30fps video: %v: %s", err, output)
	}
	clip, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := seedPreparationFixture(t, h, clip, "video/mp4", "CURRENT", "CAMERA")
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	model := &flagNinthFrameModel{}
	executor := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return fixture.now })
	executor.deps.Detector = sensitivecontent.Detector{Model: model}
	if worked, err := executor.RunDue(ctx); err != nil || !worked {
		t.Fatalf("screen full source: worked=%t err=%v", worked, err)
	}
	if model.calls != 30 {
		t.Errorf("detector examined %d of 30 decoded frames", model.calls)
	}
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var source database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=?", fixture.tenantID, fixture.mediaID).Take(&source).Error; err != nil {
			return err
		}
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		var frameCount, scopeCount, outboxCount int64
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Count(&frameCount).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Count(&scopeCount).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.OutboxIntent{}).Where("tenant_id=? AND correlation_id=?", fixture.tenantID, fixture.preparationID.String()).Count(&outboxCount).Error; err != nil {
			return err
		}
		var run database.ScreeningRun
		if err := tx.Where("tenant_id=? AND media_id=?", fixture.tenantID, fixture.mediaID).Take(&run).Error; err != nil {
			return err
		}
		if source.ScreeningStatus != "BLOCKED" || source.FlaggedRegionCount != 1 || job.Status != "FAILED" || job.ErrorCode != "SENSITIVE_CONTENT" || frameCount != 0 || scopeCount != 0 || outboxCount != 0 || run.Status != "BLOCKED" || !containsPTS(run.Regions, 266667) {
			t.Errorf("sensitive frame escaped full screening: source=%s count=%d job=%s/%s frames=%d scope=%d event=%d run=%s regions=%s", source.ScreeningStatus, source.FlaggedRegionCount, job.Status, job.ErrorCode, frameCount, scopeCount, outboxCount, run.Status, run.Regions)
		}
		return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Update("status", "OPEN").Error
	}); err != nil {
		t.Fatal(err)
	}
	service := core.Service{DB: h.DB, Now: func() time.Time { return fixture.now }, Within: h.WithinTenant}
	if err := service.DeclareFalsePositive(ctx, fixture.tenantID, fixture.responsibilityID, fixture.mediaID, "QA confirmed poster"); err != nil {
		t.Fatalf("audit false positive: %v", err)
	}
	if err := service.DeclareFalsePositive(ctx, fixture.tenantID, fixture.responsibilityID, fixture.mediaID, "QA confirmed poster"); err != nil {
		t.Fatalf("idempotent declaration: %v", err)
	}
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var audits int64
		if err := tx.Model(&database.AuditEvent{}).Where("tenant_id=? AND target_id=? AND action='media.sensitive_false_positive_declared'", fixture.tenantID, fixture.mediaID.String()).Count(&audits).Error; err != nil {
			return err
		}
		var source database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=?", fixture.tenantID, fixture.mediaID).Take(&source).Error; err != nil {
			return err
		}
		if audits != 1 || source.ScreeningStatus != "APPROVED" {
			t.Errorf("override not audited or source still blocked: audits=%d source=%s", audits, source.ScreeningStatus)
		}
		return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Updates(map[string]any{"status": "QUEUED", "next_attempt_at": fixture.now.Add(time.Minute)}).Error
	}); err != nil {
		t.Fatal(err)
	}
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	retry := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return fixture.now.Add(time.Minute) })
	retry.deps.Detector = sensitivecontent.Detector{Model: model}
	if worked, err := retry.RunDue(ctx); err != nil || !worked {
		t.Fatalf("prepare after override: worked=%t err=%v", worked, err)
	}
	if model.calls != 30 {
		t.Errorf("audited override re-screened source: calls=%d", model.calls)
	}
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		if job.Status != "READY" || job.ManifestDigest == "" {
			t.Errorf("overridden video was not prepared: status=%s code=%s digest=%q", job.Status, job.ErrorCode, job.ManifestDigest)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func containsPTS(regions []byte, want int64) bool {
	var decoded []struct {
		PTSMicros int64 `json:"ptsMicros"`
	}
	if err := json.Unmarshal(regions, &decoded); err != nil {
		return false
	}
	for _, region := range decoded {
		if region.PTSMicros == want {
			return true
		}
	}
	return false
}
