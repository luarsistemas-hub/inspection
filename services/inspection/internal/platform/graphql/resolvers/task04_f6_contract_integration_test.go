//go:build integration

package resolvers

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	originlist "inspection/services/inspection/internal/features/origins/list_versions"
	"inspection/services/inspection/internal/platform/auth"
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

func TestVV_IT_340FixedOriginAllowsMixedModalityInventory(t *testing.T) {
	fx := newFixedOriginGraphQLFixture(t, true)
	var photoVersion database.OriginVersion
	var origin database.Origin
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND asset_id=?", fx.seed.TenantID, fx.seed.AssetID).First(&origin).Error; err != nil {
			return err
		}
		versionNumber := 1
		if origin.ActiveVersionID != nil {
			if err := tx.Where("tenant_id=? AND id=?", fx.seed.TenantID, *origin.ActiveVersionID).Take(&photoVersion).Error; err != nil {
				return err
			}
			versionNumber = photoVersion.VersionNumber + 1
			if err := tx.Model(&photoVersion).Update("status", "SUPERSEDED").Error; err != nil {
				return err
			}
		}
		purpose, activatedAt := "MOVE_IN", time.Now().UTC()
		photoVersion = database.OriginVersion{ID: identity.NewID(), TenantID: fx.seed.TenantID, OriginID: origin.ID, VersionNumber: versionNumber, Purpose: &purpose, Status: "ACTIVE", MediaKind: "PHOTO", HasInventory: true, IdempotencyKey: "vv-it-340-catalog-photo", CreatedAt: activatedAt, ActivatedAt: &activatedAt}
		if err := tx.Create(&photoVersion).Error; err != nil {
			return err
		}
		if err := tx.Model(&origin).Updates(map[string]any{"active_version_id": photoVersion.ID, "version": origin.Version + 1, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		environmentID, environmentIdentityID := identity.NewID(), identity.NewID()
		return tx.Create(&database.InventoryEnvironment{ID: environmentID, TenantID: fx.seed.TenantID, OriginVersionID: photoVersion.ID, IdentityID: environmentIdentityID, TypeCode: "LIVING_ROOM", Name: "Sala", Expected: true, Decision: "ACCEPTED", Position: 0, Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error
	}); err != nil {
		t.Fatal(err)
	}
	legacyOriginID := origin.ID
	response := executeFixedOriginEntryWithReference(t, fx, "createInspection", "VV-IT-340-MIXED-MODALITY", photoVersion.ID.String())
	if strings.Contains(response, `"errors"`) || strings.Contains(response, `"code":"INVALID_INPUT"`) {
		t.Fatalf("inventory baseline with a different modality must be pinnable: %s", response)
	}
	legacyVersionID := identity.NewID()
	now := time.Now().UTC()
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", fx.seed.TenantID, legacyOriginID).Take(&origin).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, photoVersion.ID).Update("status", "SUPERSEDED").Error; err != nil {
			return err
		}
		if err := tx.Create(&database.OriginVersion{ID: legacyVersionID, TenantID: fx.seed.TenantID, OriginID: legacyOriginID, VersionNumber: photoVersion.VersionNumber + 1, Status: "ACTIVE", MediaKind: "PHOTO", IdempotencyKey: "vv-it-340-legacy-photo", CreatedAt: now, ActivatedAt: &now}).Error; err != nil {
			return err
		}
		return tx.Model(&origin).Updates(map[string]any{"active_version_id": legacyVersionID, "version": origin.Version + 1, "updated_at": now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	legacy := executeFixedOriginEntryWithReference(t, fx, "createInspection", "VV-IT-340-LEGACY-MODALITY", legacyVersionID.String())
	if !strings.Contains(legacy, "CATALOG_ORIGIN_FIRST") {
		t.Fatalf("legacy origin with a different modality must require catalog origin first: %s", legacy)
	}
}

func TestVV_IT_073HundredInspectionsRemainPinnedToOriginalVideoFramesAfterActivation(t *testing.T) {
	fx := newFixedOriginGraphQLFixture(t, false)
	if err := originlist.Setup(originlist.Dependencies{DB: fx.h.DB, Bus: fx.bus, Authorizer: auth.Authorizer{}, Within: fx.h.WithinTenant}); err != nil {
		t.Fatal(err)
	}
	originOneID := seedFixedVideoOrigin(t, fx)
	inspectionIDs := make([]identity.ID, 0, 100)
	for i := 0; i < 100; i++ {
		clientID := fmt.Sprintf("VV-IT-073-%03d", i)
		response := executeFixedOriginEntryWithReference(t, fx, "createInspection", clientID, originOneID.String())
		var payload struct {
			Data struct {
				Create struct {
					Inspection struct {
						ID string `json:"id"`
					} `json:"inspection"`
				} `json:"createInspection"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(response), &payload); err != nil || payload.Data.Create.Inspection.ID == "" {
			t.Fatalf("createInspection %d failed: response=%s decode=%v", i+1, response, err)
		}
		id, err := identity.ParseID(payload.Data.Create.Inspection.ID)
		if err != nil {
			t.Fatal(err)
		}
		inspectionIDs = append(inspectionIDs, id)
	}
	responsibilityID := seedSecondVideoOrigin(t, fx)
	var media database.MediaObject
	if err := fx.h.AdminDB.Where("tenant_id=? AND responsibility_id=?", fx.seed.TenantID, responsibilityID).Take(&media).Error; err != nil {
		t.Fatal(err)
	}
	var preparation database.VideoPreparationJob
	if err := fx.h.AdminDB.Where("tenant_id=? AND media_id=?", fx.seed.TenantID, media.ID).Take(&preparation).Error; err != nil {
		t.Fatal(err)
	}
	planID := identity.NewID()
	var submissionID identity.ID
	now := time.Now().UTC()
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		var submission database.SubmissionVersion
		if err := tx.Where("tenant_id=? AND draft_id=?", fx.seed.TenantID, fx.seed.DraftID).Order("version_number DESC").Take(&submission).Error; err != nil {
			submission = database.SubmissionVersion{ID: identity.NewID(), TenantID: fx.seed.TenantID, DraftID: fx.seed.DraftID, ResponsibilityID: fx.seed.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: []byte(`{}`), PayloadDigest: "vv-it-073-submission", SubmittedAt: now}
			if createErr := tx.Create(&submission).Error; createErr != nil {
				return createErr
			}
		}
		submissionID = submission.ID
		plan := database.VideoPlan{ID: planID, TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, SubmissionID: submissionID, CurrentPreparationID: preparation.ID, Generation: 1, PlanDigest: strings.Repeat("f", 64), Status: "SEALED", NextAttemptAt: now, SealedAt: &now, CreatedAt: now}
		if err := tx.Create(&plan).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlanPreparation{TenantID: fx.seed.TenantID, PlanID: planID, PreparationID: preparation.ID, Role: "MAIN", CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.InspectionID).Updates(map[string]any{"status": "COMPLETED", "media_kind": "VIDEO", "active_video_plan_id": planID}).Error
	}); err != nil {
		t.Fatal(err)
	}
	promotion := executeTask06GraphQL(t, fx.server, `mutation { promoteInspectionVideo(input:{inspectionId:"`+fx.seed.InspectionID.String()+`",clientMutationId:"vv-it-073-promote-o2"}) { version { id status } userErrors { code } } }`, fx.ctx)
	if strings.Contains(promotion, `"errors"`) || !strings.Contains(promotion, `"status":"ACTIVE"`) {
		t.Fatalf("O2 promotion through the active workflow failed: %s", promotion)
	}
	var originTwo database.OriginVersion
	if err := fx.h.AdminDB.Where("tenant_id=? AND responsibility_id=? AND status='ACTIVE'", fx.seed.TenantID, responsibilityID).Take(&originTwo).Error; err != nil {
		t.Fatal(err)
	}
	var origin database.Origin
	if err := fx.h.AdminDB.Where("tenant_id=? AND asset_id=?", fx.seed.TenantID, fx.seed.AssetID).Take(&origin).Error; err != nil || origin.ActiveVersionID == nil || *origin.ActiveVersionID != originTwo.ID {
		t.Fatalf("promotion did not advance active origin: origin=%+v O2=%s err=%v", origin, originTwo.ID, err)
	}
	for _, inspectionID := range inspectionIDs {
		var snapshot database.ReferenceSnapshot
		if err := fx.h.AdminDB.Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, inspectionID).Take(&snapshot).Error; err != nil {
			t.Fatal(err)
		}
		if snapshot.ReferenceVersionID == nil || *snapshot.ReferenceVersionID != originOneID {
			t.Fatalf("inspection %s changed its pinned origin: got %v, want O1 %s", inspectionID, snapshot.ReferenceVersionID, originOneID)
		}
	}
}

func seedSecondVideoOrigin(t *testing.T, fx fixedOriginGraphQLFixture) identity.ID {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	responsibilityID, draftID, mediaID, prepID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	frameID := identity.NewID()
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
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return responsibilityID
}
