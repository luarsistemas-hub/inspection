package core

import (
	"context"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	origincore "inspection/services/inspection/internal/features/origins/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Activate turns a reviewed draft into the sole active inventory baseline.
func (s Service) Activate(ctx context.Context, in ActivateInput) (database.OriginVersion, error) {
	var result database.OriginVersion
	if in.ExpectedOriginVersion <= 0 {
		return result, apperror.New(apperror.InvalidInput, "expectedOriginVersion", "expected origin version is required")
	}
	err := s.within(ctx, in.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=? AND has_inventory", in.TenantID, in.OriginVersionID).First(&result).Error; err != nil {
			return apperror.New(apperror.NotFound, "originVersionId", "inventory version not found")
		}
		if !purposes[in.Purpose] {
			return apperror.New(apperror.InvalidInput, "purpose", "unknown baseline purpose")
		}
		var origin database.Origin
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", in.TenantID, result.OriginID).First(&origin).Error; err != nil {
			return err
		}
		// Re-read after locking the origin so concurrent activations observe the
		// winner's state and cannot supersede it with a stale draft snapshot.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=? AND origin_id=? AND has_inventory", in.TenantID, in.OriginVersionID, origin.ID).First(&result).Error; err != nil {
			return apperror.New(apperror.NotFound, "originVersionId", "inventory version not found")
		}
		if result.Status == "ACTIVE" && result.Purpose != nil && *result.Purpose == in.Purpose {
			return nil
		}
		if origin.Version != in.ExpectedOriginVersion {
			return apperror.New(apperror.Conflict, "expectedOriginVersion", "baseline changed; refresh and confirm again")
		}
		if err := s.assertSourceValid(tx, in.TenantID, result); err != nil {
			return err
		}
		if result.Status != "DRAFT" {
			return draftStateError(result)
		}
		if result.DraftState == nil || *result.DraftState != "READY" {
			return apperror.New(apperror.InvalidState, "originVersionId", "draft is not ready")
		}
		var asset database.Asset
		if err := tx.Where("tenant_id=? AND id=?", in.TenantID, origin.AssetID).First(&asset).Error; err != nil {
			return err
		}
		if asset.Status == "ARCHIVED" {
			return apperror.New(apperror.InvalidState, "assetId", "ASSET_ARCHIVED")
		}
		var envCount, included, rejected, pending int64
		if err := tx.Model(&database.InventoryEnvironment{}).Where("tenant_id=? AND origin_version_id=? AND decision<>'REJECTED'", in.TenantID, result.ID).Count(&envCount).Error; err != nil {
			return err
		}
		if envCount > MaxEnvironments {
			return apperror.New(apperror.InvalidState, "originVersionId", "LIMIT_ENVIRONMENTS")
		}
		if envCount == 0 {
			return apperror.New(apperror.InvalidState, "originVersionId", "NO_ENVIRONMENTS")
		}
		activeItems := tx.Model(&database.InventoryItem{}).Joins("JOIN origins.inventory_environments e ON e.tenant_id=inventory_items.tenant_id AND e.id=inventory_items.environment_id AND e.origin_version_id=inventory_items.origin_version_id AND e.decision<>'REJECTED'").Where("inventory_items.tenant_id=? AND inventory_items.origin_version_id=? AND inventory_items.merged_into_id IS NULL", in.TenantID, result.ID)
		if err := activeItems.Where("inventory_items.decision IN ('ACCEPTED','EDITED','ADDED')").Count(&included).Error; err != nil {
			return err
		}
		if included == 0 {
			return apperror.New(apperror.InvalidState, "originVersionId", "NO_INCLUDED_ITEMS")
		}
		for query, dest := range map[string]*int64{"REJECTED": &rejected, "PENDING": &pending} {
			if err := tx.Model(&database.InventoryItem{}).Joins("JOIN origins.inventory_environments e ON e.tenant_id=inventory_items.tenant_id AND e.id=inventory_items.environment_id AND e.origin_version_id=inventory_items.origin_version_id AND e.decision<>'REJECTED'").Where("inventory_items.tenant_id=? AND inventory_items.origin_version_id=? AND inventory_items.decision=? AND inventory_items.merged_into_id IS NULL", in.TenantID, result.ID, query).Count(dest).Error; err != nil {
				return err
			}
		}
		if included+pending > MaxItemsPerVersion {
			return apperror.New(apperror.InvalidState, "originVersionId", "LIMIT_ITEMS_PER_VERSION")
		}
		var oversized int64
		if err := tx.Model(&database.InventoryItem{}).Joins("JOIN origins.inventory_environments e ON e.tenant_id=inventory_items.tenant_id AND e.id=inventory_items.environment_id AND e.origin_version_id=inventory_items.origin_version_id AND e.decision<>'REJECTED'").Where("inventory_items.tenant_id=? AND inventory_items.origin_version_id=? AND inventory_items.decision<>'REJECTED' AND inventory_items.merged_into_id IS NULL", in.TenantID, result.ID).Group("inventory_items.environment_id").Having("count(*) > ?", MaxItemsPerEnvironment).Count(&oversized).Error; err != nil {
			return err
		}
		if oversized > 0 {
			return apperror.New(apperror.InvalidState, "originVersionId", "LIMIT_ITEMS_PER_ENVIRONMENT")
		}
		var activeRows int64
		if err := tx.Model(&database.InventoryItem{}).Joins("JOIN origins.inventory_environments e ON e.tenant_id=inventory_items.tenant_id AND e.id=inventory_items.environment_id AND e.origin_version_id=inventory_items.origin_version_id AND e.decision<>'REJECTED'").Where("inventory_items.tenant_id=? AND inventory_items.origin_version_id=? AND inventory_items.decision<>'REJECTED' AND inventory_items.merged_into_id IS NULL", in.TenantID, result.ID).Count(&activeRows).Error; err != nil {
			return err
		}
		if activeRows > MaxItemsPerVersion {
			return apperror.New(apperror.InvalidState, "originVersionId", "LIMIT_ITEMS_PER_VERSION")
		}
		var sourceOversized int64
		if err := tx.Model(&database.InventoryEnvironment{}).Where("tenant_id=? AND origin_version_id=? AND decision<>'REJECTED'", in.TenantID, result.ID).Count(&sourceOversized).Error; err != nil {
			return err
		}
		if sourceOversized > MaxEnvironments {
			return apperror.New(apperror.InvalidState, "originVersionId", "LIMIT_ENVIRONMENTS")
		}
		beforeActive := identity.ID{}
		now := s.now()
		if err := retainInventoryVideoEvidence(tx, in.TenantID, result.ID, now); err != nil {
			return err
		}
		if origin.ActiveVersionID != nil {
			beforeActive = *origin.ActiveVersionID
			updates := map[string]any{"status": "SUPERSEDED", "superseded_at": now}
			if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=? AND status='ACTIVE'", in.TenantID, beforeActive).Updates(updates).Error; err != nil {
				return err
			}
			if err := origincore.ReleaseOriginReference(tx, in.TenantID, beforeActive); err != nil {
				return err
			}
		}
		purpose := in.Purpose
		result.Status = "ACTIVE"
		result.Purpose = &purpose
		result.ActivatedAt = &now
		result.ActivatedByMembershipID = &in.ActorMembershipID
		if beforeActive != (identity.ID{}) {
			result.SupersedesID = &beforeActive
		}
		if err := tx.Save(&result).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.Origin{}).Where("tenant_id=? AND id=?", in.TenantID, origin.ID).Updates(map[string]any{"active_version_id": result.ID, "version": gorm.Expr("version+1"), "updated_at": now}).Error; err != nil {
			return err
		}
		// Rejected counts all excluded items in the draft, including items
		// excluded with their environment. The REMOVE_ENVIRONMENT audit event
		// retains why that group was excluded.
		counts := ItemCounts{Included: int(included), Rejected: int(rejected), NotReviewed: int(pending)}
		if err := s.audit(tx, in.TenantID, result.ID, "VERSION", result.ID, "ACTIVATE", marshal(map[string]any{"previousActiveVersionId": beforeActive}), marshal(counts), "", in.ActorMembershipID, now); err != nil {
			return err
		}
		if s.Retention != nil {
			versions := []identity.ID{result.ID}
			if beforeActive != (identity.ID{}) {
				versions = append(versions, beforeActive)
			}
			if err := s.Retention.TransitionVersions(ctx, tx, in.TenantID, versions); err != nil {
				return err
			}
		}
		if len(in.CounterpartyParticipantIDs) > 0 && s.Counterparties != nil {
			if err := s.Counterparties.Invite(tx, in.TenantID, result.ID, in.CounterpartyParticipantIDs, in.ActorMembershipID); err != nil {
				return err
			}
		}
		return messaging.AddOutbox(tx, inventoryEvent(in.TenantID, result.ID, now))
	})
	return result, err
}

// retainInventoryVideoEvidence pins every preparation bank used by the new
// baseline before superseded origin references are released.
func retainInventoryVideoEvidence(tx *gorm.DB, tenantID, versionID identity.ID, now time.Time) error {
	var preparationIDs []identity.ID
	if err := tx.Table("origins.inventory_item_evidence AS evidence").
		Distinct("frames.preparation_id").
		Joins("JOIN media.video_frames AS frames ON frames.tenant_id=evidence.tenant_id AND frames.id=evidence.frame_id").
		Joins("JOIN origins.inventory_items AS items ON items.tenant_id=evidence.tenant_id AND items.id=evidence.item_id").
		Where("evidence.tenant_id=? AND items.origin_version_id=? AND evidence.kind='VIDEO_FRAME'", tenantID, versionID).
		Pluck("frames.preparation_id", &preparationIDs).Error; err != nil {
		return err
	}
	for _, preparationID := range preparationIDs {
		reference := database.VideoEvidenceReference{ID: identity.NewID(), TenantID: tenantID, PreparationID: preparationID, OwnerType: "ORIGIN_VERSION", OwnerID: versionID, CreatedAt: now}
		if err := tx.Where("tenant_id=? AND preparation_id=? AND owner_type=? AND owner_id=?", tenantID, preparationID, reference.OwnerType, versionID).FirstOrCreate(&reference).Error; err != nil {
			return err
		}
	}
	return nil
}

// Discard permanently closes an editable inventory draft.
func (s Service) Discard(ctx context.Context, tenantID, versionID, actor identity.ID) (database.OriginVersion, error) {
	var result database.OriginVersion
	if tenantID == (identity.ID{}) || versionID == (identity.ID{}) {
		return result, apperror.New(apperror.InvalidInput, "originVersionId", "inventory version is required")
	}
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=? AND has_inventory", tenantID, versionID).First(&result).Error; err != nil {
			return apperror.New(apperror.NotFound, "originVersionId", "inventory version not found")
		}
		if result.Status != "DRAFT" || result.DraftState == nil {
			return apperror.New(apperror.InvalidState, "originVersionId", "only inventory drafts can be discarded")
		}
		if *result.DraftState == "PREPARING" {
			return apperror.New(apperror.InvalidState, "originVersionId", "DRAFT_PREPARING")
		}
		before := marshal(result)
		now := s.now()
		result.Status = "DISCARDED"
		result.DiscardedAt = &now
		if err := tx.Save(&result).Error; err != nil {
			return err
		}
		return s.audit(tx, tenantID, result.ID, "VERSION", result.ID, "DISCARD", before, marshal(result), "", actor, now)
	})
	return result, err
}

func inventoryEvent(tenant, version identity.ID, now time.Time) events.Envelope[map[string]any] {
	return events.Envelope[map[string]any]{ID: identity.NewID(), Type: "inventory.version_activated.v1", SchemaVersion: 1, OccurredAt: now, TenantID: tenant, AggregateID: version, CorrelationID: "inventory-activation-" + version.String(), Payload: map[string]any{"originVersionId": version}}
}
