package process_inventory_job

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/features/inventory/vocabulary"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
)

// Item 4 (review round 2): in strict mode the outcome follows the two
// assessable conditions over a confirmed pair; a contradictory claim, an
// unassessable current condition or an unassessable baseline never reads as
// "unchanged". The rows are the exact inputs the review probe reproduced.
func TestTask05R2StrictDeriveRejectsInconsistentInput(t *testing.T) {
	confirmed := ItemFacts{ConditionBefore: "GOOD", Observation: "OBSERVED", Confirmed: true, Strict: true}
	declared := confirmed
	declared.Declared = true
	noBasis := confirmed
	noBasis.ConditionBefore = "NOT_ASSESSABLE"
	cases := []struct {
		name  string
		claim ItemClaim
		facts ItemFacts
		want  ItemOutcome
	}{
		{"unchanged with an unassessable current condition", ItemClaim{Status: "PRESENT_UNCHANGED", ConditionAfter: "NOT_ASSESSABLE"}, confirmed, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: "QUALITY"}},
		{"unchanged without a current condition", ItemClaim{Status: "PRESENT_UNCHANGED"}, confirmed, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: reasonInconsistentClaim}},
		{"changed although both conditions are equal", ItemClaim{Status: "CONDITION_CHANGED", ConditionAfter: "GOOD"}, confirmed, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: reasonInconsistentClaim}},
		{"improved with a worse condition", ItemClaim{Status: "IMPROVED", ConditionAfter: "POOR"}, confirmed, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: reasonInconsistentClaim}},
		{"unchanged with a worse condition", ItemClaim{Status: "PRESENT_UNCHANGED", ConditionAfter: "POOR"}, confirmed, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: reasonInconsistentClaim}},
		{"declared item that worsened (UT-117)", ItemClaim{Status: "CONDITION_CHANGED", ConditionAfter: "POOR"}, declared, ItemOutcome{Status: "CONDITION_CHANGED", ConditionAfter: "POOR"}},
		// Round 3, item 9: a baseline view without an assessable condition is
		// told apart from an item the baseline does not show.
		{"unassessable baseline", ItemClaim{Status: "CONDITION_CHANGED", ConditionAfter: "POOR"}, noBasis, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: reasonBaselineConditionUnassessed}},
		{"prior damage still present (UT-117)", ItemClaim{Status: "PRESENT_UNCHANGED", ConditionAfter: "POOR"}, ItemFacts{ConditionBefore: "POOR", Confirmed: true, Strict: true}, ItemOutcome{Status: "PRESENT_UNCHANGED", ConditionAfter: "POOR"}},
		{"unknown condition code", ItemClaim{Status: "PRESENT_UNCHANGED", ConditionAfter: "EXCELLENT"}, confirmed, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: reasonInconsistentClaim}},
		// The legacy rules are untouched.
		{"legacy declared worsening stays unchanged", ItemClaim{Status: "CONDITION_CHANGED", ConditionAfter: "POOR"}, ItemFacts{ConditionBefore: "GOOD", Declared: true, Confirmed: true}, ItemOutcome{Status: "PRESENT_UNCHANGED", ConditionAfter: "POOR"}},
		{"legacy unchanged without condition", ItemClaim{Status: "PRESENT_UNCHANGED"}, ItemFacts{ConditionBefore: "GOOD", Confirmed: true}, ItemOutcome{Status: "PRESENT_UNCHANGED"}},
	}
	for _, tc := range cases {
		if got := DeriveItemResult(tc.claim, tc.facts); got != tc.want {
			t.Errorf("%s: DeriveItemResult = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// Truth table of the strict rules over every combination of pair, movable,
// room coverage, baseline condition, claim, model reason and current condition
// (including the model's failure to answer, contradictory claims and
// processing reasons that only legacy derivation accepts). Observation,
// environment viewed and declaration are omitted from the 32,928 base rows
// because strict rules use the confirmed pair; each base row separately
// varies those three dimensions across 12 combinations and asserts an exact
// unchanged result. Every base row is checked against the PRD/TechSpec table
// (strictOracle), so a derivation that always answers "not observed" fails
// (review round 3, item 7).
func TestTask05R2StrictDeriveTruthTable(t *testing.T) {
	conditions := []string{"", "NEW", "GOOD", "FAIR", "POOR", "NOT_ASSESSABLE", "BROKEN"}
	claims := []string{"PRESENT_UNCHANGED", "CONDITION_CHANGED", "IMPROVED", "MISSING", "NOT_OBSERVED", "OUTSIDE_INVENTORY", ""}
	// Every reason the model may emit, including an empty reason and the
	// strict-only reasons normalized by the system, plus an unknown value.
	reasons := []string{"", "OBSTRUCTED", "FRAMING", "QUALITY", "NO_CORRESPONDING_VIEW", "ENVIRONMENT_NOT_CAPTURED", "ANALYSIS_FAILED", reasonPairRejected, reasonRoomCoverage, reasonInconsistentClaim, reasonBaselineConditionUnassessed, "BOGUS"}
	combinations, concluded := 0, 0
	for _, confirmed := range []bool{false, true} {
		for _, movable := range []bool{false, true} {
			for _, covered := range []bool{false, true} {
				for _, before := range conditions {
					for _, status := range claims {
						for _, reason := range reasons {
							for _, after := range conditions {
								combinations++
								facts := ItemFacts{ConditionBefore: before, Observation: "OBSERVED", Confirmed: confirmed, Strict: true, Movable: movable, RoomCovered: covered}
								claim := ItemClaim{Status: status, ConditionAfter: after, ReasonCode: reason}
								got, want := DeriveItemResult(claim, facts), strictOracle(claim, facts)
								if got != want {
									t.Fatalf("%s/%q/%s/%s confirmed=%v movable=%v covered=%v: got %+v, want %+v", status, reason, before, after, confirmed, movable, covered, got, want)
								}
								if got.Status != "" && got.Status != "NOT_OBSERVED" {
									concluded++
								}
								for _, declared := range []bool{false, true} {
									for _, observation := range []string{"", "OBSERVED", "NOT_OBSERVED"} {
										for _, viewed := range []bool{false, true} {
											variant := facts
											variant.Declared, variant.Observation, variant.EnvironmentViewed = declared, observation, viewed
											if changed := DeriveItemResult(claim, variant); changed != got {
												t.Fatalf("strict result changed with declared=%v observation=%q viewed=%v: got %+v, want %+v", declared, observation, viewed, changed, got)
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
	if combinations != 32928 || combinations != 2*2*2*len(conditions)*len(claims)*len(reasons)*len(conditions) {
		t.Fatalf("truth table covered %d combinations", combinations)
	}
	if concluded == 0 {
		t.Fatal("the truth table never reached a conclusion")
	}
	// Literal rows anchor the oracle to the decision table itself.
	pair := ItemFacts{ConditionBefore: "GOOD", Observation: "OBSERVED", Confirmed: true, Strict: true}
	fair := pair
	fair.ConditionBefore = "FAIR"
	movableUncovered := pair
	movableUncovered.Movable = true
	fixedUncovered := pair
	declaredWithoutCondition := pair
	declaredWithoutCondition.Declared, declaredWithoutCondition.ConditionBefore = true, ""
	literal := []struct {
		claim ItemClaim
		facts ItemFacts
		want  ItemOutcome
	}{
		{ItemClaim{Status: "PRESENT_UNCHANGED", ConditionAfter: "GOOD"}, pair, ItemOutcome{Status: "PRESENT_UNCHANGED", ConditionAfter: "GOOD"}},
		{ItemClaim{Status: "CONDITION_CHANGED", ConditionAfter: "POOR"}, pair, ItemOutcome{Status: "CONDITION_CHANGED", ConditionAfter: "POOR"}},
		{ItemClaim{Status: "CONDITION_CHANGED", ConditionAfter: "NEW"}, fair, ItemOutcome{Status: "IMPROVED", ConditionAfter: "NEW"}},
		{ItemClaim{Status: "IMPROVED", ConditionAfter: "GOOD"}, fair, ItemOutcome{Status: "IMPROVED", ConditionAfter: "GOOD"}},
		{ItemClaim{Status: "MISSING"}, movableUncovered, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: reasonRoomCoverage}},
		{ItemClaim{Status: "MISSING"}, fixedUncovered, ItemOutcome{Status: "MISSING"}},
		{ItemClaim{Status: "NOT_OBSERVED", ReasonCode: "OBSTRUCTED"}, pair, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: "OBSTRUCTED"}},
		{ItemClaim{Status: "NOT_OBSERVED", ReasonCode: "ENVIRONMENT_NOT_CAPTURED"}, pair, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: "NO_CORRESPONDING_VIEW"}},
		{ItemClaim{Status: "NOT_OBSERVED", ReasonCode: "ANALYSIS_FAILED"}, pair, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: "NO_CORRESPONDING_VIEW"}},
		// System-only reasons sent by the model are never kept: each becomes
		// "no corresponding view", whatever the facts.
		{ItemClaim{Status: "NOT_OBSERVED", ReasonCode: reasonPairRejected}, pair, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: "NO_CORRESPONDING_VIEW"}},
		{ItemClaim{Status: "NOT_OBSERVED", ReasonCode: reasonRoomCoverage}, movableUncovered, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: "NO_CORRESPONDING_VIEW"}},
		{ItemClaim{Status: "NOT_OBSERVED", ReasonCode: reasonInconsistentClaim}, pair, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: "NO_CORRESPONDING_VIEW"}},
		{ItemClaim{Status: "NOT_OBSERVED", ReasonCode: reasonBaselineConditionUnassessed}, declaredWithoutCondition, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: "NO_CORRESPONDING_VIEW"}},
		{ItemClaim{Status: "NOT_OBSERVED", ReasonCode: "NO_BASELINE_VIEW"}, pair, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: "NO_CORRESPONDING_VIEW"}},
		{ItemClaim{Status: "NOT_OBSERVED", ReasonCode: reasonEnvironmentUnmatched}, pair, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: "NO_CORRESPONDING_VIEW"}},
		// A system-only reason never changes a conclusion claim either.
		{ItemClaim{Status: "PRESENT_UNCHANGED", ConditionAfter: "GOOD", ReasonCode: reasonRoomCoverage}, pair, ItemOutcome{Status: "PRESENT_UNCHANGED", ConditionAfter: "GOOD"}},
		{ItemClaim{Status: "MISSING", ReasonCode: reasonPairRejected}, fixedUncovered, ItemOutcome{Status: "MISSING"}},
		{ItemClaim{Status: "PRESENT_UNCHANGED", ConditionAfter: "GOOD"}, declaredWithoutCondition, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: reasonBaselineConditionUnassessed}},
		{ItemClaim{Status: "PRESENT_UNCHANGED", ConditionAfter: "GOOD"}, ItemFacts{ConditionBefore: "GOOD", Strict: true}, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: "NO_CORRESPONDING_VIEW"}},
	}
	for _, row := range literal {
		if got := DeriveItemResult(row.claim, row.facts); got != row.want || strictOracle(row.claim, row.facts) != row.want {
			t.Errorf("%+v %+v: got %+v, oracle %+v, want %+v", row.claim, row.facts, got, strictOracle(row.claim, row.facts), row.want)
		}
	}
}

// strictOracle is the report v3 decision table, written independently of the
// implementation:
//   - a model "not observed" keeps only a reason about the current view
//     (obstructed, framing, quality, no corresponding view); any other reason
//     becomes "no corresponding view";
//   - "missing" needs the item's confirmed pair, and a movable item also the
//     whole room observed (else "room coverage insufficient");
//   - "unchanged", "changed" and "improved" need the item's confirmed pair, an
//     assessable baseline condition (else "baseline condition unassessed"),
//     an assessable current condition ("not assessable" is a quality limit,
//     any other unknown value an inconsistent claim), and the outcome is
//     derived from the two conditions: equal → unchanged, worse → changed,
//     better → improved; "unchanged" and "improved" must match it exactly,
//     "changed" accepts any real change;
//   - any other claim is dropped.
func strictOracle(claim ItemClaim, facts ItemFacts) ItemOutcome {
	no := func(reason string) ItemOutcome { return ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: reason} }
	rank := map[string]int{"NEW": 1, "GOOD": 2, "FAIR": 3, "POOR": 4}
	switch claim.Status {
	case "NOT_OBSERVED":
		switch claim.ReasonCode {
		case "OBSTRUCTED", "FRAMING", "QUALITY", "NO_CORRESPONDING_VIEW":
			return no(claim.ReasonCode)
		}
		return no("NO_CORRESPONDING_VIEW")
	case "MISSING":
		if !facts.Confirmed {
			return no("NO_CORRESPONDING_VIEW")
		}
		if facts.Movable && !facts.RoomCovered {
			return no("ROOM_COVERAGE_INSUFFICIENT")
		}
		return ItemOutcome{Status: "MISSING"}
	case "PRESENT_UNCHANGED", "CONDITION_CHANGED", "IMPROVED":
	default:
		return ItemOutcome{}
	}
	if !facts.Confirmed {
		return no("NO_CORRESPONDING_VIEW")
	}
	before, after := rank[facts.ConditionBefore], rank[claim.ConditionAfter]
	if before == 0 {
		return no("BASELINE_CONDITION_UNASSESSED")
	}
	if claim.ConditionAfter == "NOT_ASSESSABLE" {
		return no("QUALITY")
	}
	if after == 0 {
		return no("INCONSISTENT_CLAIM")
	}
	derived := "PRESENT_UNCHANGED"
	if after > before {
		derived = "CONDITION_CHANGED"
	} else if after < before {
		derived = "IMPROVED"
	}
	if claim.Status == derived || (claim.Status == "CONDITION_CHANGED" && derived != "PRESENT_UNCHANGED") {
		return ItemOutcome{Status: derived, ConditionAfter: claim.ConditionAfter}
	}
	return no("INCONSISTENT_CLAIM")
}

// Items 1 and 5: a strict comparison never stores any model sentence, so a
// downgraded conclusion cannot keep a positive statement and no wording about
// responsibility, wear or cost reaches the report; informational objects keep
// only their controlled type label.
func TestTask05R2StrictResultsCarryNoModelText(t *testing.T) {
	sofa := database.InventoryItem{ID: identity.NewID(), IdentityID: identity.NewID(), TypeCode: "SOFA", Name: "Sofá", Condition: "GOOD", Observation: "OBSERVED"}
	table := database.InventoryItem{ID: identity.NewID(), IdentityID: identity.NewID(), TypeCode: "TABLE", Name: "Mesa", Condition: "GOOD", Observation: "OBSERVED"}
	text := func(s string) *string { return &s }
	id := func(item database.InventoryItem) *string { s := item.IdentityID.String(); return &s }
	poor, outside := "POOR", "SOFA"
	rows := []compareRow{
		// Downgraded: no confirmed pair for the sofa.
		{ItemIdentityID: id(sofa), Status: "PRESENT_UNCHANGED", ConditionAfter: text("GOOD"), Description: text("Sofá presente e sem alterações em relação à entrada.")},
		{ItemIdentityID: id(table), Status: "CONDITION_CHANGED", ConditionAfter: &poor, Description: text("O morador é responsável pelo dano; conserto custará 300 reais.")},
		{Status: "OUTSIDE_INVENTORY", OutsideTypeCode: &outside, OutsideName: text("Sofá com desgaste por mau uso do inquilino"), Description: text("deve ser cobrado do ocupante")},
	}
	pairs := []database.InventoryEvidencePair{{ItemIdentityID: table.IdentityID, Confirmed: true}}
	results := deriveResults(database.InventoryJob{ID: identity.NewID()}, []database.InventoryItem{sofa, table}, rows, pairs, time.Now(), compareRules{strict: true})
	label, _ := vocabulary.ItemLabel(outside)
	seen := 0
	for _, row := range results {
		if row.Description != nil {
			t.Fatalf("strict result %s kept model text %q", row.Status, *row.Description)
		}
		switch {
		case row.Status == "OUTSIDE_INVENTORY":
			if row.OutsideName == nil || *row.OutsideName != label {
				t.Fatalf("outside observation name = %v; want the controlled label %q", row.OutsideName, label)
			}
		case *row.ItemIdentityID == sofa.IdentityID:
			if row.Status != "NOT_OBSERVED" {
				t.Fatalf("sofa without pair = %s; want NOT_OBSERVED", row.Status)
			}
		case *row.ItemIdentityID == table.IdentityID:
			if row.Status != "CONDITION_CHANGED" {
				t.Fatalf("table = %s", row.Status)
			}
		}
		seen++
	}
	if seen != 3 {
		t.Fatalf("results = %d; want three", seen)
	}
	// The legacy comparison keeps its historical text.
	legacy := deriveResults(database.InventoryJob{ID: identity.NewID()}, []database.InventoryItem{table}, rows[1:2], pairs, time.Now(), compareRules{})
	if legacy[0].Description == nil || !strings.Contains(*legacy[0].Description, "responsável") {
		t.Fatalf("legacy description changed: %+v", legacy[0])
	}
}

// Item 9: the executor decides whole-room coverage with the capture's own
// coverage rule. A component-level answer for a component with views, views
// justified as not applicable and impossible components agree with
// capturecore.EvaluateCoverage for every case.
func TestTask05R2RoomCoverageMatchesCaptureCoverage(t *testing.T) {
	room := catalog.ExpectedRoom{Key: "sala", Components: []catalog.ExpectedComponent{{Key: "piso", Views: []string{"wide", "detail"}}, {Key: "janela", Views: []string{"wide"}}}}
	answer := func(component, view, state string) capturecore.ComponentCoverageAnswer {
		return capturecore.ComponentCoverageAnswer{RequirementKey: "env:sala", ComponentKey: component, ViewKey: view, State: state}
	}
	cases := []struct {
		name    string
		answers []capturecore.ComponentCoverageAnswer
		want    bool
	}{
		{"component-level observation of a component with views", []capturecore.ComponentCoverageAnswer{answer("piso", "", "OBSERVED"), answer("janela", "wide", "OBSERVED")}, true},
		{"view not applicable keeps the other view", []capturecore.ComponentCoverageAnswer{answer("piso", "wide", "OBSERVED"), answer("piso", "detail", "NOT_APPLICABLE"), answer("janela", "wide", "OBSERVED")}, true},
		{"every view not applicable", []capturecore.ComponentCoverageAnswer{answer("piso", "wide", "OBSERVED"), answer("piso", "detail", "OBSERVED"), answer("janela", "wide", "NOT_APPLICABLE")}, true},
		{"impossible component", []capturecore.ComponentCoverageAnswer{answer("piso", "", "OBSERVED"), answer("janela", "", "IMPOSSIBLE")}, false},
		{"partial component", []capturecore.ComponentCoverageAnswer{answer("piso", "wide", "OBSERVED"), answer("janela", "wide", "OBSERVED")}, false},
		{"nothing observed", []capturecore.ComponentCoverageAnswer{answer("piso", "", "NOT_APPLICABLE"), answer("janela", "", "NOT_APPLICABLE")}, false},
	}
	for _, tc := range cases {
		got := roomCovered(room, tc.answers)
		summary := capturecore.EvaluateCoverage([]catalog.ExpectedRoom{room}, tc.answers)
		capture := summary.FullyAssessed > 0 && summary.FullyAssessed+summary.NotApplicable == summary.Expected
		if got != tc.want || got != capture {
			t.Errorf("%s: roomCovered = %v, capture coverage = %v (%+v), want %v", tc.name, got, capture, summary, tc.want)
		}
	}
}

// Item 9 through the executor: an OBSERVED answer whose evidence is not a
// ready photo of the capture does not cover the room, so a movable item's
// vacant location stays NOT_OBSERVED instead of MISSING.
func TestTask05R2ObservationWithoutUsableEvidenceDoesNotCoverTheRoom(t *testing.T) {
	for _, usable := range []bool{true, false} {
		f := newCompareFixture(t, 1, living)
		env := f.baseline.Environments[0]
		f.seed.PinScope(t, inventorytest.ScopeRoom{Key: env.IdentityID.String(), Name: env.Name, TypeCode: env.TypeCode, Components: map[string][]string{"geral": {"wide"}}})
		evidence := f.current
		if !usable {
			evidence = []identity.ID{identity.NewID()}
		}
		f.seed.ComponentAnswer(t, "env:"+env.IdentityID.String(), "geral", "", "OBSERVED", evidence)
		f.gateway.Respond = script(t, "SAME_VIEW", "SAME_VIEW", func(llm.StructuredRequest) string {
			return compareJSON(t, claim(f.baseline.Items[0], "MISSING"))
		})
		if err := f.process(t, f.job); err != nil {
			t.Fatal(err)
		}
		row := resultsOf(t, f)[f.baseline.Items[0].IdentityID.String()]
		if usable && row.Status != "MISSING" {
			t.Fatalf("component-level observation with ready evidence: %s %v; want MISSING", row.Status, value(row.ReasonCode))
		}
		if !usable && (row.Status != "NOT_OBSERVED" || value(row.ReasonCode) != reasonRoomCoverage) {
			t.Fatalf("observation without usable evidence: %s %v; want NOT_OBSERVED by room coverage", row.Status, value(row.ReasonCode))
		}
	}
}

// Item 3a: an OPEN recapture created after the submission (or a submission
// superseded by a later one) never feeds a strict comparison: the newest
// SUBMITTED answer of the room is the current evidence, by the same rule the
// report uses.
func TestTask05R2StrictComparisonReadsOnlyTheNewestSubmittedAnswer(t *testing.T) {
	f := strictFixture(t, true, living)
	key := inventorytest.EnvironmentKey(f.baseline.Environments[0])
	submitted := f.current[0]
	// An older submission of the same room, superseded by the fixture's.
	older := f.seed.Photo(t, key)
	olderDraft := identity.NewID()
	draft := func(id identity.ID, status string, at time.Time, media identity.ID) {
		t.Helper()
		var base database.CaptureDraft
		if err := f.seed.DB.Where("id=?", f.seed.CaptureDraft).Take(&base).Error; err != nil {
			t.Fatal(err)
		}
		base.ID, base.Status, base.CreatedAt, base.UpdatedAt = id, status, at, at
		if err := f.seed.DB.Create(&base).Error; err != nil {
			t.Fatal(err)
		}
		ids, _ := json.Marshal([]identity.ID{media})
		if err := f.seed.DB.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: f.seed.Tenant, DraftID: id, RequirementKey: key, MediaIDs: ids, Flags: json.RawMessage(`[]`), Version: 1, CreatedAt: at, UpdatedAt: at}).Error; err != nil {
			t.Fatal(err)
		}
	}
	draft(olderDraft, "SUBMITTED", f.seed.Now.Add(-time.Hour), older)
	// An open recapture of the room, newer than everything else, with an
	// impossibility the participant never submitted.
	open := identity.NewID()
	draft(open, "OPEN", f.seed.Now.Add(time.Hour), f.seed.Photo(t, key))
	if err := f.seed.DB.Model(&database.RequirementAnswer{}).Where("draft_id=?", open).Updates(map[string]any{"impossibility_reason": "LOCKED", "media_ids": json.RawMessage(`[]`)}).Error; err != nil {
		t.Fatal(err)
	}
	f.gateway.Respond = script(t, "SAME_VIEW", "SAME_VIEW", func(llm.StructuredRequest) string {
		return compareJSON(t, claim(f.baseline.Items[0], "PRESENT_UNCHANGED", "conditionAfter", "GOOD"), claim(f.baseline.Items[1], "PRESENT_UNCHANGED", "conditionAfter", "POOR"))
	})
	if err := f.process(t, f.job); err != nil {
		t.Fatal(err)
	}
	results := resultsOf(t, f)
	for _, item := range f.baseline.Items {
		row := results[item.IdentityID.String()]
		if row.Status != "PRESENT_UNCHANGED" {
			t.Fatalf("%s = %s %s; the open recapture replaced the submitted capture", item.Name, row.Status, value(row.ReasonCode))
		}
	}
	calls := f.gateway.Calls("INVENTORY_COMPARE")
	if len(calls) != 1 {
		t.Fatalf("compare calls = %d", len(calls))
	}
	sent := map[string]bool{}
	for _, image := range calls[0].Images {
		sent[image.EvidenceID] = true
	}
	if !sent[submitted.String()] || sent[older.String()] {
		t.Fatalf("compare images = %v; want the newest submitted photo only", sent)
	}
}
