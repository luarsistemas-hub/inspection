package classify_inspection

import (
	"testing"

	analysis "inspection/services/inspection/internal/features/analysis/core"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
)

// Item 2 (review round 2): an item without baseline basis is never counted
// as "not observed" by the report classification; a real gap still is.
func TestTask05R2NoBasisIsNotCountedAsNotObserved(t *testing.T) {
	seed := inventorytest.New(t, inventorytest.InspectionOptions{})
	baseline := seed.PinBaseline(t, room)
	job := seed.Job(t, "COMPARE", inventorytest.EnvironmentKey(baseline.Environments[0]), baseline.Version, &baseline.Environments[0].IdentityID)
	finish(t, seed, job, "SUCCEEDED", result(baseline.Items[0], "PRESENT_UNCHANGED", ""), result(baseline.Items[1], "NOT_OBSERVED", "NO_BASELINE_VIEW"))
	facts := inventoryFacts(t, seed).Facts
	if facts != (analysis.InventoryFacts{BaselineItems: 2}) {
		t.Fatalf("facts = %+v; no basis must not be counted as not observed", facts)
	}

	other := inventorytest.New(t, inventorytest.InspectionOptions{})
	otherBaseline := other.PinBaseline(t, room)
	otherJob := other.Job(t, "COMPARE", inventorytest.EnvironmentKey(otherBaseline.Environments[0]), otherBaseline.Version, &otherBaseline.Environments[0].IdentityID)
	finish(t, other, otherJob, "SUCCEEDED", result(otherBaseline.Items[0], "NOT_OBSERVED", "NO_CORRESPONDING_VIEW"), result(otherBaseline.Items[1], "NOT_OBSERVED", "NO_BASELINE_VIEW"))
	if facts := inventoryFacts(t, other).Facts; facts.NotObserved != 1 {
		t.Fatalf("facts = %+v; want exactly the item without a current view as not observed", facts)
	}
}
