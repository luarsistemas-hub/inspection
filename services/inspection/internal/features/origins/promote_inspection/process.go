package promote_inspection

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"inspection/libs/identity"
	onboardingcatalog "inspection/services/inspection/internal/features/onboarding/real_estate_catalog"
	templatecatalog "inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Process resumes a persisted promotion manifest. It is safe to invoke again
// after a worker or object-store failure; copied object keys are retained in
// the manifest before the next copy begins.
func Process(ctx context.Context, db *gorm.DB, store objectstore.Store, tenantID, promotionID identity.ID) error {
	if db == nil || store.Client == nil || tenantID == (identity.ID{}) || promotionID == (identity.ID{}) {
		return fmt.Errorf("origin promotion: missing dependency")
	}
	var promotion database.OriginPromotion
	if err := (tenanttx.Runner{DB: db}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, promotionID).First(&promotion).Error; err != nil {
			return err
		}
		if promotion.Status == StatusActive {
			return nil
		}
		now := time.Now().UTC()
		return tx.Model(&promotion).Updates(map[string]any{"status": StatusProcessing, "failure_reason": "", "started_at": now, "updated_at": now}).Error
	}); err != nil {
		return err
	}

	var m manifest
	if err := json.Unmarshal(promotion.Manifest, &m); err != nil || len(m.Items) == 0 {
		return fail(ctx, db, tenantID, promotion, "invalid promotion manifest")
	}
	for index := range m.Items {
		if err := copyItem(ctx, db, store, tenantID, promotion.ID, &m, index); err != nil {
			return fail(ctx, db, tenantID, promotion, "copy failed")
		}
	}
	return finalize(ctx, db, tenantID, promotion.ID, m)
}

func copyItem(ctx context.Context, db *gorm.DB, store objectstore.Store, tenantID, promotionID identity.ID, m *manifest, index int) error {
	item := &m.Items[index]
	var source database.MediaObject
	var display database.MediaDerivative
	if err := (tenanttx.Runner{DB: db}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=? AND status='READY'", tenantID, item.SourceID).First(&source).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND media_id=? AND kind='DISPLAY'", tenantID, source.ID).First(&display).Error
	}); err != nil {
		return err
	}
	if item.OriginalKey == "" {
		data, err := store.Read(ctx, source.ObjectKey)
		if err != nil {
			return err
		}
		key, hash, err := store.PutOriginal(ctx, tenantID, item.TargetID, source.ContentType, data)
		if err != nil {
			return err
		}
		item.OriginalKey, item.OriginalHash = key, hash
		if err := persistManifest(ctx, db, tenantID, promotionID, *m); err != nil {
			return err
		}
	}
	if item.DisplayKey == "" {
		data, err := store.Read(ctx, display.ObjectKey)
		if err != nil {
			return err
		}
		key, hash, err := store.PutDerivative(ctx, tenantID, item.TargetID, "DISPLAY", "image/jpeg", data)
		if err != nil {
			return err
		}
		item.DisplayKey, item.DisplayHash, item.DisplayContentType = key, hash, "image/jpeg"
		if err := persistManifest(ctx, db, tenantID, promotionID, *m); err != nil {
			return err
		}
	}
	return nil
}

func persistManifest(ctx context.Context, db *gorm.DB, tenantID, promotionID identity.ID, m manifest) error {
	encoded, _ := json.Marshal(m)
	return (tenanttx.Runner{DB: db}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.OriginPromotion{}).Where("tenant_id=? AND id=?", tenantID, promotionID).Updates(map[string]any{"manifest": encoded, "updated_at": time.Now().UTC()}).Error
	})
}

func finalize(ctx context.Context, db *gorm.DB, tenantID, promotionID identity.ID, m manifest) error {
	return (tenanttx.Runner{DB: db}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		var promotion database.OriginPromotion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, promotionID).First(&promotion).Error; err != nil {
			return err
		}
		if promotion.Status == StatusActive {
			return nil
		}
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", tenantID, promotion.InspectionID).First(&inspection).Error; err != nil {
			return failTx(tx, tenantID, promotion, "inspection unavailable")
		}
		var asset database.Asset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, promotion.AssetID).First(&asset).Error; err != nil {
			return failTx(tx, tenantID, promotion, "asset unavailable")
		}
		if asset.Version != promotion.ExpectedAssetVersion {
			return pendingTx(tx, tenantID, promotion, "asset changed; review the promotion before activation")
		}
		template, err := comparativeTemplate(tx, tenantID, inspection, promotion.RequestedBy)
		if err != nil {
			return failTx(tx, tenantID, promotion, "comparative template unavailable")
		}
		var origin database.Origin
		err = tx.Where("tenant_id=? AND asset_id=? AND template_id=?", tenantID, asset.ID, template.ID).First(&origin).Error
		now := time.Now().UTC()
		if err == gorm.ErrRecordNotFound {
			origin = database.Origin{ID: identity.NewID(), TenantID: tenantID, AssetID: asset.ID, TemplateID: template.ID, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&origin).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND origin_id=?", tenantID, origin.ID).Count(&count).Error; err != nil {
			return err
		}
		responsibilityID, versionID := identity.NewID(), identity.NewID()
		version := database.OriginVersion{ID: versionID, TenantID: tenantID, OriginID: origin.ID, VersionNumber: int(count) + 1, ResponsibilityID: responsibilityID, SupersedesID: origin.ActiveVersionID, Status: "ACTIVE", IdempotencyKey: "promotion:" + promotion.ID.String(), CreatedAt: now, SubmittedAt: &now, ActivatedAt: &now}
		if err := tx.Create(&version).Error; err != nil {
			return err
		}
		for _, item := range m.Items {
			var source database.MediaObject
			if err := tx.Where("tenant_id=? AND id=?", tenantID, item.SourceID).First(&source).Error; err != nil {
				return err
			}
			media := database.MediaObject{ID: item.TargetID, TenantID: tenantID, ResponsibilityID: responsibilityID, ObjectKey: item.OriginalKey, ContentType: source.ContentType, SHA256: item.OriginalHash, SizeBytes: source.SizeBytes, Status: "READY", IdempotencyKey: "promotion:" + promotion.ID.String() + ":" + item.TargetID.String(), RequirementKey: "origin:" + item.TargetID.String(), Description: source.Description, CaptureSource: source.CaptureSource, CapturedAt: source.CapturedAt, DeviceContext: source.DeviceContext, LatitudeE6: source.LatitudeE6, LongitudeE6: source.LongitudeE6, AccuracyMM: source.AccuracyMM, DistanceMM: source.DistanceMM, Flags: source.Flags, CreatedAt: now}
			if err := tx.Create(&media).Error; err != nil {
				return err
			}
			derivative := database.MediaDerivative{ID: identity.NewID(), TenantID: tenantID, MediaID: item.TargetID, ObjectKey: item.DisplayKey, Kind: "DISPLAY", SHA256: item.DisplayHash, CreatedAt: now}
			if err := tx.Create(&derivative).Error; err != nil {
				return err
			}
			evidence := database.OriginEvidence{ID: identity.NewID(), TenantID: tenantID, OriginVersionID: versionID, MediaID: item.TargetID, Category: "property", Description: source.Description, CreatedAt: now}
			if err := tx.Create(&evidence).Error; err != nil {
				return err
			}
		}
		if origin.ActiveVersionID != nil {
			if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", tenantID, *origin.ActiveVersionID).Update("status", "SUPERSEDED").Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&origin).Updates(map[string]any{"active_version_id": versionID, "version": origin.Version + 1, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&asset).Where("version=?", promotion.ExpectedAssetVersion).Updates(map[string]any{"template_id": template.ID, "version": promotion.ExpectedAssetVersion + 1, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&promotion).Updates(map[string]any{"template_id": template.ID, "origin_version_id": versionID, "status": StatusActive, "completed_at": now, "updated_at": now, "failure_reason": ""}).Error; err != nil {
			return err
		}
		return audit(tx, tenantID, promotion.RequestedBy, "origin.promotion_activated", promotion.ID, "SUCCESS", "")
	})
}

func comparativeTemplate(tx *gorm.DB, tenantID identity.ID, inspection database.Inspection, actor identity.ID) (database.Template, error) {
	var fixed database.Template
	if err := tx.Where("tenant_id=? AND key=?", tenantID, onboardingcatalog.OriginTemplateKey).First(&fixed).Error; err == nil && fixed.ActiveVersionID != nil {
		return fixed, nil
	}
	var sourceVersion database.TemplateVersion
	if err := tx.Where("tenant_id=? AND id=?", tenantID, inspection.TemplateVersionID).First(&sourceVersion).Error; err != nil {
		return database.Template{}, err
	}
	var document templatecatalog.TemplateDocument
	if err := json.Unmarshal(sourceVersion.DefinitionJSON, &document); err != nil {
		return database.Template{}, err
	}
	document.ComparisonMode = templatecatalog.FixedOrigin
	for index := range document.Requirements {
		document.Requirements[index].ComparisonTarget = templatecatalog.FixedOrigin
	}
	payload, digest, err := templatecatalog.CanonicalJSON(document)
	if err != nil {
		return database.Template{}, err
	}
	now := time.Now().UTC()
	if fixed.ID == (identity.ID{}) {
		fixed = database.Template{ID: identity.NewID(), TenantID: tenantID, Key: onboardingcatalog.OriginTemplateKey, Name: "Vistoria comparativa do imóvel", SegmentVersionID: inspection.TemplateVersionID, Version: 1, CreatedAt: now, UpdatedAt: now}
		var source database.Template
		if err := tx.Where("tenant_id=? AND id=?", tenantID, inspection.TemplateID).First(&source).Error; err != nil {
			return database.Template{}, err
		}
		fixed.SegmentVersionID = source.SegmentVersionID
		if err := tx.Create(&fixed).Error; err != nil {
			return database.Template{}, err
		}
	}
	version := database.TemplateVersion{ID: identity.NewID(), TenantID: tenantID, TemplateID: fixed.ID, VersionNumber: 1, SchemaVersion: document.SchemaVersion, DefinitionJSON: payload, CanonicalDigest: digest, Status: "ACTIVE", IdempotencyKey: "origin-promotion-template:" + fixed.ID.String(), PublishedAt: now, CreatedBy: actor}
	if err := tx.Create(&version).Error; err != nil {
		return database.Template{}, err
	}
	if err := tx.Model(&fixed).Updates(map[string]any{"active_version_id": version.ID, "updated_at": now}).Error; err != nil {
		return database.Template{}, err
	}
	fixed.ActiveVersionID = &version.ID
	return fixed, nil
}

func fail(ctx context.Context, db *gorm.DB, tenantID identity.ID, promotion database.OriginPromotion, reason string) error {
	return (tenanttx.Runner{DB: db}).Within(ctx, tenantID, func(tx *gorm.DB) error { return failTx(tx, tenantID, promotion, reason) })
}

func failTx(tx *gorm.DB, tenantID identity.ID, promotion database.OriginPromotion, reason string) error {
	now := time.Now().UTC()
	if err := tx.Model(&database.OriginPromotion{}).Where("tenant_id=? AND id=?", tenantID, promotion.ID).Updates(map[string]any{"status": StatusFailed, "failure_reason": reason, "updated_at": now}).Error; err != nil {
		return err
	}
	return audit(tx, tenantID, promotion.RequestedBy, "origin.promotion_failed", promotion.ID, "FAILED", reason)
}

func pendingTx(tx *gorm.DB, tenantID identity.ID, promotion database.OriginPromotion, reason string) error {
	now := time.Now().UTC()
	if err := tx.Model(&database.OriginPromotion{}).Where("tenant_id=? AND id=?", tenantID, promotion.ID).Updates(map[string]any{"status": StatusPending, "failure_reason": reason, "updated_at": now}).Error; err != nil {
		return err
	}
	return audit(tx, tenantID, promotion.RequestedBy, "origin.promotion_pending_review", promotion.ID, "PENDING", reason)
}
