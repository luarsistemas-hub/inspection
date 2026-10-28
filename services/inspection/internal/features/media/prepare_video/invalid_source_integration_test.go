//go:build integration

package prepare_video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
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

func TestInvalidOriginalFailsBeforeFramePublicationIT137IT152(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is required only to synthesize the truncated MP4 fixture")
	}
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	validPath := filepath.Join(t.TempDir(), "valid.mp4")
	command := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=5:duration=1", "-threads", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", validPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate MP4: %v: %s", err, output)
	}
	validMP4, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatal(err)
	}
	jpegImage := image.NewRGBA(image.Rect(0, 0, 8, 8))
	jpegImage.Set(0, 0, color.RGBA{R: 255, A: 255})
	var jpegBytes bytes.Buffer
	if err := jpeg.Encode(&jpegBytes, jpegImage, nil); err != nil {
		t.Fatal(err)
	}
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, jpegImage); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, filename, contentType string
		body                        []byte
	}{
		{name: "truncated MP4", filename: "broken.mp4", contentType: "video/mp4", body: validMP4[:len(validMP4)/8]},
		{name: "IT-137 JPEG disguised as MP4", filename: "tour.mp4", contentType: "video/mp4", body: jpegBytes.Bytes()},
		{name: "IT-343 PNG disguised as MP4", filename: "tour.mp4", contentType: "video/mp4", body: pngBytes.Bytes()},
		{name: "IT-152 invalid WebM", filename: "broken.webm", contentType: "video/webm", body: []byte("\x1a\x45\xdf\xa3broken-webm")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), tc.filename)
			if err := os.WriteFile(path, tc.body, 0o600); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(body)
			digest := hex.EncodeToString(sum[:])
			now := time.Now().UTC().Truncate(time.Microsecond)
			attemptID, mediaID, submissionID, preparationID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
			objectKey := "qa-video-invalid/" + fixture.TenantID.String() + "/" + tc.filename
			if err := h.Store.Client.Put(ctx, h.Store.Bucket, objectKey, bytes.NewReader(body), int64(len(body)), tc.contentType); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = h.Store.Client.(objectstore.Deleter).Delete(context.Background(), h.Store.Bucket, objectKey)
			})
			err = h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
				if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "invalid-" + mediaID.String(), PayloadDigest: digest, AdmittedAt: now, ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: objectKey, ContentType: tc.contentType, SHA256: digest, SizeBytes: int64(len(body)), Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "invalid-media-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
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
			executor, err := Setup(Dependencies{DB: h.DB, ClaimDB: h.AdminDB, Store: objectstore.VideoStore{Store: h.Store}, Decoder: video.FFmpeg{Threads: 1}, Detector: sensitivecontent.NewDisabledDetector(), Gateway: scopeGatewayFunc(func(context.Context, llm.StructuredRequest) (llm.StructuredResult, error) {
				t.Error("invalid source reached semantic gateway")
				return llm.StructuredResult{}, errors.New("unexpected scope call")
			}), Now: time.Now, TempDir: os.TempDir(), Limits: video.WorkerConfig{Concurrency: 1, DecoderThreads: 1, MaxBytes: 1 << 30, ScratchBytes: 4 << 30, RequiredFreeBytes: 8 << 30, StepTimeout: time.Minute, LeaseDuration: 120 * time.Second, HeartbeatInterval: 30 * time.Second, PollInterval: 2 * time.Second, ShutdownTimeout: 30 * time.Second}})
			if err != nil {
				t.Fatal(err)
			}
			worked, err := executor.RunDue(ctx)
			if err != nil || !worked {
				t.Fatalf("RunDue = %t, %v", worked, err)
			}
			if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
				var job database.VideoPreparationJob
				if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, preparationID).Take(&job).Error; err != nil {
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
				if job.Status != "FAILED" || job.ErrorCode != "SOURCE_INVALID" || job.ManifestDigest != "" || frames != 0 || scope != 0 || events != 0 {
					t.Errorf("invalid original state: status=%s code=%s manifest=%q frames=%d scope=%d events=%d", job.Status, job.ErrorCode, job.ManifestDigest, frames, scope, events)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestVV_IT_343PngContentDeclaredAsMp4IsSourceInvalid runs the invalid-source
// transport adapter against a real PNG body stored with an MP4 name and MIME
// type; the matrix assertion above requires SOURCE_INVALID before publication.
func TestVV_IT_343PngContentDeclaredAsMp4IsSourceInvalid(t *testing.T) {
	TestInvalidOriginalFailsBeforeFramePublicationIT137IT152(t)
}
