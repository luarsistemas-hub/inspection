//go:build integration

package process_deletion_requests

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

func TestDeletionRequestProcessorDeletesUnheldSourceAndBlocksHeldSource(t *testing.T) {
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
	now := time.Now().UTC().Truncate(time.Microsecond)
	type sourceCase struct {
		mediaID, attemptID, requestID identity.ID
		key                           string
		held                          bool
	}
	cases := []sourceCase{
		{mediaID: identity.NewID(), attemptID: identity.NewID(), requestID: identity.NewID(), held: false},
		{mediaID: identity.NewID(), attemptID: identity.NewID(), requestID: identity.NewID(), held: true},
	}
	for index := range cases {
		cases[index].key = fmt.Sprintf("video-sources/deletion-request/%s", cases[index].mediaID)
		if err := h.Store.Client.Put(ctx, h.Store.Bucket, cases[index].key, strings.NewReader("video-bytes"), int64(len("video-bytes")), "video/mp4"); err != nil {
			t.Fatal(err)
		}
		key := cases[index].key
		t.Cleanup(func() { _ = h.Store.Delete(context.Background(), key) })
	}
	digestBytes := sha256.Sum256([]byte("video-bytes"))
	digest := hex.EncodeToString(digestBytes[:])
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		for index, item := range cases {
			attempt := database.VideoAttempt{ID: item.attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: fmt.Sprintf("deletion-request-%d-attempt", index), PayloadDigest: digest, AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}
			if err := tx.Create(&attempt).Error; err != nil {
				return err
			}
			media := database.MediaObject{ID: item.mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: item.key, ContentType: "video/mp4", SHA256: digest, SizeBytes: int64(len("video-bytes")), Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &item.attemptID, RequirementKey: "video:main", IdempotencyKey: fmt.Sprintf("deletion-request-%d-media", index), AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: now}
			if err := tx.Create(&media).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: item.mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", Status: "RETAINED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			request := database.DeletionRequest{ID: item.requestID, TenantID: fixture.TenantID, SourceMediaID: &item.mediaID, Status: "REQUESTED", RequestedAt: now}
			if err := tx.Create(&request).Error; err != nil {
				return err
			}
			if item.held {
				hold := database.LegalHold{ID: identity.NewID(), TenantID: fixture.TenantID, SourceMediaID: &item.mediaID, Reason: "integration hold", Active: true, CreatedAt: now}
				if err := tx.Create(&hold).Error; err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := executor.RunDue(ctx, fixture.TenantID, 10)
	if err != nil || processed != 2 {
		t.Fatalf("RunDue() = processed=%d err=%v; want 2", processed, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		for _, item := range cases {
			var source database.VideoSource
			if err := tx.Where("tenant_id=? AND media_id=?", fixture.TenantID, item.mediaID).Take(&source).Error; err != nil {
				return err
			}
			var request database.DeletionRequest
			if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, item.requestID).Take(&request).Error; err != nil {
				return err
			}
			if item.held {
				if source.Status != "RETAINED" || request.Status != "BLOCKED" {
					t.Errorf("held request source=%s status=%s; want RETAINED/BLOCKED", source.Status, request.Status)
				}
				if _, _, err := (objectstore.VideoStore{Store: h.Store}).Open(ctx, item.key); err != nil {
					t.Errorf("held source object unavailable: %v", err)
				}
			} else {
				if source.Status != "EXPIRED" || request.Status != "COMPLETED" {
					t.Errorf("unheld request source=%s status=%s; want EXPIRED/COMPLETED", source.Status, request.Status)
				}
				if reader, _, err := (objectstore.VideoStore{Store: h.Store}).Open(ctx, item.key); reader != nil || err == nil {
					if reader != nil {
						_ = reader.Close()
					}
					t.Errorf("unheld source remains in object storage: reader=%v err=%v", reader, err)
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
