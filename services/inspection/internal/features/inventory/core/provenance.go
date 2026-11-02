package core

import (
	"sort"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// Source kinds recorded in inventory.item_attribute_sources.
const (
	// SourceVisual marks an attribute supported by retained visual evidence.
	SourceVisual = "VISUAL"
	// SourceVoice marks an attribute declared by one exact transcript revision.
	SourceVoice = "VOICE"
	// SourceHumanReview marks an attribute a person explicitly reviewed.
	SourceHumanReview = "HUMAN_REVIEW"
)

// patchedAttributes names the attributes an edit patch changes.
func patchedAttributes(p ItemPatch) []string {
	var out []string
	add := func(changed bool, name string) {
		if changed {
			out = append(out, name)
		}
	}
	add(p.EnvironmentID != nil, "environment")
	add(p.TypeCode != nil, "typeCode")
	add(p.Name != nil, "name")
	add(p.Material != nil, "material")
	add(p.Color != nil, "color")
	add(p.Quantity != nil || p.ClearQuantity, "quantity")
	add(p.Condition != nil, "condition")
	add(p.Observation != nil, "observation")
	add(p.Limitation != nil, "limitation")
	return out
}

// RecordHumanReview appends a HUMAN_REVIEW source for every attribute of the
// item that has a visual or voice source, and for each extra attribute a
// reviewer changed. It never removes the sources it reviews, so the earlier
// signal stays traceable, and an attribute already reviewed is not repeated.
// The decision itself is audited by the caller.
func RecordHumanReview(tx *gorm.DB, item database.InventoryItem, reviewer identity.ID, extra []string, now time.Time) error {
	var rows []database.InventoryItemAttributeSource
	if err := tx.Select("attribute_name,source_kind").Where("tenant_id=? AND item_id=?", item.TenantID, item.ID).Find(&rows).Error; err != nil {
		return err
	}
	reviewed := map[string]bool{}
	wanted := map[string]bool{}
	for _, row := range rows {
		if row.SourceKind == SourceHumanReview {
			reviewed[row.AttributeName] = true
			continue
		}
		wanted[row.AttributeName] = true
	}
	for _, name := range extra {
		wanted[name] = true
	}
	names := make([]string, 0, len(wanted))
	for name := range wanted {
		if !reviewed[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		row := database.InventoryItemAttributeSource{ID: identity.NewID(), TenantID: item.TenantID, ItemID: item.ID, OriginVersionID: item.OriginVersionID, AttributeName: name, SourceKind: SourceHumanReview, ReviewedBy: &reviewer, CreatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

// UndecidedVoiceItems reports, for each given item, whether any voice-backed
// attribute still lacks an explicit human review. It reads once for all items.
func UndecidedVoiceItems(tx *gorm.DB, tenantID identity.ID, itemIDs []identity.ID) (map[identity.ID]bool, error) {
	out := make(map[identity.ID]bool, len(itemIDs))
	if len(itemIDs) == 0 {
		return out, nil
	}
	var pending []identity.ID
	err := tx.Model(&database.InventoryItemAttributeSource{}).
		Distinct("item_id").
		Where("tenant_id=? AND item_id IN ? AND source_kind='VOICE'", tenantID, itemIDs).
		Where("NOT EXISTS (SELECT 1 FROM inventory.item_attribute_sources reviewed WHERE reviewed.tenant_id=item_attribute_sources.tenant_id AND reviewed.item_id=item_attribute_sources.item_id AND reviewed.attribute_name=item_attribute_sources.attribute_name AND reviewed.source_kind='HUMAN_REVIEW')").
		Pluck("item_id", &pending).Error
	for _, id := range pending {
		out[id] = true
	}
	return out, err
}

// VoicePendingCount counts the pending items of a version that still hold a
// voice-backed attribute without human review, among the items that count for
// the inventory (not merged away, in an environment that is not excluded). It
// is reported next to, and separately from, the generic not-reviewed count.
func VoicePendingCount(tx *gorm.DB, tenantID, versionID identity.ID) (int, error) {
	var count int64
	err := tx.Model(&database.InventoryItem{}).
		Joins("JOIN origins.inventory_environments e ON e.tenant_id=inventory_items.tenant_id AND e.id=inventory_items.environment_id AND e.origin_version_id=inventory_items.origin_version_id AND e.decision<>'REJECTED'").
		Where("inventory_items.tenant_id=? AND inventory_items.origin_version_id=? AND inventory_items.decision='PENDING' AND inventory_items.merged_into_id IS NULL", tenantID, versionID).
		Where("EXISTS (SELECT 1 FROM inventory.item_attribute_sources voice WHERE voice.tenant_id=inventory_items.tenant_id AND voice.item_id=inventory_items.id AND voice.source_kind='VOICE' AND NOT EXISTS (SELECT 1 FROM inventory.item_attribute_sources reviewed WHERE reviewed.tenant_id=voice.tenant_id AND reviewed.item_id=voice.item_id AND reviewed.attribute_name=voice.attribute_name AND reviewed.source_kind='HUMAN_REVIEW'))").
		Count(&count).Error
	return int(count), err
}

// LoadAttributeSources reads the provenance of many items at once, ordered by
// item, attribute and creation, so projections never query per item.
func LoadAttributeSources(tx *gorm.DB, tenantID identity.ID, itemIDs []identity.ID) (map[identity.ID][]database.InventoryItemAttributeSource, error) {
	out := make(map[identity.ID][]database.InventoryItemAttributeSource, len(itemIDs))
	if len(itemIDs) == 0 {
		return out, nil
	}
	var rows []database.InventoryItemAttributeSource
	if err := tx.Where("tenant_id=? AND item_id IN ?", tenantID, itemIDs).Order("item_id,attribute_name,created_at,id").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ItemID] = append(out[row.ItemID], row)
	}
	return out, nil
}

// carryVoiceSources keeps every note citation of the merged-away items on the
// merged item, so no spoken signal is lost and no single note is counted
// twice, and records the merging person's review of the merged item. The
// merged-away items keep their own sources.
func carryVoiceSources(tx *gorm.DB, merged database.InventoryItem, others []database.InventoryItem, reviewer identity.ID, now time.Time) error {
	ids := make([]identity.ID, 0, len(others)+1)
	ids = append(ids, merged.ID)
	for _, other := range others {
		ids = append(ids, other.ID)
	}
	all, err := LoadAttributeSources(tx, merged.TenantID, ids)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, row := range all[merged.ID] {
		if row.SourceKind == SourceVoice && row.NoteID != nil && row.RevisionID != nil {
			have[row.AttributeName+"|"+row.NoteID.String()+"|"+row.RevisionID.String()] = true
		}
	}
	for _, other := range others {
		for _, row := range all[other.ID] {
			if row.SourceKind != SourceVoice || row.NoteID == nil || row.RevisionID == nil {
				continue
			}
			key := row.AttributeName + "|" + row.NoteID.String() + "|" + row.RevisionID.String()
			if have[key] {
				continue
			}
			have[key] = true
			copied := database.InventoryItemAttributeSource{ID: identity.NewID(), TenantID: merged.TenantID, ItemID: merged.ID, OriginVersionID: merged.OriginVersionID, AttributeName: row.AttributeName, SourceKind: SourceVoice, NoteID: row.NoteID, RevisionID: row.RevisionID, CreatedAt: now}
			if err := tx.Create(&copied).Error; err != nil {
				return err
			}
		}
	}
	return RecordHumanReview(tx, merged, reviewer, nil, now)
}
