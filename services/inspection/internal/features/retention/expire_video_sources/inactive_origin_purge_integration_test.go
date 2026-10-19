//go:build integration

package expire_video_sources

import (
	"context"
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

func TestInactiveOriginPurgeDeletesUnreferencedFrameBank(t *testing.T) {
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
	mediaID, preparationID, versionID := identity.NewID(), identity.NewID(), identity.NewID()
	frameID := identity.NewID()
	key := "inactive-origin-purge/" + frameID.String() + ".jpg"
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, key, strings.NewReader("frame"), 5, "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Store.Delete(context.Background(), key) })
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		originID := identity.NewID()
		if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "inactive-origin-purge/source-" + mediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 5, Status: "READY", MediaKind: "VIDEO", IdempotencyKey: "inactive-origin-media-" + mediaID.String(), RequirementKey: "video:main", AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: strings.Repeat("a", 64), ManifestDigest: strings.Repeat("a", 64), PendingObjectKeys: []byte(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.OriginVersion{ID: versionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: fixture.ResponsibilityID, Status: "INVALIDATED", MediaKind: "VIDEO", VideoPreparationID: &preparationID, IdempotencyKey: "inactive-origin-purge-" + versionID.String(), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoFrame{ID: frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: strings.Repeat("a", 64), ObjectKey: key, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 5, CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	count, err := executor.PurgeInactiveOriginBanks(ctx, fixture.TenantID, 10)
	if err != nil || count != 1 {
		t.Fatalf("PurgeInactiveOriginBanks() = %d, %v; want 1", count, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var frames, scopes int64
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.TenantID, preparationID).Count(&frames).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND preparation_id=?", fixture.TenantID, preparationID).Count(&scopes).Error; err != nil {
			return err
		}
		if frames != 0 || scopes != 0 {
			t.Errorf("purged bank rows: frames=%d scopes=%d; want 0/0", frames, scopes)
		}
		if reader, _, err := (objectstore.VideoStore{Store: h.Store}).Open(ctx, key); reader != nil || err == nil {
			if reader != nil {
				_ = reader.Close()
			}
			t.Errorf("inactive frame object remains: reader=%v err=%v", reader, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInactiveOriginPurgePreservesFrameBanksReferencedByRetainedEvidence(t *testing.T) {
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
	type bank struct {
		mediaID, preparationID, versionID, responsibilityID identity.ID
		key, ownerType                                      string
	}
	banks := []bank{
		{mediaID: identity.NewID(), preparationID: identity.NewID(), versionID: identity.NewID(), responsibilityID: identity.NewID(), key: "inactive-origin-purge/report.jpg", ownerType: "REPORT_SNAPSHOT"},
		{mediaID: identity.NewID(), preparationID: identity.NewID(), versionID: identity.NewID(), responsibilityID: identity.NewID(), key: "inactive-origin-purge/plan.jpg", ownerType: "VIDEO_PLAN"},
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i := range banks {
		banks[i].key = "inactive-origin-purge/" + banks[i].preparationID.String() + ".jpg"
		key := banks[i].key
		if err := h.Store.Client.Put(ctx, h.Store.Bucket, key, strings.NewReader("frame"), 5, "image/jpeg"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Store.Delete(context.Background(), key) })
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		originID := identity.NewID()
		if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		for _, item := range banks {
			if err := tx.Create(&database.Responsibility{ID: item.responsibilityID, TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, ParticipantID: fixture.ParticipantID, Status: "EXPIRED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.MediaObject{ID: item.mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "inactive-origin-purge/source-" + item.mediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("b", 64), SizeBytes: 5, Status: "READY", MediaKind: "VIDEO", IdempotencyKey: "inactive-origin-media-" + item.mediaID.String(), RequirementKey: "video:main", AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.VideoPreparationJob{ID: item.preparationID, TenantID: fixture.TenantID, MediaID: item.mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: strings.Repeat("b", 64), ManifestDigest: strings.Repeat("b", 64), PendingObjectKeys: []byte(`[]`), CreatedAt: now}).Error; err != nil {
				return err
			}
			versionNumber := 1
			if item.ownerType == "VIDEO_PLAN" {
				versionNumber = 2
			}
			if err := tx.Create(&database.OriginVersion{ID: item.versionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: versionNumber, ResponsibilityID: item.responsibilityID, Status: "INVALIDATED", MediaKind: "VIDEO", VideoPreparationID: &item.preparationID, IdempotencyKey: "inactive-origin-ref-" + item.versionID.String(), CreatedAt: now}).Error; err != nil {
				return err
			}
			frame := database.VideoFrame{ID: identity.NewID(), TenantID: fixture.TenantID, SourceMediaID: item.mediaID, PreparationID: item.preparationID, PTSMicros: 0, SHA256: strings.Repeat("b", 64), ObjectKey: item.key, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 5, CreatedAt: now}
			if err := tx.Create(&frame).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.VideoEvidenceReference{ID: identity.NewID(), TenantID: fixture.TenantID, PreparationID: item.preparationID, OwnerType: item.ownerType, OwnerID: identity.NewID(), CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	count, err := executor.PurgeInactiveOriginBanks(ctx, fixture.TenantID, 10)
	if err != nil || count != 0 {
		t.Fatalf("PurgeInactiveOriginBanks() = %d, %v; want 0", count, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		for _, item := range banks {
			var frames int64
			if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.TenantID, item.preparationID).Count(&frames).Error; err != nil {
				return err
			}
			if frames != 1 {
				t.Errorf("%s frame rows=%d, want 1", item.ownerType, frames)
			}
			reader, _, err := (objectstore.VideoStore{Store: h.Store}).Open(ctx, item.key)
			if err != nil {
				return err
			}
			if err := reader.Close(); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
