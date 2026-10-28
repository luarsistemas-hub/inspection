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

func TestInactiveOriginPurgeSkipsEmptyBanksAndDeletesDiscardedEvidence(t *testing.T) {
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
	type bank struct {
		preparationID identity.ID
		frameID       identity.ID
		key           string
	}
	var emptyBanks []bank
	for range 2 {
		emptyBanks = append(emptyBanks, bank{preparationID: identity.NewID()})
	}
	purgeable := bank{preparationID: identity.NewID(), frameID: identity.NewID(), key: "inactive-origin-evidence/" + identity.NewID().String() + ".jpg"}
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, purgeable.key, strings.NewReader("frame"), 5, "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Store.Delete(context.Background(), purgeable.key) })
	ids := make([]identity.ID, 0, 3)
	ids = append(ids, emptyBanks[0].preparationID, emptyBanks[1].preparationID, purgeable.preparationID)
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		for index, preparationID := range ids {
			mediaID, versionID, originID := identity.NewID(), identity.NewID(), identity.NewID()
			if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: identity.NewID(), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "inactive-origin-evidence/source-" + mediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("d", 64), SizeBytes: 5, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "inactive-origin-evidence-media-" + mediaID.String(), AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: strings.Repeat("d", 64), ManifestDigest: strings.Repeat("d", 64), PendingObjectKeys: []byte(`[]`), CreatedAt: now}).Error; err != nil {
				return err
			}
			supersededAt := now.AddDate(0, 0, -400)
			if err := tx.Create(&database.OriginVersion{ID: versionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: 1, Status: "SUPERSEDED", MediaKind: "VIDEO", VideoPreparationID: &preparationID, SupersededAt: &supersededAt, IdempotencyKey: "inactive-origin-evidence-version-" + versionID.String(), CreatedAt: supersededAt}).Error; err != nil {
				return err
			}
			if index != 2 {
				continue
			}
			frame := database.VideoFrame{ID: purgeable.frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: strings.Repeat("d", 64), ObjectKey: purgeable.key, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 5, CreatedAt: now}
			if err := tx.Create(&frame).Error; err != nil {
				return err
			}
			inventoryVersionID, environmentID, itemID := identity.NewID(), identity.NewID(), identity.NewID()
			purpose := "INVENTORY_UPDATE"
			staleAt := now.AddDate(0, 0, -60)
			if err := tx.Create(&database.OriginVersion{ID: inventoryVersionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: 2, Purpose: &purpose, HasInventory: true, Status: "DISCARDED", DiscardedAt: &staleAt, IdempotencyKey: "discarded-inactive-evidence-" + inventoryVersionID.String(), CreatedAt: staleAt}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.InventoryEnvironment{ID: environmentID, TenantID: fixture.TenantID, OriginVersionID: inventoryVersionID, IdentityID: identity.NewID(), TypeCode: "LIVING_ROOM", Name: "Sala", Decision: "ACCEPTED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.InventoryItem{ID: itemID, TenantID: fixture.TenantID, OriginVersionID: inventoryVersionID, EnvironmentID: environmentID, IdentityID: identity.NewID(), TypeCode: "SOFA", Name: "Sofá", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "ACCEPTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			frameID := purgeable.frameID
			return tx.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: fixture.TenantID, ItemID: itemID, Kind: "VIDEO_FRAME", MediaID: mediaID, FrameID: &frameID, CreatedAt: now}).Error
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	count, err := executor.PurgeInactiveOriginBanks(ctx, fixture.TenantID, 2)
	if err != nil || count != 2 {
		t.Fatalf("PurgeInactiveOriginBanks() = %d, %v; want one frame bank and one discarded inventory version", count, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var frames, evidence int64
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.TenantID, purgeable.preparationID).Count(&frames).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.InventoryItemEvidence{}).Where("tenant_id=? AND frame_id=?", fixture.TenantID, purgeable.frameID).Count(&evidence).Error; err != nil {
			return err
		}
		if frames != 0 || evidence != 0 {
			t.Errorf("discarded evidence bank retained rows: frames=%d evidence=%d; want 0/0", frames, evidence)
		}
		if reader, _, err := (objectstore.VideoStore{Store: h.Store}).Open(ctx, purgeable.key); reader != nil || err == nil {
			if reader != nil {
				_ = reader.Close()
			}
			t.Errorf("purged frame object remains: reader=%v err=%v", reader, err)
		}
		for _, empty := range emptyBanks {
			var rows int64
			if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.TenantID, empty.preparationID).Count(&rows).Error; err != nil {
				return err
			}
			if rows != 0 {
				t.Errorf("empty bank unexpectedly gained frames: %d", rows)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInactiveOriginPurgeKeepsBanksReferencedByDraftOrActiveInventory(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	for _, versionStatus := range []string{"DRAFT", "ACTIVE"} {
		t.Run(versionStatus, func(t *testing.T) {
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
			mediaID, preparationID, bankVersionID := identity.NewID(), identity.NewID(), identity.NewID()
			frameID, inventoryVersionID, environmentID, itemID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
			inventoryOriginID := identity.NewID()
			key := "inactive-origin-live-evidence/" + frameID.String() + ".jpg"
			if err := h.Store.Client.Put(ctx, h.Store.Bucket, key, strings.NewReader("frame"), 5, "image/jpeg"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = h.Store.Delete(context.Background(), key) })
			purpose := "INVENTORY_UPDATE"
			if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
				originID := identity.NewID()
				if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "inactive-origin-live-evidence/source-" + mediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("e", 64), SizeBytes: 5, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "inactive-origin-live-media-" + mediaID.String(), AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: strings.Repeat("e", 64), ManifestDigest: strings.Repeat("e", 64), PendingObjectKeys: []byte(`[]`), CreatedAt: now}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.OriginVersion{ID: bankVersionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: 1, Status: "SUPERSEDED", MediaKind: "VIDEO", VideoPreparationID: &preparationID, IdempotencyKey: "inactive-origin-live-bank-" + bankVersionID.String(), CreatedAt: now}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: strings.Repeat("e", 64), ObjectKey: key, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 5, CreatedAt: now}).Error; err != nil {
					return err
				}
				var activeVersionID *identity.ID
				if versionStatus == "ACTIVE" {
					activeVersionID = &inventoryVersionID
				}
				if err := tx.Create(&database.Origin{ID: inventoryOriginID, TenantID: fixture.TenantID, AssetID: identity.NewID(), ActiveVersionID: activeVersionID, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.OriginVersion{ID: inventoryVersionID, TenantID: fixture.TenantID, OriginID: inventoryOriginID, VersionNumber: 1, Purpose: &purpose, HasInventory: true, Status: versionStatus, IdempotencyKey: "inventory-live-frame-" + inventoryVersionID.String(), CreatedAt: now}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.InventoryEnvironment{ID: environmentID, TenantID: fixture.TenantID, OriginVersionID: inventoryVersionID, IdentityID: identity.NewID(), TypeCode: "LIVING_ROOM", Name: "Sala", Decision: "ACCEPTED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.InventoryItem{ID: itemID, TenantID: fixture.TenantID, OriginVersionID: inventoryVersionID, EnvironmentID: environmentID, IdentityID: identity.NewID(), TypeCode: "SOFA", Name: "Sofá", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "ACCEPTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
					return err
				}
				return tx.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: fixture.TenantID, ItemID: itemID, Kind: "VIDEO_FRAME", MediaID: mediaID, FrameID: &frameID, CreatedAt: now}).Error
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
				var frames, evidence int64
				if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND id=?", fixture.TenantID, frameID).Count(&frames).Error; err != nil {
					return err
				}
				if err := tx.Model(&database.InventoryItemEvidence{}).Where("tenant_id=? AND frame_id=?", fixture.TenantID, frameID).Count(&evidence).Error; err != nil {
					return err
				}
				if frames != 1 || evidence != 1 {
					t.Errorf("%s inventory evidence was lost: frames=%d evidence=%d", versionStatus, frames, evidence)
				}
				reader, _, err := (objectstore.VideoStore{Store: h.Store}).Open(ctx, key)
				if err != nil {
					return err
				}
				return reader.Close()
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
