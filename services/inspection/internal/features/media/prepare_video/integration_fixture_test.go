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
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

type preparationFixture struct {
	tenantID, mediaID, preparationID identity.ID
	originID                         identity.ID
	responsibilityID                 identity.ID
	now                              time.Time
}

func syntheticH264Clip(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clip.mp4")
	command := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=5:duration=1", "-threads", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate H.264 fixture: %v: %s", err, output)
	}
	clip, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return clip
}

func seedPreparationFixture(t *testing.T, h *harness.Harness, clip []byte, contentType, kind, acquisition string) preparationFixture {
	t.Helper()
	ctx := context.Background()
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	attemptID, mediaID, submissionID, preparationID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	responsibilityID, draftID := fixture.ResponsibilityID, fixture.DraftID
	originID, originVersionID := identity.ID{}, identity.ID{}
	if kind == "ORIGIN" {
		responsibilityID, draftID = identity.NewID(), identity.NewID()
		originID, originVersionID = identity.NewID(), identity.NewID()
	}
	sum := sha256.Sum256(clip)
	digest := hex.EncodeToString(sum[:])
	objectKey := "qa-video-preparation/" + fixture.TenantID.String() + "/" + mediaID.String()
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, objectKey, bytes.NewReader(clip), int64(len(clip)), contentType); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var keys []string
		if err := h.WithinTenant(context.Background(), fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND source_media_id=?", fixture.TenantID, mediaID).Pluck("object_key", &keys).Error; err != nil {
				return err
			}
			var proxyKeys []string
			if err := tx.Model(&database.MediaDerivative{}).Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Pluck("object_key", &proxyKeys).Error; err != nil {
				return err
			}
			keys = append(keys, proxyKeys...)
			return nil
		}); err != nil {
			t.Errorf("list preparation objects for cleanup: %v", err)
		}
		keys = append(keys, objectKey)
		for _, key := range keys {
			if err := h.Store.Client.(objectstore.Deleter).Delete(context.Background(), h.Store.Bucket, key); err != nil {
				t.Errorf("cleanup preparation object: %v", err)
			}
		}
	})
	err = h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if kind == "SUPPLEMENT" {
			if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fixture.TenantID, responsibilityID).Updates(map[string]any{
				"kind":         "RECAPTURE",
				"requirements": json.RawMessage(`[{"key":"video:supplement","label":"Complemento","evidenceKind":"VIDEO","required":true,"minimumCount":1,"maximumCount":1}]`),
			}).Error; err != nil {
				return err
			}
			var responsibility database.Responsibility
			if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, responsibilityID).Take(&responsibility).Error; err != nil {
				return err
			}
			requestID := identity.NewID()
			if err := tx.Create(&database.RecaptureRequest{ID: requestID, TenantID: fixture.TenantID, InspectionID: responsibility.InspectionID, ResponsibilityID: responsibilityID, Status: "REQUESTED", DeadlineAt: now.Add(24 * time.Hour), IdempotencyKey: "fixture-supplement-" + requestID.String(), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
		}
		if kind == "ORIGIN" {
			if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.OriginVersion{ID: originVersionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: &responsibilityID, Status: "SUBMITTED", MediaKind: "VIDEO", IdempotencyKey: "fixture-origin-" + originID.String(), CreatedAt: now, SubmittedAt: &now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.CaptureDraft{ID: draftID, TenantID: fixture.TenantID, ResponsibilityID: responsibilityID, Kind: "ORIGIN", MediaKind: "VIDEO", TemplateVersionID: fixture.TemplateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[]`), Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: responsibilityID, Kind: kind, PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: acquisition, Status: "RECEIVED", IdempotencyKey: "fixture-" + mediaID.String(), PayloadDigest: digest, AdmittedAt: now, ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		requirementKey := "video:main"
		if kind == "SUPPLEMENT" {
			requirementKey = "video:supplement"
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: responsibilityID, ObjectKey: objectKey, ContentType: contentType, SHA256: digest, SizeBytes: int64(len(clip)), Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: requirementKey, IdempotencyKey: "fixture-media-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: acquisition, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: draftID, ResponsibilityID: responsibilityID, VersionNumber: 1, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: digest, SubmittedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "QUEUED", Phase: "RECEIVED", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, PendingObjectKeys: []byte("[]"), CreatedAt: now}).Error; err != nil {
			return err
		}
		if kind == "ORIGIN" {
			return tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", fixture.TenantID, originVersionID).Update("video_preparation_id", preparationID).Error
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return preparationFixture{tenantID: fixture.TenantID, mediaID: mediaID, preparationID: preparationID, originID: originID, responsibilityID: responsibilityID, now: now}
}

func markPreparationAsSupplement(t *testing.T, h *harness.Harness, fixture preparationFixture) identity.ID {
	t.Helper()
	var request database.RecaptureRequest
	if err := h.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND responsibility_id=? AND status='REQUESTED'", fixture.tenantID, fixture.responsibilityID).Take(&request).Error
	}); err != nil {
		t.Fatal(err)
	}
	return request.ID
}
