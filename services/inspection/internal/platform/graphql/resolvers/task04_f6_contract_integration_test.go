//go:build integration

package resolvers

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestCreateVideoInspectionRejectsForeignAssetAndPhotoOrigins(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	t.Run("photo origin P0", func(t *testing.T) {
		fx := newFixedOriginGraphQLFixture(t, true)
		var photoVersion database.OriginVersion
		if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND media_kind='PHOTO'", fx.seed.TenantID).Take(&photoVersion).Error
		}); err != nil {
			t.Fatal(err)
		}
		response := executeFixedOriginEntryWithReference(t, fx, "createInspection", "VV-IT-078-PHOTO", photoVersion.ID.String())
		assertFixedOriginCatalogOriginFirst(t, response)
	})
	t.Run("origin from asset Y", func(t *testing.T) {
		fx := newFixedOriginGraphQLFixture(t, false)
		var versionID identity.ID
		err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
			var source database.Asset
			if err := tx.Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.AssetID).Take(&source).Error; err != nil {
				return err
			}
			now := time.Now().UTC()
			assetID, originID := identity.NewID(), identity.NewID()
			other := source
			other.ID, other.Name, other.ExternalKey, other.IdempotencyKey = assetID, "Asset Y", "asset-y-"+assetID.String(), "asset-y-"+assetID.String()
			if err := tx.Create(&other).Error; err != nil {
				return err
			}
			versionID = identity.NewID()
			if err := tx.Create(&database.Origin{ID: originID, TenantID: fx.seed.TenantID, AssetID: assetID, ActiveVersionID: &versionID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			return tx.Create(&database.OriginVersion{ID: versionID, TenantID: fx.seed.TenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: &fx.seed.ResponsibilityID, Status: "ACTIVE", MediaKind: "VIDEO", IdempotencyKey: "it078-asset-y-" + versionID.String(), CreatedAt: now, ActivatedAt: &now}).Error
		})
		if err != nil {
			t.Fatal(err)
		}
		response := executeFixedOriginEntryWithReference(t, fx, "createInspection", "VV-IT-078-ASSET-Y", versionID.String())
		assertFixedOriginInvalidInput(t, response)
	})
}

func seedSecondVideoOrigin(t *testing.T, fx fixedOriginGraphQLFixture, originOneID identity.ID) identity.ID {
	t.Helper()
	var o1 database.OriginVersion
	if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, originOneID).Take(&o1).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	responsibilityID, draftID, mediaID, prepID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	versionID, frameID := identity.NewID(), identity.NewID()
	digest := strings.Repeat("e", 64)
	err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.Responsibility{ID: responsibilityID, TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, ParticipantID: fx.seed.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.CaptureDraft{ID: draftID, TenantID: fx.seed.TenantID, ResponsibilityID: responsibilityID, Kind: "ORIGIN", MediaKind: "VIDEO", TemplateVersionID: fx.seed.TemplateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[]`), Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fx.seed.TenantID, ResponsibilityID: responsibilityID, ObjectKey: "it073-o2/" + mediaID.String(), ContentType: "video/mp4", SHA256: digest, SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "it073-o2-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: prepID, TenantID: fx.seed.TenantID, MediaID: mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		days := 30
		expiry := now.Add(30 * 24 * time.Hour)
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fx.seed.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &now, RetentionDays: &days, SourceExpiryAt: &expiry, Status: "RETAINED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fx.seed.TenantID, SourceMediaID: mediaID, PreparationID: prepID, PTSMicros: 0, SHA256: digest, ObjectKey: "it073-o2/frame/" + frameID.String(), Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 1, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fx.seed.TenantID, PreparationID: prepID, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.OriginVersion{ID: versionID, TenantID: fx.seed.TenantID, OriginID: o1.OriginID, VersionNumber: o1.VersionNumber + 1, ResponsibilityID: &responsibilityID, Status: "DRAFT", MediaKind: "VIDEO", VideoPreparationID: &prepID, ScopeManifestDigest: digest, IdempotencyKey: "it073-o2-" + versionID.String(), CreatedAt: now}).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	return responsibilityID
}
