package core

import (
	"context"
	"errors"
	"time"

	"inspection/libs/identity"
	resolveprompt "inspection/services/inspection/internal/features/analysis/resolve_prompt"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CreateDraft creates an inventory draft inside the caller's transaction.
func (s Service) CreateDraft(tx *gorm.DB, in DraftInput) (database.OriginVersion, error) {
	if tx == nil || in.TenantID == (identity.ID{}) || in.AssetID == (identity.ID{}) {
		return database.OriginVersion{}, apperror.New(apperror.InvalidInput, "assetId", "asset is required")
	}
	if in.Source == "" {
		in.Source = "CAPTURE"
	}
	if in.Source != "CAPTURE" && in.Source != "LEGACY_ORIGIN" && in.Source != "COMPARATIVE_INSPECTION" && in.Source != "CONTESTATION" {
		return database.OriginVersion{}, apperror.New(apperror.InvalidInput, "source", "unknown draft source")
	}
	now := s.now()
	var origin database.Origin
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND asset_id=?", in.TenantID, in.AssetID).First(&origin).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		origin = database.Origin{ID: identity.NewID(), TenantID: in.TenantID, AssetID: in.AssetID, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err = tx.Create(&origin).Error; err != nil {
			return database.OriginVersion{}, err
		}
	} else if err != nil {
		return database.OriginVersion{}, err
	}
	var existing database.OriginVersion
	if err := tx.Where("tenant_id=? AND origin_id=? AND has_inventory AND status='DRAFT'", in.TenantID, origin.ID).First(&existing).Error; err == nil {
		return database.OriginVersion{}, apperror.New(apperror.Conflict, "originVersionId", existing.ID.String())
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return database.OriginVersion{}, err
	}
	if in.Source == "LEGACY_ORIGIN" {
		if origin.ActiveVersionID == nil {
			return database.OriginVersion{}, apperror.New(apperror.InvalidState, "assetId", "origin has no active version")
		}
		var active database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", in.TenantID, *origin.ActiveVersionID).First(&active).Error; err != nil {
			return database.OriginVersion{}, err
		}
		if active.HasInventory {
			return database.OriginVersion{}, apperror.New(apperror.InvalidState, "assetId", "active version already has inventory")
		}
		in.SourceVersionID = active.ID
	}
	if in.Source == "COMPARATIVE_INSPECTION" || in.Source == "CONTESTATION" {
		if in.SourceVersionID == (identity.ID{}) {
			return database.OriginVersion{}, apperror.New(apperror.InvalidInput, "sourceVersionId", "source version is required")
		}
	}
	var latestVersion int64
	if err := tx.Model(&database.OriginVersion{}).Select("COALESCE(MAX(version_number), 0)").Where("tenant_id=? AND origin_id=?", in.TenantID, origin.ID).Scan(&latestVersion).Error; err != nil {
		return database.OriginVersion{}, err
	}
	draftState := "PREPARING"
	if in.Source == "COMPARATIVE_INSPECTION" || in.Source == "CONTESTATION" {
		draftState = "READY"
	}
	version := database.OriginVersion{ID: identity.NewID(), TenantID: in.TenantID, OriginID: origin.ID, VersionNumber: int(latestVersion) + 1, Source: in.Source, HasInventory: true, Status: "DRAFT", DraftState: &draftState, IdempotencyKey: "inventory-" + identity.NewID().String(), CreatedAt: now}
	if in.SourceInspectionID != (identity.ID{}) {
		version.SourceInspectionID = &in.SourceInspectionID
	}
	if in.SourceVersionID != (identity.ID{}) {
		version.SourceVersionID = &in.SourceVersionID
	}
	if err := tx.Create(&version).Error; err != nil {
		return database.OriginVersion{}, err
	}
	if in.Source == "COMPARATIVE_INSPECTION" || in.Source == "CONTESTATION" {
		if err := s.copyIncluded(tx, in.TenantID, in.SourceVersionID, version.ID, now); err != nil {
			return database.OriginVersion{}, err
		}
	}
	return version, nil
}

// RequestOriginCataloging opens an on-demand inventory draft from a legacy origin.
func (s Service) RequestOriginCataloging(ctx context.Context, tenantID, assetID, actor identity.ID) (database.OriginVersion, error) {
	var result database.OriginVersion
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var asset database.Asset
		if err := tx.Where("tenant_id=? AND id=?", tenantID, assetID).First(&asset).Error; err != nil {
			return apperror.New(apperror.NotFound, "assetId", "asset not found")
		}
		var origin database.Origin
		if err := tx.Where("tenant_id=? AND asset_id=?", tenantID, assetID).First(&origin).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.InvalidState, "assetId", "origin has no active version")
			}
			return err
		}
		var existing database.OriginVersion
		if err := tx.Where("tenant_id=? AND origin_id=? AND has_inventory AND status='DRAFT'", tenantID, origin.ID).First(&existing).Error; err == nil {
			return apperror.New(apperror.Conflict, "originVersionId", existing.ID.String())
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if origin.ActiveVersionID == nil {
			return apperror.New(apperror.InvalidState, "assetId", "origin has no active version")
		}
		var err error
		result, err = s.CreateDraft(tx, DraftInput{TenantID: tenantID, AssetID: assetID, Source: "LEGACY_ORIGIN", ActorMembershipID: actor})
		if err != nil {
			return err
		}
		return s.requestLegacyCatalogJob(ctx, tx, tenantID, &result)
	})
	return result, err
}

// legacyCatalogKey is the requirement key of the single CATALOG job that
// catalogs a legacy origin version.
const legacyCatalogKey = "legacy:origin"

// requestLegacyCatalogJob schedules the CATALOG job over the evidence of the
// legacy version. The job is keyed to the inspection that captured the legacy
// origin; without evidence or such an inspection there is nothing to catalog
// and the empty draft is immediately reviewable.
func (s Service) requestLegacyCatalogJob(ctx context.Context, tx *gorm.DB, tenantID identity.ID, draft *database.OriginVersion) error {
	now := s.now()
	var legacy database.OriginVersion
	if err := tx.Where("tenant_id=? AND id=?", tenantID, *draft.SourceVersionID).Take(&legacy).Error; err != nil {
		return err
	}
	var inspectionID identity.ID
	if legacy.ResponsibilityID != nil {
		var responsibility database.Responsibility
		if err := tx.Where("tenant_id=? AND id=?", tenantID, *legacy.ResponsibilityID).Take(&responsibility).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		inspectionID = responsibility.InspectionID
	}
	var evidence int64
	if legacy.MediaKind == "VIDEO" {
		if legacy.VideoPreparationID != nil {
			if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=? AND representative=true", tenantID, *legacy.VideoPreparationID).Count(&evidence).Error; err != nil {
				return err
			}
		}
	} else if err := tx.Model(&database.OriginEvidence{}).Where("tenant_id=? AND origin_version_id=?", tenantID, legacy.ID).Count(&evidence).Error; err != nil {
		return err
	}
	if inspectionID == (identity.ID{}) || evidence == 0 {
		ready := "READY"
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=? AND draft_state='PREPARING'", tenantID, draft.ID).Update("draft_state", ready).Error; err != nil {
			return err
		}
		draft.DraftState = &ready
		return EmitDraftReady(tx, tenantID, draft.ID, identity.ID{}, now)
	}
	var inspection database.Inspection
	if err := tx.Where("tenant_id=? AND id=?", tenantID, inspectionID).Take(&inspection).Error; err != nil {
		return err
	}
	var analysis database.AnalysisPromptSnapshot
	if err := tx.Where("id=?", inspection.AnalysisPromptSnapshotID).Take(&analysis).Error; err != nil {
		return err
	}
	catalogPrompt, err := resolveprompt.ResolveTask(ctx, tx, analysis.AnalysisType, "INVENTORY_CATALOG", now)
	if err != nil {
		return err
	}
	snapshotID := catalogPrompt.Snapshot.ID
	// A discarded earlier draft of the same legacy origin used this key; the job
	// is re-pointed at the new draft instead of violating uniqueness.
	var existing database.InventoryJob
	if err := tx.Where("tenant_id=? AND inspection_id=? AND kind='CATALOG' AND requirement_key=?", tenantID, inspectionID, legacyCatalogKey).Take(&existing).Error; err == nil {
		return tx.Model(&existing).Updates(map[string]any{"origin_version_id": draft.ID, "status": "PENDING", "attempts": 0, "next_attempt_at": now, "error_code": nil, "lease_token": nil, "lease_until": nil, "prompt_snapshot_id": snapshotID, "updated_at": now}).Error
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	job := database.InventoryJob{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, OriginVersionID: draft.ID, Kind: "CATALOG", RequirementKey: legacyCatalogKey, Generation: 1, Status: "PENDING", NextAttemptAt: now, PromptSnapshotID: &snapshotID, CreatedAt: now, UpdatedAt: now}
	return tx.Create(&job).Error
}

// RetryDraft resets failed inventory jobs and reopens their draft atomically.
func (s Service) RetryDraft(ctx context.Context, tenantID, versionID, actor identity.ID) (database.OriginVersion, error) {
	var result database.OriginVersion
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var version database.OriginVersion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=? AND has_inventory", tenantID, versionID).Take(&version).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.NotFound, "originVersionId", "draft not found")
			}
			return err
		}
		if version.Status != "DRAFT" || version.DraftState == nil || *version.DraftState != "FAILED" {
			return apperror.New(apperror.InvalidState, "originVersionId", "only failed inventory drafts can be retried")
		}
		now := s.now()
		jobs := tx.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND status='FAILED'", tenantID, versionID).Updates(map[string]any{"status": "PENDING", "attempts": 0, "next_attempt_at": now, "error_code": nil, "lease_token": nil, "lease_until": nil, "updated_at": now})
		if jobs.Error != nil {
			return jobs.Error
		}
		if jobs.RowsAffected == 0 {
			return apperror.New(apperror.InvalidState, "originVersionId", "draft has no failed jobs to retry")
		}
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=? AND draft_state='FAILED'", tenantID, versionID).Update("draft_state", "PREPARING").Error; err != nil {
			return err
		}
		state := "PREPARING"
		version.DraftState = &state
		result = version
		_ = actor // Authorization is enforced by the resolver before this operation.
		return nil
	})
	return result, err
}

// StartFromInspection starts a next-version draft pinned to a completed comparison.
func (s Service) StartFromInspection(ctx context.Context, tenantID, inspectionID, actor identity.ID) (database.OriginVersion, error) {
	var result database.OriginVersion
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", tenantID, inspectionID).First(&inspection).Error; err != nil {
			return apperror.New(apperror.NotFound, "inspectionId", "inspection not found")
		}
		if inspection.Status != "COMPLETED" {
			return apperror.New(apperror.InvalidState, "inspectionId", "inspection must be completed")
		}
		var snapshot database.ReferenceSnapshot
		if err := tx.Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).First(&snapshot).Error; err != nil || snapshot.ReferenceVersionID == nil {
			return apperror.New(apperror.InvalidState, "inspectionId", "inspection has no inventory reference")
		}
		var source database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=? AND has_inventory AND status IN ('ACTIVE','SUPERSEDED')", tenantID, *snapshot.ReferenceVersionID).First(&source).Error; err != nil {
			return apperror.New(apperror.InvalidState, "inspectionId", "inspection has no inventory reference")
		}
		var err error
		result, err = s.CreateDraft(tx, DraftInput{TenantID: tenantID, AssetID: inspection.AssetID, Source: "COMPARATIVE_INSPECTION", SourceInspectionID: inspection.ID, SourceVersionID: source.ID, ActorMembershipID: actor})
		if err != nil {
			return err
		}
		if err := s.importComparison(tx, tenantID, inspection.ID, result.ID); err != nil {
			return err
		}
		var active database.Origin
		if err := tx.Where("tenant_id=? AND asset_id=?", tenantID, inspection.AssetID).First(&active).Error; err == nil && active.ActiveVersionID != nil && *active.ActiveVersionID != source.ID {
			var newer database.OriginVersion
			if err := tx.Where("tenant_id=? AND id=?", tenantID, *active.ActiveVersionID).First(&newer).Error; err == nil && newer.VersionNumber > source.VersionNumber {
				result.SourceVersionID = &source.ID
			}
		}
		return nil
	})
	return result, err
}

func (s Service) copyIncluded(tx *gorm.DB, tenant, source, target identity.ID, now time.Time) error {
	var sourceVersion database.OriginVersion
	if err := tx.Where("tenant_id=? AND id=?", tenant, source).First(&sourceVersion).Error; err != nil {
		return apperror.New(apperror.NotFound, "sourceVersionId", "source inventory version not found")
	}
	if !sourceVersion.HasInventory {
		return apperror.New(apperror.InvalidState, "sourceVersionId", "source version has no inventory")
	}
	var envs []database.InventoryEnvironment
	if err := tx.Where("tenant_id=? AND origin_version_id=? AND decision<>'REJECTED'", tenant, source).Find(&envs).Error; err != nil {
		return err
	}
	mapping := map[identity.ID]identity.ID{}
	for _, env := range envs {
		copy := env
		copy.ID = identity.NewID()
		copy.OriginVersionID = target
		copy.Decision = "ACCEPTED"
		copy.Version = 1
		copy.CreatedAt = now
		copy.UpdatedAt = now
		if err := tx.Create(&copy).Error; err != nil {
			return err
		}
		mapping[env.ID] = copy.ID
	}
	var items []database.InventoryItem
	if err := tx.Model(&database.InventoryItem{}).Joins("JOIN origins.inventory_environments e ON e.tenant_id=inventory_items.tenant_id AND e.id=inventory_items.environment_id AND e.origin_version_id=inventory_items.origin_version_id AND e.decision<>'REJECTED'").Where("inventory_items.tenant_id=? AND inventory_items.origin_version_id=? AND inventory_items.decision IN ('ACCEPTED','EDITED','ADDED') AND inventory_items.merged_into_id IS NULL", tenant, source).Find(&items).Error; err != nil {
		return err
	}
	for _, item := range items {
		copy := item
		copy.ID = identity.NewID()
		copy.OriginVersionID = target
		copy.EnvironmentID = mapping[item.EnvironmentID]
		copy.Decision = "ACCEPTED"
		copy.Version = 1
		copy.DecidedBy = nil
		copy.DecidedAt = nil
		copy.RejectionReason = nil
		copy.RejectionText = nil
		copy.Suggested = nil
		copy.MergedIntoID = nil
		copy.CreatedAt = now
		copy.UpdatedAt = now
		if err := tx.Create(&copy).Error; err != nil {
			return err
		}
		var evs []database.InventoryItemEvidence
		if err := tx.Where("tenant_id=? AND item_id=?", tenant, item.ID).Find(&evs).Error; err != nil {
			return err
		}
		for _, e := range evs {
			e.ID = identity.NewID()
			e.ItemID = copy.ID
			if err := tx.Create(&e).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// LegacySourcesExpired reports whether a LEGACY_ORIGIN draft catalogs a video
// origin whose source is gone; cataloging then relies on retained frames.
func (s Service) LegacySourcesExpired(ctx context.Context, tenantID, draftID identity.ID) (bool, error) {
	expired := false
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var draft database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", tenantID, draftID).Take(&draft).Error; err != nil {
			return err
		}
		if draft.SourceVersionID == nil {
			return nil
		}
		var legacy database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", tenantID, *draft.SourceVersionID).Take(&legacy).Error; err != nil {
			return err
		}
		if legacy.MediaKind != "VIDEO" || legacy.VideoPreparationID == nil {
			return nil
		}
		var count int64
		err := tx.Table("media.video_sources AS s").Joins("JOIN media.video_preparation_jobs AS p ON p.tenant_id=s.tenant_id AND p.media_id=s.media_id").Where("s.tenant_id=? AND p.id=? AND (s.status IN ('EXPIRED','DELETING') OR s.original_deleted_at IS NOT NULL)", tenantID, *legacy.VideoPreparationID).Count(&count).Error
		expired = count > 0
		return err
	})
	return expired, err
}
