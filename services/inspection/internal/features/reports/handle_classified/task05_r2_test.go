package handle_classified

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	customerreport "inspection/services/inspection/internal/features/reports/customer/core"
	"inspection/services/inspection/internal/platform/database"
)

func (f *t05Fixture) inventory(t *testing.T, evidence *[]reportcore.Evidence) *reportcore.InventorySection {
	t.Helper()
	var inspection database.Inspection
	var reference database.ReferenceSnapshot
	if err := f.seed.DB.Where("id=?", f.seed.Inspection).Take(&inspection).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.seed.DB.Where("inspection_id=?", f.seed.Inspection).Take(&reference).Error; err != nil {
		t.Fatal(err)
	}
	section, err := buildInventoryReport(context.Background(), f.seed.DB, f.seed.Tenant, inspection, reference, f.seed.Now.Add(time.Hour), evidence)
	if err != nil || section == nil || section.Coverage == nil {
		t.Fatalf("inventory section = %+v, %v", section, err)
	}
	return section
}

// Item 2 (review round 2): "no basis to compare" is never counted or shown as
// "not observed" in any section. The inventory and comparison sections of the
// same issue agree, and each section's counts add up to its items.
func TestTask05R2NoBasisIsNeverNotObservedInAnySection(t *testing.T) {
	env := inventorytest.BaselineEnvironment{Name: "Sala", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}, {Name: "Mesa", TypeCode: "TABLE", Condition: "GOOD", Evidence: 1}, {Name: "Quadro", TypeCode: "OTHER", Condition: "GOOD", Declared: true}}}
	f := newT05Fixture(t, env)
	f.capture(t, 0, 1, true)
	f.respond(t, firstCurrent, t05Compare(t05Claim(f.item("Sofá"), "PRESENT_UNCHANGED", "conditionAfter", "GOOD"), t05Claim(f.item("Mesa"), "NOT_OBSERVED", "reasonCode", "OBSTRUCTED")))
	f.run(t, 0)
	comparison, evidence := f.section(t)
	if comparison.Counts != (reportcore.ComparisonCounts{Total: 3, Unchanged: 1, NotObserved: 1, NoBasis: 1}) {
		t.Fatalf("comparison counts = %+v", comparison.Counts)
	}
	inventory := f.inventory(t, &evidence)
	coverage := inventory.Coverage
	if coverage.ExpectedItems != 3 || coverage.AssessedItems != 1 || coverage.NotObserved != 1 || coverage.NoBasis != 1 || coverage.AssessedItems+coverage.NotObserved+coverage.NoBasis != coverage.ExpectedItems {
		t.Fatalf("inventory coverage = %+v; want 1 assessed, 1 not observed, 1 no basis of 3", coverage)
	}
	for _, entry := range inventory.Problems {
		if entry.Name == "Quadro" {
			t.Fatalf("no-basis item listed as a problem: %+v", entry)
		}
	}
	var frame *reportcore.InventoryEntry
	for i := range inventory.Informational {
		if inventory.Informational[i].Name == "Quadro" {
			frame = &inventory.Informational[i]
		}
	}
	if frame == nil || frame.Status != reportcore.InventoryNoBasis || frame.Reason != reportcore.LimitationNoBaselineView {
		t.Fatalf("no-basis entry = %+v; want status NO_BASIS with its limitation", frame)
	}
	snapshot := t05Snapshot(comparison, evidence)
	snapshot.Inventory = inventory
	if err := reportcore.Validate(snapshot); err != nil {
		t.Fatalf("snapshot with both sections is invalid: %v", err)
	}
	view := customerreport.MapInventory(snapshot)
	if view.Coverage.NotObserved != 1 || view.Coverage.NoBasis != 1 {
		t.Fatalf("customer inventory coverage = %+v", view.Coverage)
	}
	html, err := reportcore.HTML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "1 não observados · 1 sem base para comparar") {
		t.Fatalf("HTML counts do not keep no basis apart:\n%s", html)
	}
}

// Item 3b: a confirmed pair of an item that ended as a gap is never listed,
// so the report cannot show "not observed" next to "baseline E ↔ current E".
func TestTask05R2GapNeverListsAPair(t *testing.T) {
	f := newT05Fixture(t, t05Living)
	f.capture(t, 0, 1, true)
	sofa := f.item("Sofá")
	f.respond(t, firstCurrent, t05Compare(t05Claim(sofa, "NOT_OBSERVED", "reasonCode", "OBSTRUCTED")))
	job := f.run(t, 0)
	var confirmed int64
	if err := f.seed.DB.Model(&database.InventoryEvidencePair{}).Where("job_id=? AND confirmed", job.ID).Count(&confirmed).Error; err != nil || confirmed != 1 {
		t.Fatalf("confirmed pairs = %d, %v; the fixture needs a confirmed pair", confirmed, err)
	}
	section, evidence := f.section(t)
	item := itemNamed(t, section, "Sofá")
	if item.Outcome != reportcore.ComparisonNotObserved || len(item.Pairs) != 0 {
		t.Fatalf("Sofá = %+v; a gap must not list a pair", item)
	}
	if role := evidenceRole(evidence, f.originOf(sofa).String()); role != "" {
		t.Fatalf("the unused baseline image was pinned as %s evidence", role)
	}
}

// Item 8: a paired image keeps its media flags, so a sensitive baseline image
// is unavailable to the customer however it entered the report.
func TestTask05R2PairedEvidenceKeepsSensitiveFlags(t *testing.T) {
	f := newT05Fixture(t, t05Living)
	f.capture(t, 0, 1, true)
	sofa := f.item("Sofá")
	if err := f.seed.DB.Model(&database.MediaObject{}).Where("id=?", f.originOf(sofa)).Update("flags", json.RawMessage(`["SENSITIVE_DETECTION"]`)).Error; err != nil {
		t.Fatal(err)
	}
	f.respond(t, firstCurrent, t05Compare(t05Claim(sofa, "PRESENT_UNCHANGED", "conditionAfter", "GOOD")))
	f.run(t, 0)
	section, evidence := f.section(t)
	origin := f.originOf(sofa).String()
	var flags []string
	for _, item := range evidence {
		if item.ID == origin {
			flags = item.Flags
		}
	}
	if len(flags) != 1 || flags[0] != "SENSITIVE_DETECTION" {
		t.Fatalf("paired baseline flags = %v; want the media flag", flags)
	}
	view, err := customerreport.ProjectComparison(t05Snapshot(section, evidence), "")
	if err != nil {
		t.Fatal(err)
	}
	if pairs := view.Items[0].Pairs; len(pairs) != 1 || pairs[0].BaselineAvailability != "UNAVAILABLE" || pairs[0].CurrentAvailability != "AVAILABLE" {
		t.Fatalf("customer pair = %+v; want the sensitive baseline image unavailable", view.Items[0].Pairs)
	}
}

// Items 1 and 5 through the report: no model sentence reaches any audience,
// whatever the model wrote, and a downgraded conclusion keeps no positive text.
func TestTask05R2ModelTextNeverReachesTheReport(t *testing.T) {
	env := inventorytest.BaselineEnvironment{Name: "Sala", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}, {Name: "Mesa", TypeCode: "TABLE", Condition: "GOOD", Evidence: 1}}}
	f := newT05Fixture(t, env)
	f.capture(t, 0, 1, true)
	phrases := []string{"O morador é responsável pelo dano", "Sofá presente e sem alterações em relação à entrada.", "Reparo estimado em 300 reais", "normal wear"}
	f.respond(t, firstCurrent, t05Compare(
		t05Claim(f.item("Sofá"), "CONDITION_CHANGED", "conditionAfter", "FAIR", "description", phrases[0]),
		// Contradictory: "unchanged" with a worse condition is downgraded.
		t05Claim(f.item("Mesa"), "PRESENT_UNCHANGED", "conditionAfter", "POOR", "description", phrases[1]),
		map[string]any{"status": "OUTSIDE_INVENTORY", "outsideTypeCode": "CHAIR", "outsideName": phrases[2], "description": phrases[3]},
	))
	f.run(t, 0)
	section, evidence := f.section(t)
	mesa := itemNamed(t, section, "Mesa")
	if mesa.Outcome != reportcore.ComparisonNotObserved || len(mesa.Limitations) != 1 || mesa.Limitations[0] != reportcore.LimitationInconsistentClaim {
		t.Fatalf("Mesa = %+v; want the contradictory claim downgraded with its limitation", mesa)
	}
	snapshot := t05Snapshot(section, evidence)
	html, err := reportcore.HTML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, phrase := range phrases {
		if strings.Contains(string(encoded), phrase) || strings.Contains(string(html), phrase) {
			t.Fatalf("model text %q reached the report", phrase)
		}
	}
	if len(section.OutsideBaseline) != 1 || section.OutsideBaseline[0].Description != "" || section.OutsideBaseline[0].Name == phrases[2] {
		t.Fatalf("outside observation = %+v; want the controlled label only", section.OutsideBaseline)
	}
}
