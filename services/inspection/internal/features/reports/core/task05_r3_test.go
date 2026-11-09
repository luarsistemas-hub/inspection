package core

import "testing"

// Item 1 (review round 3): the room of a pair is the item's room (RoomKey),
// bound to its CURRENT evidence. A baseline photo listed first under another
// key (the "origin:<mediaId>" entry of a FIXED_ORIGIN reference payload) does
// not make a same-room pair "cross rooms"; a current view of another room,
// CURRENT↔CURRENT pairs, one evidence on both sides and reversed roles are
// still rejected, for staff and customer copies.
func TestTask05R3PairRoomIsTheItemRoom(t *testing.T) {
	build := func(evidence []Evidence, pairs ...ComparisonPair) Snapshot {
		s := Snapshot{SchemaVersion: 3, Evidence: evidence, Comparison: &ComparisonSection{Baseline: ComparisonBaseline{VersionID: "v", VersionNumber: 1, Purpose: "MOVE_IN", ActivatedAt: "2026-01-01T00:00:00Z"}, Status: "COMPLETED", Items: []ComparisonItem{{Room: "Sala", RoomKey: "env:sala", Name: "Sofá", Outcome: ComparisonUnchanged, Pairs: pairs}}}}
		s.Comparison.Counts = CountComparison(s.Comparison.Items)
		return s
	}
	customerCopy := func(s Snapshot) Snapshot {
		section := *s.Comparison
		section.Baseline.VersionID = ""
		s.Comparison = &section
		return s
	}
	originEntry := Evidence{ID: "ref", RequirementKey: "origin:ref", Role: "REFERENCE"}
	reference := Evidence{ID: "ref", RequirementKey: "env:sala", Role: "REFERENCE"}
	current := Evidence{ID: "cur", RequirementKey: "env:sala", Role: "CURRENT"}
	otherCurrent := Evidence{ID: "other", RequirementKey: "env:sala", Role: "CURRENT"}
	kitchen := Evidence{ID: "kitchen", RequirementKey: "env:cozinha", Role: "CURRENT"}
	pair := ComparisonPair{BaselineEvidenceID: "ref", CurrentEvidenceID: "cur"}
	valid := map[string]Snapshot{
		"reference payload entry listed first": build([]Evidence{originEntry, current, reference}, pair),
		"baseline photo keyed by its room":     build([]Evidence{reference, current}, pair),
	}
	for name, snapshot := range valid {
		for _, s := range []Snapshot{snapshot, customerCopy(snapshot)} {
			if err := validateComparison(s, s.Comparison.Baseline.VersionID == ""); err != nil {
				t.Errorf("%s rejected: %v", name, err)
			}
		}
	}
	invalid := map[string]Snapshot{
		"current view of another room":   build([]Evidence{originEntry, kitchen}, ComparisonPair{BaselineEvidenceID: "ref", CurrentEvidenceID: "kitchen"}),
		"second pair in another room":    build([]Evidence{originEntry, current, kitchen}, pair, ComparisonPair{BaselineEvidenceID: "ref", CurrentEvidenceID: "kitchen"}),
		"CURRENT to CURRENT":             build([]Evidence{current, otherCurrent}, ComparisonPair{BaselineEvidenceID: "cur", CurrentEvidenceID: "other"}),
		"same evidence on both sides":    build([]Evidence{current}, ComparisonPair{BaselineEvidenceID: "cur", CurrentEvidenceID: "cur"}),
		"reversed roles":                 build([]Evidence{originEntry, current}, ComparisonPair{BaselineEvidenceID: "cur", CurrentEvidenceID: "ref"}),
		"reference evidence as current":  build([]Evidence{originEntry, {ID: "ref2", RequirementKey: "env:sala", Role: "REFERENCE"}}, ComparisonPair{BaselineEvidenceID: "ref", CurrentEvidenceID: "ref2"}),
		"pair references unknown images": build([]Evidence{current}, pair),
	}
	for name, snapshot := range invalid {
		for _, s := range []Snapshot{snapshot, customerCopy(snapshot)} {
			if err := validateComparison(s, s.Comparison.Baseline.VersionID == ""); err == nil {
				t.Errorf("%s (customer=%v) accepted", name, s.Comparison.Baseline.VersionID == "")
			}
		}
	}
}
