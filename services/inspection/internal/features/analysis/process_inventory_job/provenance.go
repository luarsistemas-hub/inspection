package process_inventory_job

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"inspection/libs/identity"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// sourceBatchSize bounds one insert of attribute sources.
const sourceBatchSize = 200

// catalogSourceRows maps generated items to their per-attribute provenance.
//
// Speech and images stay separate: a voice citation yields one VOICE row per
// declared attribute, citing the exact note and revision the manifest fixed. An
// item with retained visual evidence also yields VISUAL rows citing each of its
// evidence rows, for its identity and for every attribute that carries a value
// and that no note declared. Nearby frames of a note interval are never proof,
// so an item without evidence of its own yields no VISUAL row at all.
func catalogSourceRows(items []database.InventoryItem, evidence []database.InventoryItemEvidence, now time.Time) []database.InventoryItemAttributeSource {
	byItem := make(map[identity.ID][]database.InventoryItemEvidence, len(items))
	for _, row := range evidence {
		byItem[row.ItemID] = append(byItem[row.ItemID], row)
	}
	var out []database.InventoryItemAttributeSource
	for _, item := range items {
		declared := map[string]bool{}
		seen := map[string]bool{}
		for _, source := range ExtractVoiceSources(item) {
			key := source.Attribute + "|" + source.NoteID.String() + "|" + source.RevisionID.String()
			declared[source.Attribute] = true
			if seen[key] {
				continue
			}
			seen[key] = true
			note, revision := source.NoteID, source.RevisionID
			out = append(out, database.InventoryItemAttributeSource{ID: identity.NewID(), TenantID: item.TenantID, ItemID: item.ID, OriginVersionID: item.OriginVersionID, AttributeName: source.Attribute, SourceKind: inventorycore.SourceVoice, NoteID: &note, RevisionID: &revision, CreatedAt: now})
		}
		rows := byItem[item.ID]
		if len(rows) == 0 {
			continue
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Position < rows[j].Position })
		for _, attribute := range visualAttributes(item, declared) {
			for _, row := range rows {
				visual := row.ID
				out = append(out, database.InventoryItemAttributeSource{ID: identity.NewID(), TenantID: item.TenantID, ItemID: item.ID, OriginVersionID: item.OriginVersionID, AttributeName: attribute, SourceKind: inventorycore.SourceVisual, VisualEvidenceID: &visual, CreatedAt: now})
			}
		}
	}
	return out
}

// visualAttributes lists the attributes the item's own images support: its
// identity always, plus each valued attribute that speech did not declare.
func visualAttributes(item database.InventoryItem, declaredByVoice map[string]bool) []string {
	attributes := []string{"name"}
	add := func(has bool, name string) {
		if has && !declaredByVoice[name] {
			attributes = append(attributes, name)
		}
	}
	add(item.Quantity != nil, "quantity")
	add(item.Material != nil && strings.TrimSpace(*item.Material) != "", "material")
	add(item.Color != nil && strings.TrimSpace(*item.Color) != "", "color")
	add(item.Condition != "" && item.Condition != "NOT_ASSESSABLE", "condition")
	return attributes
}

// writeCatalogSources persists the provenance of the generated items.
func writeCatalogSources(tx *gorm.DB, items []database.InventoryItem, evidence []database.InventoryItemEvidence, now time.Time) error {
	rows := catalogSourceRows(items, evidence, now)
	if len(rows) == 0 {
		return nil
	}
	return tx.CreateInBatches(&rows, sourceBatchSize).Error
}

// clearCatalogSources removes the VISUAL and VOICE sources of the pending
// suggestions a catalog write is about to replace. It runs before the
// suggestions are deleted: the item reference blocks removal of the item while
// a source still cites it. Human reviews belong to decided items, which a
// regeneration never replaces, and are never touched here.
func clearCatalogSources(tx *gorm.DB, job database.InventoryJob, pendingItems []identity.ID) error {
	return tx.Where("tenant_id=? AND source_kind IN ('VISUAL','VOICE') AND item_id IN ?", job.TenantID, pendingItems).Delete(&database.InventoryItemAttributeSource{}).Error
}

// reviewedItems summarizes the items of a room that a person already decided.
type reviewedItems struct {
	// keys are the identity keys (type and normalized name) of every reviewed
	// item, rejected ones included: a rejection is not proposed again.
	keys map[string]bool
	// active counts the reviewed items that stay in the inventory.
	active int
}

// loadReviewedItems reads what a regeneration must preserve and not duplicate.
func loadReviewedItems(tx *gorm.DB, job database.InventoryJob, environmentID identity.ID) (reviewedItems, error) {
	var rows []database.InventoryItem
	if err := tx.Select("type_code,name,decision,merged_into_id,suggested").Where("tenant_id=? AND origin_version_id=? AND environment_id=? AND decision<>'PENDING'", job.TenantID, job.OriginVersionID, environmentID).Find(&rows).Error; err != nil {
		return reviewedItems{}, err
	}
	out := reviewedItems{keys: make(map[string]bool, len(rows))}
	for _, row := range rows {
		out.keys[itemKey(row.TypeCode, row.Name)] = true
		// An edited item changed its type or name: what the model originally
		// proposed for it is still the same decided item, not a new suggestion.
		var original struct {
			TypeCode string `json:"typeCode"`
			Name     string `json:"name"`
		}
		if len(row.Suggested) > 0 && json.Unmarshal(row.Suggested, &original) == nil && original.Name != "" {
			out.keys[itemKey(original.TypeCode, original.Name)] = true
		}
		if row.Decision != "REJECTED" && row.MergedIntoID == nil {
			out.active++
		}
	}
	return out, nil
}

// itemKey is the case- and space-insensitive identity of a suggestion.
func itemKey(typeCode, name string) string {
	return typeCode + "|" + strings.ToLower(strings.Join(strings.Fields(name), " "))
}

// withoutReviewed drops the suggestions of a room that match an item a person
// already reviewed, then keeps at most the room's remaining capacity, together
// with the evidence rows of what is dropped. It reports whether it truncated.
func withoutReviewed(items []database.InventoryItem, evidence []database.InventoryItemEvidence, reviewed reviewedItems) ([]database.InventoryItem, []database.InventoryItemEvidence, bool) {
	if len(reviewed.keys) == 0 {
		return items, evidence, false
	}
	capacity := maxItemsPerEnvironment - reviewed.active
	if capacity < 0 {
		capacity = 0
	}
	kept := make([]database.InventoryItem, 0, len(items))
	dropped := map[identity.ID]bool{}
	truncated := false
	for _, item := range items {
		switch {
		case reviewed.keys[itemKey(item.TypeCode, item.Name)]:
			dropped[item.ID] = true
		case len(kept) >= capacity:
			dropped[item.ID] = true
			truncated = true
		default:
			kept = append(kept, item)
		}
	}
	if len(dropped) == 0 {
		return items, evidence, false
	}
	keptEvidence := make([]database.InventoryItemEvidence, 0, len(evidence))
	for _, row := range evidence {
		if !dropped[row.ItemID] {
			keptEvidence = append(keptEvidence, row)
		}
	}
	return kept, keptEvidence, truncated
}
