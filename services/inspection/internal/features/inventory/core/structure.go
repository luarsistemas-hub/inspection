package core

import (
	"context"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AddItem adds a manually authored item and validates evidence ownership.
func (s Service) AddItem(ctx context.Context, in AddItemInput) (database.InventoryItem, error) {
	var out database.InventoryItem
	if err := ValidateItem(in.TypeCode, in.Name); err != nil {
		return out, err
	}
	if in.Condition == "" {
		in.Condition = "NOT_ASSESSABLE"
	}
	if !IsCondition(in.Condition) {
		return out, apperror.New(apperror.InvalidInput, "condition", "unknown condition")
	}
	if in.Observation == "" {
		in.Observation = "OBSERVED"
	}
	if !IsObservation(in.Observation) {
		return out, apperror.New(apperror.InvalidInput, "observation", "unknown observation")
	}
	err := s.within(ctx, in.TenantID, func(tx *gorm.DB) error {
		if _, err := s.editable(tx, in.TenantID, in.OriginVersionID); err != nil {
			return err
		}
		var env database.InventoryEnvironment
		if err := tx.Where("tenant_id=? AND origin_version_id=? AND id=?", in.TenantID, in.OriginVersionID, in.EnvironmentID).First(&env).Error; err != nil {
			return apperror.New(apperror.NotFound, "environmentId", "environment not found")
		}
		if env.Decision == "REJECTED" {
			return apperror.New(apperror.InvalidState, "environmentId", "environment is excluded")
		}
		var n int64
		if err := tx.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=? AND environment_id=? AND decision<>'REJECTED' AND merged_into_id IS NULL", in.TenantID, in.OriginVersionID, in.EnvironmentID).Count(&n).Error; err != nil {
			return err
		}
		if n >= MaxItemsPerEnvironment {
			return apperror.New(apperror.InvalidInput, "items", "LIMIT_ITEMS_PER_ENVIRONMENT")
		}
		if err := tx.Model(&database.InventoryItem{}).Joins("JOIN origins.inventory_environments e ON e.tenant_id=inventory_items.tenant_id AND e.id=inventory_items.environment_id AND e.origin_version_id=inventory_items.origin_version_id AND e.decision<>'REJECTED'").Where("inventory_items.tenant_id=? AND inventory_items.origin_version_id=? AND inventory_items.decision<>'REJECTED' AND inventory_items.merged_into_id IS NULL", in.TenantID, in.OriginVersionID).Count(&n).Error; err != nil {
			return err
		}
		if n >= MaxItemsPerVersion {
			return apperror.New(apperror.InvalidInput, "items", "LIMIT_ITEMS_PER_VERSION")
		}
		if err := s.validateEvidence(tx, in.TenantID, in.OriginVersionID, in.Evidence); err != nil {
			return err
		}
		now := s.now()
		out = database.InventoryItem{ID: identity.NewID(), TenantID: in.TenantID, OriginVersionID: in.OriginVersionID, EnvironmentID: in.EnvironmentID, IdentityID: identity.NewID(), TypeCode: in.TypeCode, Name: in.Name, Material: in.Material, Color: in.Color, Quantity: in.Quantity, Condition: in.Condition, Observation: in.Observation, Limitation: in.Limitation, Origin: "MANUAL", Declared: len(in.Evidence) == 0, Decision: "ADDED", Version: 1, DecidedBy: &in.ActorMembershipID, DecidedAt: &now, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&out).Error; err != nil {
			return err
		}
		for i, e := range in.Evidence {
			row := database.InventoryItemEvidence{ID: identity.NewID(), TenantID: in.TenantID, ItemID: out.ID, Kind: e.Kind, MediaID: e.MediaID, FrameID: e.FrameID, Region: e.Region, Position: i, CreatedAt: now}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return s.audit(tx, in.TenantID, in.OriginVersionID, "ITEM", out.ID, "ADD", nil, marshal(out), "", in.ActorMembershipID, now)
	})
	return out, err
}

// MergeItems combines 2–10 items, preserving the first item's identity and evidence.
func (s Service) MergeItems(ctx context.Context, in MergeItemsInput) (database.InventoryItem, error) {
	var out database.InventoryItem
	if len(in.ItemIDs) < 2 || len(in.ItemIDs) > 10 {
		return out, apperror.New(apperror.InvalidInput, "itemIds", "select 2 to 10 items")
	}
	err := s.within(ctx, in.TenantID, func(tx *gorm.DB) error {
		if _, err := s.editable(tx, in.TenantID, in.OriginVersionID); err != nil {
			return err
		}
		var found []database.InventoryItem
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND origin_version_id=? AND id IN ?", in.TenantID, in.OriginVersionID, in.ItemIDs).Find(&found).Error; err != nil {
			return err
		}
		if len(found) != len(in.ItemIDs) {
			return apperror.New(apperror.NotFound, "itemIds", "inventory item not found")
		}
		byID := make(map[identity.ID]database.InventoryItem, len(found))
		for _, item := range found {
			byID[item.ID] = item
		}
		items := make([]database.InventoryItem, 0, len(in.ItemIDs))
		for _, id := range in.ItemIDs {
			items = append(items, byID[id])
		}
		env := items[0].EnvironmentID
		if in.ResultEnvironmentID != nil {
			env = *in.ResultEnvironmentID
		} else {
			for _, it := range items {
				if it.EnvironmentID != env {
					return apperror.New(apperror.InvalidInput, "resultEnvironmentId", "required when merging environments")
				}
			}
		}
		var target database.InventoryEnvironment
		if err := tx.Where("tenant_id=? AND origin_version_id=? AND id=?", in.TenantID, in.OriginVersionID, env).First(&target).Error; err != nil {
			return apperror.New(apperror.InvalidInput, "resultEnvironmentId", "environment must belong to draft")
		}
		if target.Decision == "REJECTED" {
			return apperror.New(apperror.InvalidState, "resultEnvironmentId", "environment is excluded")
		}
		for _, item := range items {
			if item.Decision == "REJECTED" || item.MergedIntoID != nil {
				return apperror.New(apperror.InvalidState, "itemIds", "excluded items cannot be merged")
			}
			var sourceEnv database.InventoryEnvironment
			if err := tx.Where("tenant_id=? AND origin_version_id=? AND id=?", in.TenantID, in.OriginVersionID, item.EnvironmentID).First(&sourceEnv).Error; err != nil || sourceEnv.Decision == "REJECTED" {
				return apperror.New(apperror.InvalidState, "itemIds", "item environment is excluded")
			}
		}
		var targetCount int64
		if err := tx.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=? AND environment_id=? AND decision<>'REJECTED' AND merged_into_id IS NULL AND id NOT IN ?", in.TenantID, in.OriginVersionID, env, in.ItemIDs).Count(&targetCount).Error; err != nil {
			return err
		}
		if targetCount+1 > MaxItemsPerEnvironment {
			return apperror.New(apperror.InvalidInput, "items", "LIMIT_ITEMS_PER_ENVIRONMENT")
		}
		out = items[0]
		before := marshal(items)
		out.EnvironmentID = env
		now := s.now()
		out.Decision = "EDITED"
		out.Version++
		out.DecidedBy = &in.ActorMembershipID
		out.DecidedAt = &now
		out.UpdatedAt = now
		var allEvidence []database.InventoryItemEvidence
		if err := tx.Where("tenant_id=? AND item_id IN ?", in.TenantID, in.ItemIDs).Order("position,id").Find(&allEvidence).Error; err != nil {
			return err
		}
		wasPending := false
		for _, item := range items {
			if item.Decision == "PENDING" && !item.Declared {
				wasPending = true
				break
			}
		}
		if wasPending && len(allEvidence) > 0 {
			availability, err := LoadEvidenceAvailability(tx, in.TenantID, allEvidence)
			if err != nil {
				return err
			}
			allUnavailable := true
			for _, evidence := range allEvidence {
				if availability[evidence.ID] != "UNAVAILABLE" {
					allUnavailable = false
					break
				}
			}
			if allUnavailable {
				return apperror.New(apperror.InvalidState, "itemIds", "EVIDENCE_UNAVAILABLE")
			}
		}
		seenEvidence := make(map[string]bool, len(allEvidence))
		position := 0
		for _, evidence := range allEvidence {
			key := evidence.Kind + ":" + evidence.MediaID.String()
			if evidence.FrameID != nil {
				key += ":" + evidence.FrameID.String()
			}
			if seenEvidence[key] {
				continue
			}
			seenEvidence[key] = true
			if evidence.ItemID == out.ID {
				evidence.Position = position
				if err := tx.Save(&evidence).Error; err != nil {
					return err
				}
			} else {
				evidence.ID = identity.NewID()
				evidence.ItemID = out.ID
				evidence.Position = position
				if err := tx.Create(&evidence).Error; err != nil {
					return err
				}
			}
			position++
		}
		if err := tx.Save(&out).Error; err != nil {
			return err
		}
		for _, it := range items[1:] {
			it.MergedIntoID = &out.ID
			it.Decision = "REJECTED"
			reason := "DUPLICATE"
			it.RejectionReason = &reason
			it.Version++
			it.UpdatedAt = now
			if err := tx.Save(&it).Error; err != nil {
				return err
			}
		}
		return s.audit(tx, in.TenantID, in.OriginVersionID, "ITEM", out.ID, "MERGE", before, marshal(out), "", in.ActorMembershipID, now)
	})
	return out, err
}

// SplitItem creates 2–5 new items from one existing item.
func (s Service) SplitItem(ctx context.Context, in SplitItemInput) ([]database.InventoryItem, error) {
	var out []database.InventoryItem
	if len(in.Parts) < 2 || len(in.Parts) > 5 {
		return nil, apperror.New(apperror.InvalidInput, "parts", "split requires 2 to 5 parts")
	}
	err := s.within(ctx, in.TenantID, func(tx *gorm.DB) error {
		if _, err := s.editable(tx, in.TenantID, in.OriginVersionID); err != nil {
			return err
		}
		var original database.InventoryItem
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND origin_version_id=? AND id=?", in.TenantID, in.OriginVersionID, in.ItemID).First(&original).Error; err != nil {
			return apperror.New(apperror.NotFound, "itemId", "inventory item not found")
		}
		if original.Version != in.ExpectedVersion {
			return apperror.New(apperror.Conflict, "expectedVersion", "item changed")
		}
		if original.Decision == "REJECTED" || original.MergedIntoID != nil {
			return apperror.New(apperror.InvalidState, "itemId", "excluded items cannot be split")
		}
		var environment database.InventoryEnvironment
		if err := tx.Where("tenant_id=? AND origin_version_id=? AND id=?", in.TenantID, in.OriginVersionID, original.EnvironmentID).First(&environment).Error; err != nil || environment.Decision == "REJECTED" {
			return apperror.New(apperror.InvalidState, "itemId", "item environment is excluded")
		}
		now := s.now()
		var currentEnvCount, currentVersionCount int64
		if err := tx.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=? AND environment_id=? AND decision<>'REJECTED' AND merged_into_id IS NULL", in.TenantID, in.OriginVersionID, original.EnvironmentID).Count(&currentEnvCount).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=? AND decision<>'REJECTED' AND merged_into_id IS NULL", in.TenantID, in.OriginVersionID).Count(&currentVersionCount).Error; err != nil {
			return err
		}
		if currentEnvCount-1+int64(len(in.Parts)) > MaxItemsPerEnvironment {
			return apperror.New(apperror.InvalidInput, "parts", "LIMIT_ITEMS_PER_ENVIRONMENT")
		}
		if currentVersionCount-1+int64(len(in.Parts)) > MaxItemsPerVersion {
			return apperror.New(apperror.InvalidInput, "parts", "LIMIT_ITEMS_PER_VERSION")
		}
		for _, p := range in.Parts {
			if err := ValidateItem(p.TypeCode, p.Name); err != nil {
				return err
			}
			condition := p.Condition
			if condition == "" {
				condition = original.Condition
			}
			if !IsCondition(condition) {
				return apperror.New(apperror.InvalidInput, "condition", "unknown condition")
			}
			part := database.InventoryItem{ID: identity.NewID(), TenantID: in.TenantID, OriginVersionID: in.OriginVersionID, EnvironmentID: original.EnvironmentID, IdentityID: identity.NewID(), TypeCode: p.TypeCode, Name: p.Name, Condition: condition, Observation: original.Observation, Origin: "MANUAL", Declared: len(p.EvidenceIDs) == 0, Decision: "ADDED", Version: 1, DecidedBy: &in.ActorMembershipID, DecidedAt: &now, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&part).Error; err != nil {
				return err
			}
			for i, id := range p.EvidenceIDs {
				var e database.InventoryItemEvidence
				if err := tx.Where("tenant_id=? AND item_id=? AND id=?", in.TenantID, original.ID, id).First(&e).Error; err != nil {
					return apperror.New(apperror.InvalidInput, "evidenceIds", "evidence must belong to split item")
				}
				e.ID = identity.NewID()
				e.ItemID = part.ID
				e.Position = i
				if err := tx.Create(&e).Error; err != nil {
					return err
				}
			}
			out = append(out, part)
		}
		before := marshal(original)
		original.Decision = "REJECTED"
		reason := "NOT_RELEVANT"
		original.RejectionReason = &reason
		firstPartID := out[0].ID
		original.MergedIntoID = &firstPartID
		original.Version++
		if err := tx.Save(&original).Error; err != nil {
			return err
		}
		return s.audit(tx, in.TenantID, in.OriginVersionID, "ITEM", original.ID, "SPLIT", before, marshal(out), "", in.ActorMembershipID, now)
	})
	return out, err
}

// UpsertEnvironment creates or edits an environment in a draft.
func (s Service) UpsertEnvironment(ctx context.Context, in UpsertEnvironmentInput) (database.InventoryEnvironment, error) {
	var out database.InventoryEnvironment
	if err := ValidateEnvironment(in.TypeCode, in.Name); err != nil {
		return out, err
	}
	err := s.within(ctx, in.TenantID, func(tx *gorm.DB) error {
		if _, err := s.editable(tx, in.TenantID, in.OriginVersionID); err != nil {
			return err
		}
		now := s.now()
		if in.EnvironmentID == (identity.ID{}) {
			var n int64
			if err := tx.Model(&database.InventoryEnvironment{}).Where("tenant_id=? AND origin_version_id=? AND decision<>'REJECTED'", in.TenantID, in.OriginVersionID).Count(&n).Error; err != nil {
				return err
			}
			if n >= MaxEnvironments {
				return apperror.New(apperror.InvalidInput, "environments", "LIMIT_ENVIRONMENTS")
			}
			out = database.InventoryEnvironment{ID: identity.NewID(), TenantID: in.TenantID, OriginVersionID: in.OriginVersionID, IdentityID: identity.NewID(), TypeCode: in.TypeCode, Name: in.Name, Expected: in.Expected, Decision: "ADDED", Position: int(n), Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&out).Error; err != nil {
				return err
			}
			return s.audit(tx, in.TenantID, in.OriginVersionID, "ENVIRONMENT", out.ID, "ADD", nil, marshal(out), "", in.ActorMembershipID, now)
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND origin_version_id=? AND id=?", in.TenantID, in.OriginVersionID, in.EnvironmentID).First(&out).Error; err != nil {
			return apperror.New(apperror.NotFound, "environmentId", "environment not found")
		}
		if out.Decision == "REJECTED" {
			return apperror.New(apperror.InvalidState, "environmentId", "environment is excluded")
		}
		before := marshal(out)
		out.TypeCode = in.TypeCode
		out.Name = in.Name
		out.Expected = in.Expected
		out.Decision = "EDITED"
		out.Version++
		out.UpdatedAt = now
		if err := tx.Save(&out).Error; err != nil {
			return err
		}
		return s.audit(tx, in.TenantID, in.OriginVersionID, "ENVIRONMENT", out.ID, "EDIT", before, marshal(out), "", in.ActorMembershipID, now)
	})
	return out, err
}

// RemoveEnvironment excludes an environment and its items from the draft.
func (s Service) RemoveEnvironment(ctx context.Context, in RemoveEnvironmentInput) error {
	if in.TenantID == (identity.ID{}) || in.OriginVersionID == (identity.ID{}) {
		return apperror.New(apperror.InvalidInput, "originVersionId", "inventory version is required")
	}
	if in.EnvironmentID == (identity.ID{}) {
		return apperror.New(apperror.InvalidInput, "environmentId", "environment is required")
	}
	return s.within(ctx, in.TenantID, func(tx *gorm.DB) error {
		if _, err := s.editable(tx, in.TenantID, in.OriginVersionID); err != nil {
			return err
		}
		var env database.InventoryEnvironment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND origin_version_id=? AND id=?", in.TenantID, in.OriginVersionID, in.EnvironmentID).First(&env).Error; err != nil {
			return apperror.New(apperror.NotFound, "environmentId", "environment not found")
		}
		var count int64
		if err := tx.Model(&database.InventoryEnvironment{}).Where("tenant_id=? AND origin_version_id=? AND decision<>'REJECTED'", in.TenantID, in.OriginVersionID).Count(&count).Error; err != nil {
			return err
		}
		if env.Decision == "REJECTED" {
			return apperror.New(apperror.InvalidState, "environmentId", "environment is already excluded")
		}
		if count <= 1 {
			return apperror.New(apperror.InvalidState, "environmentId", "at least one environment must remain")
		}
		var accepted int64
		if err := tx.Model(&database.InventoryItem{}).Where("tenant_id=? AND environment_id=? AND decision IN ('ACCEPTED','EDITED','ADDED')", in.TenantID, env.ID).Count(&accepted).Error; err != nil {
			return err
		}
		if accepted > 0 && !in.ConfirmItemExclusion {
			return apperror.New(apperror.InvalidInput, "confirmItemExclusion", "confirmation is required to exclude accepted items")
		}
		var items []database.InventoryItem
		if err := tx.Where("tenant_id=? AND environment_id=?", in.TenantID, env.ID).Find(&items).Error; err != nil {
			return err
		}
		now := s.now()
		before := marshal(map[string]any{"environment": env, "items": items})
		env.Decision = "REJECTED"
		env.Version++
		env.UpdatedAt = now
		if err := tx.Save(&env).Error; err != nil {
			return err
		}
		for i := range items {
			if items[i].Decision == "REJECTED" {
				continue
			}
			items[i].Decision = "REJECTED"
			reason := "NOT_RELEVANT"
			items[i].RejectionReason = &reason
			items[i].Version++
			items[i].DecidedBy = &in.ActorMembershipID
			items[i].DecidedAt = &now
			items[i].UpdatedAt = now
			if err := tx.Save(&items[i]).Error; err != nil {
				return err
			}
		}
		return s.audit(tx, in.TenantID, in.OriginVersionID, "ENVIRONMENT", env.ID, "REMOVE_ENVIRONMENT", before, marshal(map[string]any{"environment": env, "items": items}), "", in.ActorMembershipID, now)
	})
}
