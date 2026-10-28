//go:build integration

package purge_data

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

func TestInspectionPurgeReleasesSealedPlanAndDeletesUnpromotedFrameBankIT325(t *testing.T) {
	result := runVideoPurgeIntegration(t, false)
	if result.frameRows != 0 || result.objectExists {
		t.Fatalf("unpromoted frame bank after purge: rows=%d objectExists=%v; want 0/false", result.frameRows, result.objectExists)
	}
}

func TestInspectionPurgePreservesFrameBankPromotedToActiveOrigin(t *testing.T) {
	result := runVideoPurgeIntegration(t, true)
	if result.frameRows != 1 || !result.objectExists {
		t.Fatalf("promoted frame bank after purge: rows=%d objectExists=%v; want 1/true", result.frameRows, result.objectExists)
	}
}

func TestInspectionPurgePreservesPhotoEvidenceOfActiveInventoryAndDeletesDiscardedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name        string
		version     string
		wantPresent bool
	}{{name: "active baseline", version: "ACTIVE", wantPresent: true}, {name: "discarded draft", version: "DISCARDED", wantPresent: false}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h, err := harness.NewFromEnv(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = h.Close() })
			fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			originID, versionID, environmentID, itemID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
			purpose := "INVENTORY_UPDATE"
			if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
				if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, ActiveVersionID: func() *identity.ID {
					if tc.version == "ACTIVE" {
						return &versionID
					}
					return nil
				}(), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.OriginVersion{ID: versionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: 1, Purpose: &purpose, Source: "COMPARATIVE_INSPECTION", SourceInspectionID: &fixture.InspectionID, HasInventory: true, Status: tc.version, IdempotencyKey: "purge-photo-inventory-" + versionID.String(), CreatedAt: now, ActivatedAt: func() *time.Time {
					if tc.version == "ACTIVE" {
						return &now
					}
					return nil
				}()}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.InventoryEnvironment{ID: environmentID, TenantID: fixture.TenantID, OriginVersionID: versionID, IdentityID: identity.NewID(), TypeCode: "LIVING_ROOM", Name: "Sala", Decision: "ACCEPTED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.InventoryItem{ID: itemID, TenantID: fixture.TenantID, OriginVersionID: versionID, EnvironmentID: environmentID, IdentityID: identity.NewID(), TypeCode: "SOFA", Name: "Sofá", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "ACCEPTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
					return err
				}
				return tx.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: fixture.TenantID, ItemID: itemID, Kind: "PHOTO", MediaID: fixture.MediaID, CreatedAt: now}).Error
			}); err != nil {
				t.Fatal(err)
			}
			clock := retentioncore.Clock{Class: retentioncore.EvidenceReports, StartsAt: now.Add(-time.Hour), DueAt: now.Add(-time.Minute)}
			adminDB := h.MigrationDB
			if adminDB == nil {
				adminDB = h.AdminDB
			}
			if _, err := PurgeWithStore(ctx, adminDB, h.Store, fixture.TenantID, fixture.InspectionID, clock); err != nil {
				t.Fatalf("PurgeWithStore() error: %v", err)
			}
			reader, _, openErr := (objectstore.VideoStore{Store: h.Store}).Open(ctx, "tenant/"+fixture.TenantID.String()+"/media/"+fixture.MediaID.String())
			if reader != nil {
				_ = reader.Close()
			}
			derivativeReader, _, derivativeErr := (objectstore.VideoStore{Store: h.Store}).Open(ctx, "tenant/"+fixture.TenantID.String()+"/media/"+fixture.MediaID.String()+"/normalized")
			if derivativeReader != nil {
				_ = derivativeReader.Close()
			}
			var mediaRows, evidenceRows, derivativeRows int64
			if err := adminDB.Model(&database.MediaObject{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.MediaID).Count(&mediaRows).Error; err != nil {
				t.Fatal(err)
			}
			if err := adminDB.Model(&database.InventoryItemEvidence{}).Where("tenant_id=? AND item_id=?", fixture.TenantID, itemID).Count(&evidenceRows).Error; err != nil {
				t.Fatal(err)
			}
			if err := adminDB.Model(&database.MediaDerivative{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.DerivativeID).Count(&derivativeRows).Error; err != nil {
				t.Fatal(err)
			}
			if tc.wantPresent {
				if openErr != nil || mediaRows != 1 || evidenceRows != 1 || derivativeErr != nil || derivativeRows != 1 {
					t.Fatalf("active baseline photo evidence lost: objectErr=%v mediaRows=%d evidenceRows=%d derivativeErr=%v derivativeRows=%d", openErr, mediaRows, evidenceRows, derivativeErr, derivativeRows)
				}
			} else if openErr == nil || mediaRows != 0 || evidenceRows != 0 || derivativeErr == nil || derivativeRows != 0 {
				t.Fatalf("discarded photo evidence retained: objectErr=%v mediaRows=%d evidenceRows=%d derivativeErr=%v derivativeRows=%d", openErr, mediaRows, evidenceRows, derivativeErr, derivativeRows)
			}
		})
	}
}

func runVideoPurgeIntegration(t *testing.T, promote bool) struct {
	frameRows    int64
	objectExists bool
} {
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
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	mediaID, attemptID, preparationID, frameID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	planID, submissionID := identity.NewID(), identity.NewID()
	versionID, originID := identity.NewID(), identity.NewID()
	key := "retention-purge-video/" + frameID.String() + ".jpg"
	originalKey := "retention-purge-video/" + mediaID.String() + ".mp4"
	for _, item := range []struct{ key, contentType, data string }{{key, "image/jpeg", "frame"}, {originalKey, "video/mp4", "video"}} {
		if err := h.Store.Client.Put(ctx, h.Store.Bucket, item.key, strings.NewReader(item.data), int64(len(item.data)), item.contentType); err != nil {
			t.Fatal(err)
		}
		objectKey := item.key
		t.Cleanup(func() { _ = h.Store.Delete(context.Background(), objectKey) })
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	digest := strings.Repeat("c", 64)
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: fixture.DraftID, ResponsibilityID: fixture.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: []byte(`{}`), PayloadDigest: digest, SubmittedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "purge-video-attempt-" + mediaID.String(), PayloadDigest: digest, AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: originalKey, ContentType: "video/mp4", SHA256: digest, SizeBytes: 5, Status: "READY", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "purge-video-media-" + mediaID.String(), AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: digest, ObjectKey: key, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 5, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlan{ID: planID, TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, SubmissionID: submissionID, CurrentPreparationID: preparationID, Generation: 1, PlanDigest: digest, Status: "SEALED", NextAttemptAt: now, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoEvidenceReference{ID: identity.NewID(), TenantID: fixture.TenantID, PreparationID: preparationID, OwnerType: "VIDEO_PLAN", OwnerID: planID, CreatedAt: now}).Error; err != nil {
			return err
		}
		if promote {
			if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, ActiveVersionID: &versionID, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.OriginVersion{ID: versionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: &fixture.ResponsibilityID, Status: "ACTIVE", MediaKind: "VIDEO", VideoPreparationID: &preparationID, IdempotencyKey: "purge-video-origin-" + versionID.String(), CreatedAt: now, ActivatedAt: &now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.VideoEvidenceReference{ID: identity.NewID(), TenantID: fixture.TenantID, PreparationID: preparationID, OwnerType: "ORIGIN_VERSION", OwnerID: versionID, CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	clock := retentioncore.Clock{Class: retentioncore.EvidenceReports, StartsAt: now.Add(-time.Hour), DueAt: now.Add(-time.Minute)}
	adminDB := h.MigrationDB
	if adminDB == nil {
		adminDB = h.AdminDB
	}
	if adminDB == nil {
		t.Fatal("integration harness administrative database is required for retention purge")
	}
	if _, err := PurgeWithStore(ctx, adminDB, h.Store, fixture.TenantID, fixture.InspectionID, clock); err != nil {
		t.Fatalf("PurgeWithStore() error: %v", err)
	}
	var frameRows int64
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.TenantID, preparationID).Count(&frameRows).Error
	}); err != nil {
		t.Fatal(err)
	}
	objectExists := true
	reader, _, openErr := (objectstore.VideoStore{Store: h.Store}).Open(ctx, key)
	if openErr != nil {
		objectExists = false
	} else {
		_ = reader.Close()
	}
	return struct {
		frameRows    int64
		objectExists bool
	}{frameRows: frameRows, objectExists: objectExists}
}
