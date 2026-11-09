package handle_classified

import (
	"context"
	"strings"
	"testing"

	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	customerreport "inspection/services/inspection/internal/features/reports/customer/core"
	"inspection/services/inspection/internal/platform/database"
)

// TestTask05R3ReferenceEntryFirstStillValidates reproduces the report context
// order of production: the FIXED_ORIGIN reference payload lists the baseline
// photo first, keyed "origin:<mediaId>" (reportSnapshotContext), and the
// comparison then pairs that same photo. The issued v3 snapshot must validate:
// the room of a pair is the item's room, bound to the current evidence, never
// the key the shared baseline photo was first listed with.
func TestTask05R3ReferenceEntryFirstStillValidates(t *testing.T) {
	f := newT05Fixture(t, t05Living)
	current := f.capture(t, 0, 1, true)
	sofa := f.item("Sofá")
	f.respond(t, firstCurrent, t05Compare(t05Claim(sofa, "PRESENT_UNCHANGED", "conditionAfter", "GOOD")))
	f.run(t, 0)
	var inspection database.Inspection
	var reference database.ReferenceSnapshot
	if err := f.seed.DB.Where("id=?", f.seed.Inspection).Take(&inspection).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.seed.DB.Where("inspection_id=?", f.seed.Inspection).Take(&reference).Error; err != nil {
		t.Fatal(err)
	}
	origin := f.originOf(sofa).String()
	evidence := []reportcore.Evidence{{ID: origin, RequirementKey: "origin:" + origin, Role: "REFERENCE", Description: "Sala na entrada", Availability: "AVAILABLE"}}
	section, err := buildComparisonReport(context.Background(), f.seed.DB, f.seed.Tenant, inspection, reference, &evidence)
	if err != nil {
		t.Fatal(err)
	}
	item := itemNamed(t, section, "Sofá")
	if item.Outcome != reportcore.ComparisonUnchanged || len(item.Pairs) != 1 || item.Pairs[0].BaselineEvidenceID != origin || item.Pairs[0].CurrentEvidenceID != current[0].String() {
		t.Fatalf("Sofá = %+v; want UNCHANGED with its pair", item)
	}
	if want := "env:" + f.baseline.Environments[0].IdentityID.String(); item.RoomKey != want {
		t.Fatalf("room key = %q; want the room's capture requirement %q", item.RoomKey, want)
	}
	if err := reportcore.Validate(t05Snapshot(section, evidence)); err != nil {
		t.Fatalf("v3 snapshot with the production reference entry listed first = %v; want valid", err)
	}
}

// Item 8 (review round 3): in a v3 report the inventory section states the
// same per-item results and counts as the comparison. A room whose job left
// no result row counts its items as not observed in both sections, and a
// conclusion the comparison downgraded (its pairs are gone) is never kept as
// "condition changed" in the inventory problems.
func TestTask05R3InventoryAgreesWithComparisonCounts(t *testing.T) {
	kitchen := inventorytest.BaselineEnvironment{Name: "Cozinha", TypeCode: "KITCHEN", Items: []inventorytest.BaselineItem{{Name: "Pia", TypeCode: "SINK", Condition: "GOOD", Evidence: 1}}}
	living := inventorytest.BaselineEnvironment{Name: "Sala", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}, {Name: "Mesa", TypeCode: "TABLE", Condition: "GOOD", Evidence: 1}, {Name: "Cadeira", TypeCode: "CHAIR", Condition: "POOR", Evidence: 1}, {Name: "Quadro", TypeCode: "OTHER", Condition: "GOOD", Declared: true}}}
	f := newT05Fixture(t, living, kitchen)
	f.capture(t, 0, 1, true)
	f.respond(t, firstCurrent, t05Compare(
		t05Claim(f.item("Sofá"), "PRESENT_UNCHANGED", "conditionAfter", "GOOD"),
		t05Claim(f.item("Mesa"), "CONDITION_CHANGED", "conditionAfter", "POOR"),
		t05Claim(f.item("Cadeira"), "CONDITION_CHANGED", "conditionAfter", "GOOD"),
	))
	job := f.run(t, 0)
	// The Mesa conclusion loses its pair: the comparison downgrades it.
	if err := f.seed.DB.Where("job_id=? AND item_identity_id=?", job.ID, f.item("Mesa").IdentityID).Delete(&database.InventoryEvidencePair{}).Error; err != nil {
		t.Fatal(err)
	}
	// The kitchen job never ran: no result row for the sink.
	comparison, evidence := f.section(t)
	inventory := f.inventory(t, &evidence)
	alignInventoryWithComparison(inventory, comparison)
	want := reportcore.ComparisonCounts{Total: 5, Unchanged: 1, Improved: 1, NotObserved: 2, NoBasis: 1}
	if comparison.Counts != want {
		t.Fatalf("comparison counts = %+v; want %+v", comparison.Counts, want)
	}
	coverage := inventory.Coverage
	if coverage.ExpectedItems != want.Total || coverage.NotObserved != want.NotObserved || coverage.NoBasis != want.NoBasis || coverage.AssessedItems != want.Unchanged+want.Changed+want.Improved+want.Missing {
		t.Fatalf("inventory coverage = %+v; want the comparison counts %+v", coverage, want)
	}
	statusOf := map[string]string{}
	for _, entry := range append(append([]reportcore.InventoryEntry{}, inventory.Problems...), inventory.Informational...) {
		if entry.ItemIdentityID != "" {
			statusOf[entry.Name] = entry.Status
		}
	}
	for name, status := range map[string]string{"Mesa": "NOT_OBSERVED", "Pia": "NOT_OBSERVED", "Cadeira": "IMPROVED", "Quadro": reportcore.InventoryNoBasis} {
		if statusOf[name] != status {
			t.Errorf("inventory %s = %q; want %q (comparison %s)", name, statusOf[name], status, itemNamed(t, comparison, name).Outcome)
		}
	}
	if _, listed := statusOf["Sofá"]; listed {
		t.Error("an unchanged item is listed in the exception-first inventory")
	}
	for _, entry := range inventory.Problems {
		if entry.Status == "CONDITION_CHANGED" {
			t.Fatalf("a downgraded conclusion stayed a problem: %+v", entry)
		}
	}
	snapshot := t05Snapshot(comparison, evidence)
	snapshot.Inventory = inventory
	if err := reportcore.Validate(snapshot); err != nil {
		t.Fatalf("aligned snapshot is invalid: %v", err)
	}
	view := customerreport.MapInventory(snapshot)
	if view.Coverage.NotObserved != want.NotObserved || view.Coverage.NoBasis != want.NoBasis || view.Coverage.ExpectedItems != want.Total {
		t.Fatalf("customer inventory coverage = %+v; want the comparison counts", view.Coverage)
	}
}

// Item 9 (review round 3): an item the baseline shows without an assessable
// condition is "no basis to compare" with its own limitation, distinct from
// an item without a baseline view; neither states a baseline condition on
// any surface.
func TestTask05R3NoBasisNeverStatesBaselineCondition(t *testing.T) {
	living := inventorytest.BaselineEnvironment{Name: "Sala", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{{Name: "Sofá", TypeCode: "SOFA", Condition: "NOT_ASSESSABLE", Evidence: 1}, {Name: "Quadro", TypeCode: "OTHER", Condition: "GOOD", Declared: true}}}
	f := newT05Fixture(t, living)
	f.capture(t, 0, 1, true)
	f.respond(t, firstCurrent, t05Compare(t05Claim(f.item("Sofá"), "PRESENT_UNCHANGED", "conditionAfter", "GOOD")))
	f.run(t, 0)
	comparison, evidence := f.section(t)
	sofa, frame := itemNamed(t, comparison, "Sofá"), itemNamed(t, comparison, "Quadro")
	if sofa.Outcome != reportcore.ComparisonNoBasis || len(sofa.Limitations) != 1 || sofa.Limitations[0] != reportcore.LimitationNoBaselineCondition {
		t.Fatalf("Sofá = %+v; want no basis because the baseline condition is not assessable", sofa)
	}
	if frame.Outcome != reportcore.ComparisonNoBasis || len(frame.Limitations) != 1 || frame.Limitations[0] != reportcore.LimitationNoBaselineView {
		t.Fatalf("Quadro = %+v; want no basis because the baseline does not show it", frame)
	}
	for _, item := range []reportcore.ComparisonItem{sofa, frame} {
		if item.BaselineCondition != "" || strings.Contains(reportcore.ComparisonItemLine(item), "Condição na referência") {
			t.Fatalf("%s states a baseline condition: %+v / %q", item.Name, item, reportcore.ComparisonItemLine(item))
		}
	}
	snapshot := t05Snapshot(comparison, evidence)
	html, err := reportcore.HTML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), "Sem base para comparar · Condição na referência") {
		t.Fatalf("issued HTML states the baseline condition of a no-basis item")
	}
	view, err := customerreport.ProjectComparison(snapshot, reportcore.ComparisonNoBasis)
	if err != nil || view.Displayed != 2 {
		t.Fatalf("customer no-basis filter = %+v, %v", view, err)
	}
	for _, item := range view.Items {
		if item.BaselineCondition != "" {
			t.Fatalf("customer no-basis item states a baseline condition: %+v", item)
		}
	}
	// A legacy line of an already-issued item keeps no baseline condition
	// either.
	if line := reportcore.ComparisonItemLine(reportcore.ComparisonItem{Room: "Sala", Name: "Sofá", Outcome: reportcore.ComparisonNoBasis, BaselineCondition: "GOOD"}); strings.Contains(line, "Condição na referência") {
		t.Fatalf("no-basis line = %q", line)
	}
}
