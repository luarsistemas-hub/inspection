package core

import (
	"context"
	"errors"
	"sort"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// Versions lists origin versions newest first.
func (s Service) Versions(ctx context.Context, tenantID, assetID identity.ID, limit, after int) ([]database.OriginVersion, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	var rows []database.OriginVersion
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var origin database.Origin
		if err := tx.Where("tenant_id=? AND asset_id=?", tenantID, assetID).First(&origin).Error; err != nil {
			return apperror.New(apperror.NotFound, "assetId", "asset not found")
		}
		q := tx.Where("tenant_id=? AND origin_id=?", tenantID, origin.ID)
		if after > 0 {
			q = q.Where("version_number < ?", after)
		}
		return q.Order("version_number DESC").Limit(limit).Find(&rows).Error
	})
	return rows, err
}

// InventoryDraft loads an internally ordered draft and progress summary.
func (s Service) InventoryDraft(ctx context.Context, tenantID, versionID identity.ID) (DraftView, error) {
	var out DraftView
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=? AND has_inventory", tenantID, versionID).First(&out.Version).Error; err != nil {
			return apperror.New(apperror.NotFound, "originVersionId", "inventory version not found")
		}
		if err := s.assertSourceValid(tx, tenantID, out.Version); err != nil {
			if !apperror.Is(err, apperror.InvalidState) {
				return err
			}
			out.ReadOnly = true
			out.NoticeCode = "SOURCE_INVALIDATED"
		}
		if out.Version.Source == "COMPARATIVE_INSPECTION" && out.Version.SourceVersionID != nil {
			var source database.OriginVersion
			var origin database.Origin
			if err := tx.Where("tenant_id=? AND id=?", tenantID, out.Version.OriginID).First(&origin).Error; err == nil && origin.ActiveVersionID != nil && *origin.ActiveVersionID != *out.Version.SourceVersionID {
				var active database.OriginVersion
				if err := tx.Where("tenant_id=? AND id=?", tenantID, *origin.ActiveVersionID).First(&active).Error; err == nil && tx.Where("tenant_id=? AND id=?", tenantID, *out.Version.SourceVersionID).First(&source).Error == nil && active.VersionNumber > source.VersionNumber {
					out.Warnings = append(out.Warnings, "NEWER_VERSION_EXISTS")
				}
			}
		}
		var envs []database.InventoryEnvironment
		if err := tx.Where("tenant_id=? AND origin_version_id=? AND decision<>'REJECTED'", tenantID, versionID).Order("position,id").Find(&envs).Error; err != nil {
			return err
		}
		return loadInventoryViews(tx, tenantID, envs, false, &out, nil)
	})
	return out, err
}

// BaselineInventory returns an internal or customer-safe inventory projection.
func (s Service) BaselineInventory(ctx context.Context, tenantID, assetID identity.ID, versionID *identity.ID, customer bool) (BaselineView, error) {
	var out BaselineView
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var asset database.Asset
		if err := tx.Where("tenant_id=? AND id=?", tenantID, assetID).First(&asset).Error; err != nil {
			return apperror.New(apperror.NotFound, "assetId", "asset not found")
		}
		var origin database.Origin
		if err := tx.Where("tenant_id=? AND asset_id=?", tenantID, assetID).First(&origin).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			if versionID != nil {
				return apperror.New(apperror.NotFound, "versionId", "inventory version not found")
			}
			return nil
		} else if err != nil {
			return err
		}
		id := versionID
		if id == nil {
			id = origin.ActiveVersionID
		}
		if id == nil {
			return nil
		}
		var version database.OriginVersion
		if err := tx.Where("tenant_id=? AND origin_id=? AND id=? AND status IN ('ACTIVE','SUPERSEDED')", tenantID, origin.ID, *id).First(&version).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.NotFound, "versionId", "inventory version not found")
			}
			return err
		}
		if !version.HasInventory {
			if versionID == nil {
				return nil
			}
			return apperror.New(apperror.NotFound, "versionId", "inventory version not found")
		}
		out.HasInventory = true
		out.Version = &version
		var envs []database.InventoryEnvironment
		if err := tx.Where("tenant_id=? AND origin_version_id=? AND decision<>'REJECTED'", tenantID, version.ID).Order("position,id").Find(&envs).Error; err != nil {
			return err
		}
		return loadInventoryViews(tx, tenantID, envs, customer, nil, &out.Environments)
	})
	return out, err
}

// InventoryTrail loads the append-only review and lifecycle entries.
func (s Service) InventoryTrail(ctx context.Context, tenantID, versionID identity.ID) (TrailView, error) {
	var out TrailView
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var v database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=? AND has_inventory", tenantID, versionID).First(&v).Error; err != nil {
			return apperror.New(apperror.NotFound, "originVersionId", "inventory version not found")
		}
		if err := tx.Where("tenant_id=? AND origin_version_id=?", tenantID, versionID).Order("occurred_at,id").Find(&out.Decisions).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND origin_version_id=? AND origin='AI' AND suggested IS NOT NULL", tenantID, versionID).Order("created_at,id").Find(&out.Suggestions).Error; err != nil {
			return err
		}
		out.SuggestionEvidence = make(map[identity.ID][]identity.ID, len(out.Suggestions))
		itemIDs := make([]identity.ID, 0, len(out.Suggestions))
		for _, item := range out.Suggestions {
			itemIDs = append(itemIDs, item.ID)
		}
		if len(itemIDs) > 0 {
			var evidence []database.InventoryItemEvidence
			if err := tx.Where("tenant_id=? AND item_id IN ?", tenantID, itemIDs).Order("item_id,position,id").Find(&evidence).Error; err != nil {
				return err
			}
			for _, row := range evidence {
				out.SuggestionEvidence[row.ItemID] = append(out.SuggestionEvidence[row.ItemID], row.MediaID)
			}
		}
		return tx.Where("tenant_id=? AND origin_version_id=?", tenantID, versionID).Order("created_at,id").Find(&out.CounterpartyEvents).Error
	})
	return out, err
}

// loadInventoryViews assembles projections with set-based reads so ordering and
// availability calculation do not issue database queries from sort callbacks.
func loadInventoryViews(tx *gorm.DB, tenant identity.ID, envs []database.InventoryEnvironment, customer bool, draft *DraftView, baseline *[]EnvironmentView) error {
	views := make([]EnvironmentView, len(envs))
	environmentIDs := make([]identity.ID, 0, len(envs))
	for i, env := range envs {
		views[i] = EnvironmentView{Environment: env}
		environmentIDs = append(environmentIDs, env.ID)
	}
	if len(environmentIDs) == 0 {
		if baseline != nil {
			*baseline = views
		}
		return nil
	}
	var items []database.InventoryItem
	q := tx.Where("tenant_id=? AND environment_id IN ? AND merged_into_id IS NULL", tenant, environmentIDs)
	if customer {
		q = q.Where("decision IN ('ACCEPTED','EDITED','ADDED')")
	}
	if err := q.Order("environment_id,created_at,id").Find(&items).Error; err != nil {
		return err
	}
	itemIDs := make([]identity.ID, 0, len(items))
	for _, item := range items {
		itemIDs = append(itemIDs, item.ID)
	}
	var evidence []database.InventoryItemEvidence
	if len(itemIDs) > 0 {
		if err := tx.Where("tenant_id=? AND item_id IN ?", tenant, itemIDs).Order("item_id,position,id").Find(&evidence).Error; err != nil {
			return err
		}
	}
	availability, err := LoadEvidenceAvailability(tx, tenant, evidence)
	if err != nil {
		return err
	}
	retentionAvailability, err := LoadRetentionAvailability(tx, tenant, evidence)
	if err != nil {
		return err
	}
	evidenceByItem := make(map[identity.ID][]EvidenceView, len(itemIDs))
	ranks := make(map[identity.ID]int, len(itemIDs))
	for _, row := range evidence {
		state := availability[row.ID]
		evidenceByItem[row.ItemID] = append(evidenceByItem[row.ItemID], EvidenceView{Evidence: row, Availability: state, RetentionAvailability: retentionAvailability[row.ID]})
		if state != "AVAILABLE" {
			ranks[row.ItemID] = 0
		} else if _, exists := ranks[row.ItemID]; !exists {
			ranks[row.ItemID] = 1
		}
	}
	position := make(map[identity.ID]int, len(envs))
	for i, env := range envs {
		position[env.ID] = i
	}
	itemsByEnv := make(map[identity.ID][]database.InventoryItem, len(envs))
	for _, item := range items {
		itemsByEnv[item.EnvironmentID] = append(itemsByEnv[item.EnvironmentID], item)
	}
	for _, env := range envs {
		group := itemsByEnv[env.ID]
		if draft != nil {
			sort.SliceStable(group, func(i, j int) bool {
				ri, rj := ranks[group[i].ID], ranks[group[j].ID]
				if ri != rj {
					return ri < rj
				}
				ci, cj := 10001, 10001
				if group[i].ConfidenceBps != nil {
					ci = *group[i].ConfidenceBps
				}
				if group[j].ConfidenceBps != nil {
					cj = *group[j].ConfidenceBps
				}
				if ci != cj {
					return ci < cj
				}
				return group[i].ID.String() < group[j].ID.String()
			})
		}
		for _, item := range group {
			view := ItemView{Item: item, Evidence: evidenceByItem[item.ID]}
			if customer {
				view.Item.Suggested = nil
				view.Item.ConfidenceBps = nil
			}
			views[position[env.ID]].Items = append(views[position[env.ID]].Items, view)
			if draft != nil {
				if item.Decision == "PENDING" {
					draft.Pending++
				} else {
					draft.Decided++
				}
			}
		}
	}
	if draft != nil {
		draft.Environments = views
	}
	if baseline != nil {
		*baseline = views
	}
	return nil
}
