//go:build integration

package harness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/png"
	"io"
	"net/http"
	"slices"
	"testing"
	"time"

	mediacore "inspection/services/inspection/internal/features/media/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

func TestVV_IT_362InterruptedPhotoMultipartResumesConfirmedParts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	h, err := NewFromEnv(ctx)
	if err != nil {
		t.Skipf("integration dependencies unavailable: %v", err)
	}
	defer h.Close()
	fixture, err := h.SeedTask06Fixture(ctx, Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Updates(map[string]any{"media_kind": "PHOTO", "status": "OPEN"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	var photosBefore int64
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=?", fixture.TenantID, fixture.ResponsibilityID).Count(&photosBefore).Error
	}); err != nil {
		t.Fatal(err)
	}
	// A real, uncompressed PNG spanning three parts within the 20 MiB ceiling.
	pixels := image.NewRGBA(image.Rect(0, 0, 1700, 1700))
	for index := range pixels.Pix {
		pixels.Pix[index] = byte(index % 239)
	}
	var encoded bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&encoded, pixels); err != nil {
		t.Fatal(err)
	}
	photo := encoded.Bytes()
	if len(photo) <= 2*objectstore.PartSizeBytes || len(photo) > objectstore.MaxOriginalBytes {
		t.Fatalf("fixture photo size=%d; want three parts within the photo ceiling", len(photo))
	}
	digest := sha256.Sum256(photo)
	service := mediacore.Service{DB: h.DB, Store: h.Store, Within: h.WithinTenant}
	upload, err := service.CreateWithKey(ctx, fixture.TenantID, fixture.ResponsibilityID, "image/png", hex.EncodeToString(digest[:]), int64(len(photo)), "vv-it-362-photo")
	if err != nil {
		t.Fatal(err)
	}
	completed := false
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if completed {
			if deleter, ok := h.Store.Client.(objectstore.Deleter); ok {
				_ = deleter.Delete(cleanupCtx, h.Store.Bucket, upload.ObjectKey)
			}
		} else {
			_ = h.Store.Client.AbortMultipart(cleanupCtx, h.Store.Bucket, upload.ObjectKey, upload.UploadID)
		}
	})
	put := func(number int) objectstore.Part {
		t.Helper()
		signed, err := service.Presign(ctx, fixture.TenantID, fixture.ResponsibilityID, upload.MediaID, []int{number})
		if err != nil || len(signed) != 1 {
			t.Fatalf("presign photo part %d: %v (%d URLs)", number, err, len(signed))
		}
		start := (number - 1) * objectstore.PartSizeBytes
		end := min(start+objectstore.PartSizeBytes, len(photo))
		request, err := http.NewRequestWithContext(ctx, http.MethodPut, signed[0].URL, bytes.NewReader(photo[start:end]))
		if err != nil {
			t.Fatal(err)
		}
		response, err := h.HTTP.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
			t.Fatalf("PUT photo part %d: HTTP %d: %s", number, response.StatusCode, body)
		}
		return objectstore.Part{Number: number, ETag: response.Header.Get("ETag")}
	}

	// The transfer is interrupted after the first confirmed part.
	first := put(1)
	state, err := service.Reconcile(ctx, fixture.TenantID, fixture.ResponsibilityID, upload.MediaID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "UPLOADING" || len(state.Received) != 1 || state.Received[0].Number != 1 || state.Received[0].ETag != first.ETag || !slices.Equal(state.MissingParts, []int{2, 3}) {
		t.Fatalf("photo reconciliation after interruption: %+v; want part 1 confirmed and 2,3 missing", state)
	}
	parts := []objectstore.Part{first}
	for _, number := range state.MissingParts {
		parts = append(parts, put(number))
	}
	if _, err := service.Complete(ctx, fixture.TenantID, fixture.ResponsibilityID, upload.MediaID, parts); err != nil {
		t.Fatalf("complete resumed photo upload: %v", err)
	}
	completed = true
	recovered, err := service.Reconcile(ctx, fixture.TenantID, fixture.ResponsibilityID, upload.MediaID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.MediaID != upload.MediaID || len(recovered.MissingParts) != 0 || recovered.Status == "UPLOADING" {
		t.Fatalf("resumed photo upload state: %+v", recovered)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var media database.MediaObject
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, upload.MediaID).Take(&media).Error; err != nil {
			return err
		}
		var photosAfter int64
		if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=?", fixture.TenantID, fixture.ResponsibilityID).Count(&photosAfter).Error; err != nil {
			return err
		}
		if media.MediaKind == "VIDEO" || media.SizeBytes != int64(len(photo)) || photosAfter != photosBefore+1 {
			t.Errorf("resumed photo media kind=%s size=%d media rows %d->%d; want one PHOTO of %d bytes", media.MediaKind, media.SizeBytes, photosBefore, photosAfter, len(photo))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	head, err := h.Store.Client.Head(ctx, h.Store.Bucket, upload.ObjectKey)
	if err != nil || head.Size != int64(len(photo)) {
		t.Fatalf("assembled photo object size=%d err=%v; want %d", head.Size, err, len(photo))
	}
}
