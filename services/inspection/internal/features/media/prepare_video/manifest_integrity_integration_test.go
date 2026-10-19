//go:build integration

package prepare_video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

func TestManifestCommitRejectsOutOfRangePTSAndWrongSourceDigestIT167(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	for _, scenario := range []string{"pts-outside-duration", "source-digest-mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := seedPreparationFixture(t, h, syntheticH264Clip(t), "video/mp4", "ORIGIN", "CAMERA")
			now := time.Now().UTC().Truncate(time.Microsecond)
			frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
			for y := 0; y < 2; y++ {
				for x := 0; x < 2; x++ {
					frame.Set(x, y, color.RGBA{R: 64, G: 128, B: 192, A: 255})
				}
			}
			var imageBytes bytes.Buffer
			if err := jpeg.Encode(&imageBytes, frame, nil); err != nil {
				t.Fatal(err)
			}
			frameBytes := imageBytes.Bytes()
			frameSum := sha256.Sum256(frameBytes)
			frameDigest := hex.EncodeToString(frameSum[:])
			objectKey := "qa-video-it167/" + scenario + "/" + fixture.tenantID.String() + "/" + fixture.preparationID.String() + ".jpg"
			if err := h.Store.Client.Put(ctx, h.Store.Bucket, objectKey, bytes.NewReader(frameBytes), int64(len(frameBytes)), "image/jpeg"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = h.Store.Client.(objectstore.Deleter).Delete(context.Background(), h.Store.Bucket, objectKey)
			})
			framePTS := int64(0)
			if scenario == "pts-outside-duration" {
				framePTS = 1_000_001
			}
			sourceDigest := fixtureMediaDigest(t, h, fixture)
			if scenario == "source-digest-mismatch" {
				sourceDigest = "mismatched-source-digest"
			}
			leaseToken := identity.NewID()
			leaseExpiry := now.Add(time.Minute)
			if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
				source := database.VideoSource{ID: identity.NewID(), TenantID: fixture.tenantID, MediaID: fixture.mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 1, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &now, Status: "RETAINED", CreatedAt: now, UpdatedAt: now}
				if err := tx.Create(&source).Error; err != nil {
					return err
				}
				frame := database.VideoFrame{ID: identity.NewID(), TenantID: fixture.tenantID, SourceMediaID: fixture.mediaID, PreparationID: fixture.preparationID, PTSMicros: framePTS, SHA256: frameDigest, ObjectKey: objectKey, Width: 2, Height: 2, Role: "BASELINE", Quality: "GOOD", SizeBytes: int64(len(frameBytes)), CreatedAt: now}
				if err := tx.Create(&frame).Error; err != nil {
					return err
				}
				return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Updates(map[string]any{"status": "PROCESSING", "phase": "FRAME_BANK_READY", "attempts": 1, "lease_token": leaseToken, "lease_expires_at": leaseExpiry, "source_sha256": sourceDigest, "manifest_digest": "", "error_code": ""}).Error
			}); err != nil {
				t.Fatal(err)
			}
			executor := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return now })
			r := reservation{job: database.VideoPreparationJob{ID: fixture.preparationID, TenantID: fixture.tenantID, MediaID: fixture.mediaID, Phase: "FRAME_BANK_READY", SourceSHA256: sourceDigest}, token: leaseToken}
			if err := executor.prepare(ctx, r); err == nil {
				t.Fatal("prepare accepted invalid manifest integrity")
			} else if finishErr := executor.finishFailure(ctx, r, safeCode(err)); finishErr != nil {
				t.Fatalf("persist manifest integrity rejection: %v", finishErr)
			}
			if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
				var job database.VideoPreparationJob
				if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
					return err
				}
				if job.Status != "FAILED" || job.ErrorCode != "FRAME_INTEGRITY_FAILED" || job.ManifestDigest != "" {
					t.Errorf("manifest integrity rejection status=%s code=%s digest=%q", job.Status, job.ErrorCode, job.ManifestDigest)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func fixtureMediaDigest(t *testing.T, h *harness.Harness, fixture preparationFixture) string {
	t.Helper()
	var media database.MediaObject
	if err := h.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.mediaID).Take(&media).Error
	}); err != nil {
		t.Fatal(err)
	}
	return media.SHA256
}
