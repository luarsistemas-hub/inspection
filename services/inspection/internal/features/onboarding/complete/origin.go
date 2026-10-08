package complete

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// originMediaStatus checks the saved selection against the private media
// records. VERIFIED images may still be passing through the screening worker.
func (s Service) originMediaStatus(ctx context.Context, tenantID, sessionID identity.ID, ids []identity.ID) (string, error) {
	var media []database.MediaObject
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND responsibility_id=? AND id IN ?", tenantID, sessionID, ids).Find(&media).Error
	})
	if err != nil {
		return "", err
	}
	if len(media) != len(ids) {
		return "", apperror.New(apperror.InvalidState, "referencePhotos", "reference photos were not uploaded")
	}
	status := "ACTIVE"
	for _, photo := range media {
		if photo.RequirementKey != "reference" || strings.TrimSpace(photo.Description) == "" {
			return "", apperror.New(apperror.InvalidState, "referencePhotos", "reference photo is incomplete")
		}
		switch photo.Status {
		case "READY":
		case "VERIFIED":
			status = "PENDING"
		default:
			return "", apperror.New(apperror.InvalidState, "referencePhotos", "reference photo could not be approved")
		}
	}
	return status, nil
}

// ensureOrigin attaches the already screened photos to the first asset. The
// session ID identifies both the provisional media responsibility and retry.
func (s Service) ensureOrigin(ctx context.Context, tenantID, sessionID, assetID, templateVersionID identity.ID, ids []identity.ID) (identity.ID, error) {
	var versionID identity.ID
	now := s.now()
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var existing database.OriginVersion
		key := "onboarding:origin:" + sessionID.String()
		if err := tx.Where("tenant_id=? AND idempotency_key=?", tenantID, key).First(&existing).Error; err == nil {
			// A retry of an older session may find its version already superseded
			// by a newer onboarding for the same asset; it still belongs to that session.
			if (existing.Status != "ACTIVE" && existing.Status != "SUPERSEDED") || existing.ResponsibilityID != sessionID {
				return apperror.New(apperror.InvalidState, "referencePhotos", "origin is unavailable")
			}
			versionID = existing.ID
			return nil
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		var media []database.MediaObject
		if err := tx.Where("tenant_id=? AND responsibility_id=? AND id IN ? AND status='READY'", tenantID, sessionID, ids).Find(&media).Error; err != nil {
			return err
		}
		if len(media) != len(ids) {
			return apperror.New(apperror.InvalidState, "referencePhotos", "reference photos are not ready")
		}
		// An asset has at most one origin. When a new onboarding reuses an asset
		// that already has one, its photos become a new version of that origin.
		if err := lockOriginAsset(tx, tenantID, assetID); err != nil {
			return err
		}
		var origin database.Origin
		err := tx.Where("tenant_id=? AND asset_id=?", tenantID, assetID).First(&origin).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			origin = database.Origin{ID: identity.NewID(), TenantID: tenantID, AssetID: assetID, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&origin).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		var supersedes *identity.ID
		if origin.ActiveVersionID != nil {
			supersedes = origin.ActiveVersionID
			if err := tx.Model(&database.OriginVersion{}).
				Where("tenant_id=? AND id=? AND status='ACTIVE'", tenantID, *origin.ActiveVersionID).
				Updates(map[string]any{"status": "SUPERSEDED"}).Error; err != nil {
				return err
			}
		}
		var lastVersion int
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND origin_id=?", tenantID, origin.ID).
			Select("COALESCE(MAX(version_number), 0)").Scan(&lastVersion).Error; err != nil {
			return err
		}
		version := database.OriginVersion{ID: identity.NewID(), TenantID: tenantID, OriginID: origin.ID, VersionNumber: lastVersion + 1, ResponsibilityID: sessionID, SupersedesID: supersedes, Status: "ACTIVE", IdempotencyKey: key, CreatedAt: now, SubmittedAt: &now, ActivatedAt: &now}
		if err := tx.Create(&version).Error; err != nil {
			return err
		}
		if err := tx.Model(&origin).Updates(map[string]any{"active_version_id": version.ID, "version": origin.Version + 1, "updated_at": now}).Error; err != nil {
			return err
		}
		draft := database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: sessionID, Kind: "ORIGIN", TemplateVersionID: templateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[]`), Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&draft).Error; err != nil {
			return err
		}
		for _, photo := range media {
			evidence := database.OriginEvidence{ID: identity.NewID(), TenantID: tenantID, OriginVersionID: version.ID, MediaID: photo.ID, Category: "Referência", Description: photo.Description, AttentionItems: photo.AttentionItems, CreatedAt: now}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&evidence).Error; err != nil {
				return err
			}
		}
		versionID = version.ID
		return nil
	})
	return versionID, err
}

// lockOriginAsset serializes origin changes for one asset across API instances,
// so concurrent onboardings for the same address cannot both activate a version.
func lockOriginAsset(tx *gorm.DB, tenantID, assetID identity.ID) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	return tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "origin:"+tenantID.String()+":"+assetID.String()).Error
}
