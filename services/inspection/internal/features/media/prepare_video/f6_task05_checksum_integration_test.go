//go:build integration

package prepare_video

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

func TestVVIT122CorruptedPersistedVideoChunkFailsChecksumValidation(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	clip := syntheticH264Clip(t)
	fixture := seedPreparationFixture(t, h, clip, "video/mp4", "CURRENT", "CAMERA")
	var media database.MediaObject
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.mediaID).Take(&media).Error
	}); err != nil {
		t.Fatal(err)
	}
	// Keep the final manifest hash from the original recording but alter bytes
	// persisted in object storage after that hash was calculated locally.
	corrupted := append([]byte(nil), clip...)
	corrupted[len(corrupted)/2] ^= 0xff
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, media.ObjectKey, bytes.NewReader(corrupted), int64(len(corrupted)), media.ContentType); err != nil {
		t.Fatal(err)
	}
	executor := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, time.Now)
	worked, err := executor.RunDue(ctx)
	if err != nil || !worked {
		t.Fatalf("RunDue = %t, %v", worked, err)
	}
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		if job.Status != "FAILED" || job.ErrorCode != "CHECKSUM_MISMATCH" {
			t.Fatalf("corrupted upload validation status=%s error=%s; want FAILED/CHECKSUM_MISMATCH", job.Status, job.ErrorCode)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
