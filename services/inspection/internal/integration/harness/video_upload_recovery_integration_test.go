//go:build integration

package harness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"slices"
	"testing"
	"time"

	"inspection/libs/identity"
	mediacore "inspection/services/inspection/internal/features/media/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

// This exercises the provider-backed recovery boundary used by the Capture
// client. A lost HTTP response is represented by intentionally discarding the
// result of Complete before reading the authoritative status again.
func TestTask05VideoMultipartRecoveryIT119IT127(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	h, err := NewFromEnv(ctx)
	if err != nil {
		t.Fatalf("IT-119/IT-127 require PostgreSQL, RabbitMQ and MinIO: %v", err)
	}
	defer h.Close()
	fixture, err := h.SeedTask06Fixture(ctx, Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	attemptID := identity.NewID()
	err = h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).
			Updates(map[string]any{"media_kind": "VIDEO", "status": "OPEN"}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoAttempt{
			ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID,
			Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900,
			MaxBytes: objectstore.MaxVideoBytes, AcquisitionSource: "CAMERA", Status: "ACTIVE",
			IdempotencyKey: "task05-recovery-attempt", PayloadDigest: "task05-recovery",
			AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now,
		}).Error
	})
	if err != nil {
		t.Fatal(err)
	}

	video := make([]byte, 2*objectstore.PartSizeBytes+127)
	for index := range video {
		video[index] = byte(index % 251)
	}
	digest := sha256.Sum256(video)
	service := mediacore.Service{DB: h.DB, Store: h.Store, Within: h.WithinTenant}
	upload, err := service.CreateVideoWithAttempt(ctx, fixture.TenantID, fixture.ResponsibilityID, attemptID, "video/mp4", hex.EncodeToString(digest[:]), int64(len(video)), "task05-recovery-upload")
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
			t.Fatalf("presign part %d: %v (%d URLs)", number, err, len(signed))
		}
		start := (number - 1) * objectstore.PartSizeBytes
		end := min(start+objectstore.PartSizeBytes, len(video))
		request, err := http.NewRequestWithContext(ctx, http.MethodPut, signed[0].URL, bytes.NewReader(video[start:end]))
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
			t.Fatalf("PUT part %d: HTTP %d: %s", number, response.StatusCode, body)
		}
		etag := response.Header.Get("ETag")
		if etag == "" {
			t.Fatalf("PUT part %d returned no ETag", number)
		}
		return objectstore.Part{Number: number, ETag: etag}
	}

	first := put(1)
	state, err := service.Reconcile(ctx, fixture.TenantID, fixture.ResponsibilityID, upload.MediaID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "UPLOADING" || len(state.Received) != 1 || state.Received[0].Number != 1 || state.Received[0].ETag != first.ETag || !slices.Equal(state.MissingParts, []int{2, 3}) {
		t.Fatalf("IT-119 unexpected authoritative recovery: %+v", state)
	}
	parts := []objectstore.Part{first}
	for _, number := range state.MissingParts {
		parts = append(parts, put(number))
	}
	if len(parts) != 3 {
		t.Fatalf("IT-119 sent %d parts instead of one initial and two resumed", len(parts))
	}
	if _, err := service.Complete(ctx, fixture.TenantID, fixture.ResponsibilityID, upload.MediaID, parts); err != nil {
		t.Fatal(err)
	}
	completed = true

	// Simulate the client losing the successful completion response.
	recovered, err := service.Reconcile(ctx, fixture.TenantID, fixture.ResponsibilityID, upload.MediaID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != "RECEIVED" || recovered.MediaID != upload.MediaID || len(recovered.Received) != 3 || len(recovered.MissingParts) != 0 {
		t.Fatalf("IT-127 missing existing receipt after lost response: %+v", recovered)
	}
	replayed, err := service.CreateVideoWithAttempt(ctx, fixture.TenantID, fixture.ResponsibilityID, attemptID, "video/mp4", hex.EncodeToString(digest[:]), int64(len(video)), "task05-recovery-upload")
	if err != nil || replayed.MediaID != upload.MediaID {
		t.Fatalf("IT-127 did not preserve media identity on retry: %v (%s != %s)", err, replayed.MediaID, upload.MediaID)
	}
	var mediaCount int64
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=? AND media_kind='VIDEO'", fixture.TenantID, fixture.ResponsibilityID).Count(&mediaCount).Error
	}); err != nil {
		t.Fatal(err)
	}
	if mediaCount != 1 {
		t.Fatalf("IT-127 created %d video media objects", mediaCount)
	}
}
