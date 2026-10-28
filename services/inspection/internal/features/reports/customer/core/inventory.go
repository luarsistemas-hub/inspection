package core

import reportcore "inspection/services/inspection/internal/features/reports/core"

// InventoryView is the customer-safe, allowlisted inventory projection.
type InventoryView struct {
	ReferenceVersionNumber int
	Purpose                string
	Coverage               *InventoryCoverage
	Problems               []InventoryEntry
	Informational          []InventoryEntry
}

// InventoryCoverage contains aggregate coverage only, without internal IDs.
type InventoryCoverage struct {
	ExpectedItems, AssessedItems, NotObserved               int
	EnvironmentsNotCaptured, EnvironmentsAddedDuringCapture []string
}

// InventoryEntry omits item identity and matcher confidence/decision fields.
type InventoryEntry struct {
	Environment, Type, Name, Status         string
	ConditionBefore, ConditionAfter, Reason *string
	Pairs                                   []InventoryPair
}

// InventoryPair exposes only the evidence references needed to display a pair.
type InventoryPair struct {
	OriginEvidenceID, CurrentEvidenceID                 string
	OriginSourceAvailability, CurrentSourceAvailability string
	TimestampMS                                         *int64
}

// MapInventory projects a v2 inventory section through an explicit customer allowlist.
func MapInventory(snapshot reportcore.Snapshot) *InventoryView {
	if snapshot.SchemaVersion != 2 || snapshot.Inventory == nil {
		return nil
	}
	view := &InventoryView{ReferenceVersionNumber: snapshot.Inventory.Reference.VersionNumber, Purpose: snapshot.Inventory.Reference.Purpose}
	evidenceByID := make(map[string]reportcore.Evidence, len(snapshot.Evidence))
	for _, evidence := range snapshot.Evidence {
		evidenceByID[evidence.ID] = evidence
	}
	view.Problems = mapEntriesWithAvailability(snapshot.Inventory.Problems, evidenceByID)
	view.Informational = mapEntriesWithAvailability(snapshot.Inventory.Informational, evidenceByID)
	if coverage := snapshot.Inventory.Coverage; coverage != nil {
		view.Coverage = &InventoryCoverage{ExpectedItems: coverage.ExpectedItems, AssessedItems: coverage.AssessedItems, NotObserved: coverage.NotObserved, EnvironmentsNotCaptured: append([]string(nil), coverage.EnvironmentsNotCaptured...), EnvironmentsAddedDuringCapture: append([]string(nil), coverage.EnvironmentsAddedDuringCapture...)}
	}
	return view
}

func mapEntriesWithAvailability(entries []reportcore.InventoryEntry, evidence map[string]reportcore.Evidence) []InventoryEntry {
	result := mapEntries(entries)
	for i, entry := range entries {
		for j, pair := range entry.Pairs {
			origin, current := evidence[pair.Origin], evidence[pair.Current]
			originAvailability := origin.SourceAvailability
			if originAvailability == "" {
				originAvailability = origin.Availability
			}
			currentAvailability := current.SourceAvailability
			if currentAvailability == "" {
				currentAvailability = current.Availability
			}
			result[i].Pairs[j].OriginSourceAvailability = safeAvailability(originAvailability)
			result[i].Pairs[j].CurrentSourceAvailability = safeAvailability(currentAvailability)
		}
	}
	return result
}

func safeAvailability(value string) string {
	switch value {
	case "AVAILABLE", "EXPIRED", "MISSING", "SENSITIVE_BLOCKED":
		return value
	default:
		return "AVAILABLE"
	}
}

func mapEntries(entries []reportcore.InventoryEntry) []InventoryEntry {
	result := make([]InventoryEntry, 0, len(entries))
	for _, entry := range entries {
		mapped := InventoryEntry{Environment: entry.Environment, Type: entry.Type, Name: entry.Name, Status: entry.Status, ConditionBefore: optionalText(entry.ConditionBefore), ConditionAfter: optionalText(entry.ConditionAfter), Reason: optionalText(entry.Reason), Pairs: make([]InventoryPair, 0, len(entry.Pairs))}
		for _, pair := range entry.Pairs {
			mapped.Pairs = append(mapped.Pairs, InventoryPair{OriginEvidenceID: pair.Origin, CurrentEvidenceID: pair.Current, TimestampMS: pair.TimestampMS})
		}
		result = append(result, mapped)
	}
	return result
}

func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
