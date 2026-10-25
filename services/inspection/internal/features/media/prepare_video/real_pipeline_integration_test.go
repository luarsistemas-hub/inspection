//go:build integration

package prepare_video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/sensitivecontent"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

func TestRealClipCommitsFrameBankAndScope(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Fatalf("real clip test requires ffmpeg: %v", err)
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Fatalf("real clip test requires ffprobe: %v", err)
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
	path := filepath.Join(t.TempDir(), "current.mp4")
	command := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=5:duration=1", "-threads", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate real video: %v: %s", err, output)
	}
	clip, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(clip)
	digest := hex.EncodeToString(sum[:])
	now := time.Now().UTC().Truncate(time.Microsecond)
	attemptID, mediaID, submissionID, preparationID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	objectKey := "qa-video/" + fixture.TenantID.String() + "/" + mediaID.String() + ".mp4"
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, objectKey, bytes.NewReader(clip), int64(len(clip)), "video/mp4"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = h.Store.Client.(objectstore.Deleter).Delete(context.Background(), h.Store.Bucket, objectKey)
	})
	err = h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "real-clip-" + mediaID.String(), PayloadDigest: digest, AdmittedAt: now, ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: objectKey, ContentType: "video/mp4", SHA256: digest, SizeBytes: int64(len(clip)), Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "real-clip-media-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: fixture.DraftID, ResponsibilityID: fixture.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: digest, SubmittedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "QUEUED", Phase: "RECEIVED", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, PendingObjectKeys: []byte("[]"), CreatedAt: now}).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := Setup(Dependencies{
		DB: h.DB, ClaimDB: h.AdminDB, Store: objectstore.VideoStore{Store: h.Store}, Decoder: video.FFmpeg{Threads: 1}, Detector: sensitivecontent.NewDisabledDetector(),
		Gateway: scopeGatewayFunc(func(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
			observations := make([]video.ScopeObservation, 0, len(request.Images))
			for _, frame := range request.Images {
				observations = append(observations, video.ScopeObservation{FrameID: frame.EvidenceID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", Landmarks: []string{}})
			}
			response, err := json.Marshal(map[string]any{"observations": observations, "unresolvedFrameIds": []string{}})
			return llm.StructuredResult{JSON: response}, err
		}),
		Now: func() time.Time { return now }, TempDir: os.TempDir(), Limits: video.WorkerConfig{Concurrency: 1, DecoderThreads: 1, MaxBytes: 1 << 30, ScratchBytes: 4 << 30, RequiredFreeBytes: 8 << 30, StepTimeout: time.Minute, LeaseDuration: 120 * time.Second, HeartbeatInterval: 30 * time.Second, PollInterval: 2 * time.Second, ShutdownTimeout: 30 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	worked, err := boundary.RunDue(ctx)
	if err != nil || !worked {
		t.Fatalf("RunDue = %t, %v", worked, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, preparationID).Take(&job).Error; err != nil {
			return err
		}
		var source database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Take(&source).Error; err != nil {
			return err
		}
		var frames, scope, events int64
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.TenantID, preparationID).Count(&frames).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND preparation_id=?", fixture.TenantID, preparationID).Count(&scope).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='media.video_prepared.v1' AND correlation_id=?", fixture.TenantID, preparationID.String()).Count(&events).Error; err != nil {
			return err
		}
		if job.Status != "READY" || job.ManifestDigest == "" || source.FirstFrameReadyAt == nil || source.SourceExpiryAt == nil || source.RetentionDays == nil || *source.RetentionDays != 30 || frames == 0 || scope != 1 || events != 1 {
			t.Errorf("incomplete real preparation: status=%s digest=%t anchor=%t frames=%d scope=%d events=%d", job.Status, job.ManifestDigest != "", source.FirstFrameReadyAt != nil, frames, scope, events)
		}
		if source.FirstFrameReadyAt != nil && !source.FirstFrameReadyAt.Equal(now) {
			t.Errorf("first readiness changed: %s != %s", source.FirstFrameReadyAt, now)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	limitedAttemptID, limitedMediaID, limitedSubmissionID, limitedPreparationID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	limitedObjectKey := "qa-video/derivative-limit/" + limitedMediaID.String() + ".mp4"
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, limitedObjectKey, bytes.NewReader(clip), int64(len(clip)), "video/mp4"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = h.Store.Client.(objectstore.Deleter).Delete(context.Background(), h.Store.Bucket, limitedObjectKey)
		var keys []string
		if err := h.WithinTenant(context.Background(), fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND source_media_id=?", fixture.TenantID, limitedMediaID).Pluck("object_key", &keys).Error
		}); err == nil {
			for _, key := range keys {
				_ = h.Store.Client.(objectstore.Deleter).Delete(context.Background(), h.Store.Bucket, key)
			}
		}
	})
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.VideoAttempt{ID: limitedAttemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "derivative-limit-attempt", PayloadDigest: digest, AdmittedAt: now, ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: limitedMediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: limitedObjectKey, ContentType: "video/mp4", SHA256: digest, SizeBytes: int64(len(clip)), Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &limitedAttemptID, RequirementKey: "video:main", IdempotencyKey: "derivative-limit-media", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SubmissionVersion{ID: limitedSubmissionID, TenantID: fixture.TenantID, DraftID: fixture.DraftID, ResponsibilityID: fixture.ResponsibilityID, VersionNumber: 2, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: digest, SubmittedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoPreparationJob{ID: limitedPreparationID, TenantID: fixture.TenantID, MediaID: limitedMediaID, SubmissionID: &limitedSubmissionID, Status: "QUEUED", Phase: "RECEIVED", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, PendingObjectKeys: []byte("[]"), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	limited, err := Setup(Dependencies{DB: h.DB, ClaimDB: h.AdminDB, Store: objectstore.VideoStore{Store: h.Store}, Decoder: video.FFmpeg{Threads: 1}, Detector: sensitivecontent.NewDisabledDetector(), Gateway: boundary.deps.Gateway, Now: func() time.Time { return now }, TempDir: os.TempDir(), Limits: boundary.deps.Limits, DerivativeBankBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	worked, err = limited.RunDue(ctx)
	if err != nil || !worked {
		t.Fatalf("limited RunDue = %t, %v", worked, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, limitedPreparationID).Take(&job).Error; err != nil {
			return err
		}
		var source database.VideoSource
		sourceErr := tx.Where("tenant_id=? AND media_id=?", fixture.TenantID, limitedMediaID).Take(&source).Error
		var scope int64
		if err := tx.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND preparation_id=?", fixture.TenantID, limitedPreparationID).Count(&scope).Error; err != nil {
			return err
		}
		if job.Status != "FAILED" || job.ErrorCode != "RESOURCE_LIMIT" || job.ManifestDigest != "" || scope != 0 || sourceErr == nil && source.FirstFrameReadyAt != nil {
			t.Errorf("derivative bank overflow published readiness: job=%+v source=%+v sourceErr=%v scope=%d", job, source, sourceErr, scope)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
