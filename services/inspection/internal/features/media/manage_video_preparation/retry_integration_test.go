//go:build integration

package manage_video_preparation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

type retryFixture struct {
	tenantID, mediaID, jobID, actorID identity.ID
	bus                               *mediator.Bus
}

func seedRetryFixture(t *testing.T, h *harness.Harness, status string, version int, expired bool) retryFixture {
	t.Helper()
	ctx := context.Background()
	f, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	mediaID, attemptID, submissionID, jobID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: f.TenantID, ResponsibilityID: f.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "retry-fixture-" + mediaID.String(), PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: f.TenantID, ResponsibilityID: f.ResponsibilityID, ObjectKey: "retry-fixture/" + mediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 123, Status: "VERIFIED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "retry-media-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: f.TenantID, DraftID: f.DraftID, ResponsibilityID: f.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: strings.Repeat("b", 64), SubmittedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: jobID, TenantID: f.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: status, Phase: "EXTRACTING", PreparationVersion: version, Generation: 1, Attempts: 4, NextAttemptAt: now, ErrorCode: "PREPARATION_FAILED", PendingObjectKeys: []byte("[]"), CreatedAt: now}).Error; err != nil {
			return err
		}
		if expired {
			return tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: f.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 96, Height: 64, FrameRate: 5, ScreeningStatus: "APPROVED", Status: "EXPIRED", CreatedAt: now, UpdatedAt: now}).Error
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := Setup(Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Within: h.WithinTenant}); err != nil {
		t.Fatal(err)
	}
	return retryFixture{tenantID: f.TenantID, mediaID: mediaID, jobID: jobID, actorID: f.IdentityID, bus: bus}
}

func (f retryFixture) context() context.Context {
	return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: f.tenantID, CorrelationID: "qa-retry", Principal: requestctx.Principal{IdentityID: f.actorID, TenantID: f.tenantID, Roles: []string{auth.TenantAdmin}}})
}

func TestExpiredSourceWithoutFramesRejectsRetryIT153(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	h, err := harness.NewFromEnv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f := seedRetryFixture(t, h, "FAILED", 4, true)
	_, err = f.bus.Send(f.context(), RetryCommand{TenantID: f.tenantID, MediaID: f.mediaID, ExpectedPreparationVersion: 4, MutationID: "expired-retry"})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidState || !strings.Contains(appErr.Safe, "replacement") {
		t.Fatalf("expired source retry = %v, want INVALID_STATE with replacement guidance", err)
	}
	if err := h.WithinTenant(context.Background(), f.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", f.tenantID, f.jobID).Take(&job).Error; err != nil {
			return err
		}
		if job.Status != "FAILED" || job.PreparationVersion != 4 {
			t.Errorf("rejected retry changed job: status=%s version=%d", job.Status, job.PreparationVersion)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentRetryVersionFourAllowsOneWinnerIT156(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	h, err := harness.NewFromEnv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f := seedRetryFixture(t, h, "FAILED", 4, false)
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, key := range []string{"retry-a", "retry-b"} {
		group.Add(1)
		go func(key string) {
			defer group.Done()
			_, err := f.bus.Send(f.context(), RetryCommand{TenantID: f.tenantID, MediaID: f.mediaID, ExpectedPreparationVersion: 4, MutationID: key})
			results <- err
		}(key)
	}
	group.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
			continue
		}
		var appErr *apperror.Error
		if errors.As(err, &appErr) && appErr.Code == apperror.Conflict {
			conflict++
			continue
		}
		t.Fatalf("unexpected concurrent retry result: %v", err)
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("race results: success=%d conflict=%d", success, conflict)
	}
	if err := h.WithinTenant(context.Background(), f.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", f.tenantID, f.jobID).Take(&job).Error; err != nil {
			return err
		}
		var audits int64
		if err := tx.Model(&database.AuditEvent{}).Where("tenant_id=? AND target_id=? AND action='video.preparation_retried'", f.tenantID, f.mediaID.String()).Count(&audits).Error; err != nil {
			return err
		}
		if job.Status != "QUEUED" || job.PreparationVersion != 5 || audits != 1 {
			t.Errorf("retry race persisted status=%s version=%d audits=%d", job.Status, job.PreparationVersion, audits)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadyRetryRejectedAndNewProfilePreservesFirstReadinessAnchor(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	h, err := harness.NewFromEnv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f := seedRetryFixture(t, h, "READY", 4, false)
	anchor := time.Now().UTC().Add(-36 * time.Hour).Truncate(time.Microsecond)
	if err := h.WithinTenant(context.Background(), f.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", f.tenantID, f.jobID).Updates(map[string]any{"first_ready_at": anchor, "profile_digest": strings.Repeat("a", 64), "manifest_digest": strings.Repeat("b", 64)}).Error
	}); err != nil {
		t.Fatal(err)
	}
	_, err = f.bus.Send(f.context(), RetryCommand{TenantID: f.tenantID, MediaID: f.mediaID, ExpectedPreparationVersion: 4, MutationID: "ready-retry-rejected"})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidState {
		t.Fatalf("retry of READY source = %v, want INVALID_STATE", err)
	}
	newProfile := video.DefaultProfile().Digest()
	raw, err := f.bus.Send(f.context(), ReprocessCommand{TenantID: f.tenantID, MediaID: f.mediaID, ExpectedPreparationVersion: 4, ProfileDigest: newProfile, MutationID: "new-profile-reprocess"})
	if err != nil {
		t.Fatalf("reprocess with deployed profile: %v", err)
	}
	status := raw.(Status)
	if status.PreparationID == f.jobID || status.FirstFrameReadyAt == nil || !status.FirstFrameReadyAt.Equal(anchor) {
		t.Fatalf("new preparation=%+v; want new ID and preserved first-readiness anchor", status)
	}
	if err := h.WithinTenant(context.Background(), f.tenantID, func(tx *gorm.DB) error {
		var rows []database.VideoPreparationJob
		if err := tx.WithContext(context.Background()).Where("tenant_id=? AND media_id=?", f.tenantID, f.mediaID).Find(&rows).Error; err != nil {
			return err
		}
		profiles := make(map[string]database.VideoPreparationJob, len(rows))
		for _, row := range rows {
			profiles[row.ProfileDigest] = row
		}
		original, hasOriginal := profiles[strings.Repeat("a", 64)]
		updated, hasUpdated := profiles[newProfile]
		if len(rows) != 2 || !hasOriginal || !hasUpdated || original.Generation != 1 || updated.Generation != 1 || original.FirstReadyAt == nil || updated.FirstReadyAt == nil || !original.FirstReadyAt.Equal(anchor) || !updated.FirstReadyAt.Equal(anchor) {
			t.Errorf("profile preparations=%+v; want two profiles, each at generation one", rows)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
