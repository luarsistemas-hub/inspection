package render_pdf

import (
	"testing"

	"inspection/services/inspection/internal/features/reports/core"
)

// Item 5 (round 9): the customer copy of a v3 comparative inventory keeps the
// decision recorded for each evidence pair. NOT_COMPARABLE and UNCERTAIN pairs
// must not be presented as SAME_VIEW, and no pair is dropped (the staff PDF and
// the customer screen show the same pairs).
func TestR9CustomerProjectionKeepsOriginalPairDecisions(t *testing.T) {
	snapshot := completeV3Snapshot()
	ms := int64(800)
	snapshot.Evidence = append(snapshot.Evidence,
		core.Evidence{ID: "baseline-table", RequirementKey: "env:living", Role: "REFERENCE", Description: "Mesa na entrada", Availability: "AVAILABLE"},
		core.Evidence{ID: "current-table", RequirementKey: "env:living", Role: "CURRENT", Description: "Mesa atual", Availability: "AVAILABLE"},
	)
	snapshot.Inventory.Problems[1].Pairs = []core.InventoryEvidencePair{
		{Origin: "baseline-table", Current: "current-table", Decision: "NOT_COMPARABLE"},
		{Origin: "baseline-table", Current: "current-photo", Decision: "UNCERTAIN", TimestampMS: &ms},
	}
	if err := core.Validate(snapshot); err != nil {
		t.Fatalf("fixture invalid: %v", err)
	}
	projected, internal, err := projectForAudience(snapshot, "PDF_CUSTOMER")
	if err != nil || internal {
		t.Fatalf("customer projection failed: internal=%v err=%v", internal, err)
	}
	want := map[string][]string{"Sofá": {"SAME_VIEW"}, "Mesa": {"NOT_COMPARABLE", "UNCERTAIN"}}
	for _, entry := range projected.Inventory.Problems {
		expected, ok := want[entry.Name]
		if !ok {
			continue
		}
		if len(entry.Pairs) != len(expected) {
			t.Fatalf("%s pairs=%+v; want %d pairs (none dropped)", entry.Name, entry.Pairs, len(expected))
		}
		for i, pair := range entry.Pairs {
			if pair.Decision != expected[i] {
				t.Fatalf("%s pair %d decision=%q; want the original %q", entry.Name, i, pair.Decision, expected[i])
			}
		}
		delete(want, entry.Name)
	}
	if len(want) != 0 {
		t.Fatalf("customer projection lost entries %v", want)
	}
	if projected.Inventory.Problems[0].ItemIdentityID != "" {
		t.Fatal("customer projection kept an internal item identity")
	}
}
