//go:build integration

package expire_video_sources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func TestSourceExpiryHonorsInspectionHoldThenExpiresAfterReleaseIT327(t *testing.T) {
	testExpiryWithInheritedHold(t, "inspection")
}

func TestSourceExpiryHonorsOriginHoldThenExpiresAfterRelease(t *testing.T) {
	testExpiryWithInheritedHold(t, "origin")
}

func testExpiryWithInheritedHold(t *testing.T, holdScope string) {
	t.Helper()
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
	mediaID, attemptID, preparationID, frameID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	holdID := identity.NewID()
	videoBytes := []byte("retained-video-source")
	digestBytes := sha256.Sum256(videoBytes)
	digest := hex.EncodeToString(digestBytes[:])
	originalKey := fmt.Sprintf("video-source-holds/%s/%s.mp4", fixture.TenantID, mediaID)
	frameKey := fmt.Sprintf("video-source-holds/%s/%s.jpg", fixture.TenantID, frameID)
	for _, item := range []struct {
		key, contentType string
		data             []byte
	}{{originalKey, "video/mp4", videoBytes}, {frameKey, "image/jpeg", []byte("frame")}} {
		if err := h.Store.Client.Put(ctx, h.Store.Bucket, item.key, strings.NewReader(string(item.data)), int64(len(item.data)), item.contentType); err != nil {
			t.Fatal(err)
		}
		key := item.key
		t.Cleanup(func() { _ = h.Store.Delete(context.Background(), key) })
	}
	firstReady := now.Add(-8 * 24 * time.Hour)
	expiry := now.Add(-time.Hour)
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		attempt := database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "retention-inherited-hold-attempt-" + mediaID.String(), PayloadDigest: digest, AdmittedAt: firstReady, ExpiresAt: now, CreatedAt: firstReady}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		media := database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: originalKey, ContentType: "video/mp4", SHA256: digest, SizeBytes: int64(len(videoBytes)), Status: "READY", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "retention-inherited-hold-media-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: firstReady}
		if err := tx.Create(&media).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: firstReady, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &firstReady, CreatedAt: firstReady}).Error; err != nil {
			return err
		}
		days := 5
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &firstReady, RetentionDays: &days, SourceExpiryAt: &expiry, Status: "RETAINED", CreatedAt: firstReady, UpdatedAt: firstReady}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: digest, ObjectKey: frameKey, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 5, CreatedAt: firstReady}).Error; err != nil {
			return err
		}
		hold := database.LegalHold{ID: holdID, TenantID: fixture.TenantID, Reason: "integration retained evidence", Active: true, CreatedAt: now}
		if holdScope == "inspection" {
			hold.InspectionID = &fixture.InspectionID
		} else {
			versionID := identity.NewID()
			originID := identity.NewID()
			if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.OriginVersion{ID: versionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: &fixture.ResponsibilityID, Status: "INVALIDATED", MediaKind: "VIDEO", VideoPreparationID: &preparationID, IdempotencyKey: "retention-origin-hold-" + versionID.String(), CreatedAt: now}).Error; err != nil {
				return err
			}
			hold.OriginVersionID = &versionID
		}
		return tx.Create(&hold).Error
	}); err != nil {
		t.Fatal(err)
	}
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := executor.RunDue(ctx, fixture.TenantID, now, 10)
	if err != nil || claimed != 0 {
		t.Fatalf("held sweep = claimed=%d err=%v; want 0", claimed, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var source database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Take(&source).Error; err != nil {
			return err
		}
		if source.Status != "RETAINED" {
			t.Errorf("held source status=%s, want RETAINED", source.Status)
		}
		reader, _, err := (objectstore.VideoStore{Store: h.Store}).Open(ctx, originalKey)
		if err != nil {
			return fmt.Errorf("held source object unavailable: %w", err)
		}
		_ = reader.Close()
		reader, _, err = (objectstore.VideoStore{Store: h.Store}).Open(ctx, frameKey)
		if err != nil {
			return fmt.Errorf("held frame unavailable: %w", err)
		}
		return reader.Close()
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.LegalHold{}).Where("tenant_id=? AND id=?", fixture.TenantID, holdID).Updates(map[string]any{"active": false, "released_at": now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err = executor.RunDue(ctx, fixture.TenantID, now.Add(time.Minute), 10)
	if err != nil || claimed != 1 {
		t.Fatalf("released sweep = claimed=%d err=%v; want 1", claimed, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var source database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Take(&source).Error; err != nil {
			return err
		}
		if source.Status != "EXPIRED" {
			t.Errorf("released source status=%s, want EXPIRED", source.Status)
		}
		if reader, _, err := (objectstore.VideoStore{Store: h.Store}).Open(ctx, originalKey); reader != nil || err == nil {
			if reader != nil {
				_ = reader.Close()
			}
			t.Errorf("released source object remains: reader=%v err=%v", reader, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
