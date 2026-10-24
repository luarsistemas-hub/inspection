package core

import (
	"context"
	"errors"
	"time"

	"inspection/libs/identity"
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
		return err
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
		var jobs []database.InventoryJob
		if err := tx.Where("tenant_id=? AND inspection_id=? AND kind='COMPARE' AND status='SUCCEEDED'", tenantID, inspectionID).Find(&jobs).Error; err != nil {
			return err
		}
		jobIDs := make([]identity.ID, 0, len(jobs))
		jobByID := make(map[identity.ID]database.InventoryJob, len(jobs))
		for _, job := range jobs {
			jobIDs = append(jobIDs, job.ID)
			jobByID[job.ID] = job
		}
		if len(jobIDs) > 0 {
			var results []database.InventoryItemResult
			if err := tx.Where("tenant_id=? AND job_id IN ?", tenantID, jobIDs).Find(&results).Error; err != nil {
				return err
			}
			for _, itemResult := range results {
				switch itemResult.Status {
				case "CONDITION_CHANGED":
					if itemResult.ItemIdentityID == nil || itemResult.ConditionAfter == nil {
						continue
					}
					var copied database.InventoryItem
					if err := tx.Where("tenant_id=? AND origin_version_id=? AND identity_id=?", tenantID, result.ID, *itemResult.ItemIdentityID).First(&copied).Error; err != nil {
						continue
					}
					copied.Decision = "PENDING"
					copied.Suggested = marshal(map[string]any{"condition": *itemResult.ConditionAfter})
					copied.Version++
					if err := tx.Save(&copied).Error; err != nil {
						return err
					}
				case "OUTSIDE_INVENTORY":
					job := jobByID[itemResult.JobID]
					if job.EnvironmentIdentityID == nil || itemResult.OutsideTypeCode == nil || itemResult.OutsideName == nil {
						continue
					}
					var env database.InventoryEnvironment
					if err := tx.Where("tenant_id=? AND origin_version_id=? AND identity_id=?", tenantID, result.ID, *job.EnvironmentIdentityID).First(&env).Error; err != nil {
						continue
					}
					created := database.InventoryItem{ID: identity.NewID(), TenantID: tenantID, OriginVersionID: result.ID, EnvironmentID: env.ID, IdentityID: identity.NewID(), TypeCode: *itemResult.OutsideTypeCode, Name: *itemResult.OutsideName, Condition: "NOT_ASSESSABLE", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", Version: 1, Suggested: marshal(map[string]any{"status": "OUTSIDE_INVENTORY"}), CreatedAt: s.now(), UpdatedAt: s.now()}
					if err := tx.Create(&created).Error; err != nil {
						return err
					}
				}
			}
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
