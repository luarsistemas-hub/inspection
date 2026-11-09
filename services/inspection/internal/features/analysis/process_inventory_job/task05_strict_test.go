package process_inventory_job

import (
	"encoding/json"
	"errors"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"
)

// Strict (report v3) rules of TechSpec "Item status": every conclusion needs
// the item's own confirmed pair, and proven absence of a movable item needs
// whole-room coverage. The legacy rows show the unchanged historical rules.
func TestTask05StrictDeriveItemResultRules(t *testing.T) {
	cases := []struct {
		name  string
		claim ItemClaim
		facts ItemFacts
		want  ItemOutcome
	}{
		{"movable absence with a vacant former location only", ItemClaim{Status: "MISSING"}, ItemFacts{ConditionBefore: "GOOD", Confirmed: true, Strict: true, Movable: true}, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: reasonRoomCoverage}},
		{"movable absence with whole-room coverage", ItemClaim{Status: "MISSING"}, ItemFacts{ConditionBefore: "GOOD", Confirmed: true, Strict: true, Movable: true, RoomCovered: true}, ItemOutcome{Status: "MISSING"}},
		{"built-in absence with its confirmed location", ItemClaim{Status: "MISSING"}, ItemFacts{ConditionBefore: "GOOD", Confirmed: true, Strict: true}, ItemOutcome{Status: "MISSING"}},
		{"absence without the item's own pair", ItemClaim{Status: "MISSING"}, ItemFacts{ConditionBefore: "GOOD", Observation: "OBSERVED", EnvironmentViewed: true, Strict: true, RoomCovered: true}, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: "NO_CORRESPONDING_VIEW"}},
		{"legacy absence from a viewed environment", ItemClaim{Status: "MISSING"}, ItemFacts{ConditionBefore: "GOOD", Observation: "OBSERVED", EnvironmentViewed: true}, ItemOutcome{Status: "MISSING"}},
		{"declared item without pair", ItemClaim{Status: "PRESENT_UNCHANGED"}, ItemFacts{ConditionBefore: "GOOD", Declared: true, Strict: true}, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: "NO_CORRESPONDING_VIEW"}},
		{"legacy declared item without pair", ItemClaim{Status: "PRESENT_UNCHANGED"}, ItemFacts{ConditionBefore: "GOOD", Declared: true}, ItemOutcome{Status: "PRESENT_UNCHANGED"}},
		{"improvement with a confirmed pair", ItemClaim{Status: "IMPROVED", ConditionAfter: "GOOD"}, ItemFacts{ConditionBefore: "POOR", Confirmed: true, Strict: true}, ItemOutcome{Status: "IMPROVED", ConditionAfter: "GOOD"}},
		{"unchanged with a confirmed pair", ItemClaim{Status: "PRESENT_UNCHANGED", ConditionAfter: "GOOD"}, ItemFacts{ConditionBefore: "GOOD", Confirmed: true, Strict: true}, ItemOutcome{Status: "PRESENT_UNCHANGED", ConditionAfter: "GOOD"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveItemResult(tc.claim, tc.facts); got != tc.want {
				t.Fatalf("DeriveItemResult = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestTask05RoomCoverageNeedsEveryApplicableExpectedView(t *testing.T) {
	room := compareScopeRoom{Key: "sala", Components: []catalog.ExpectedComponent{{Key: "piso", Views: []string{"wide", "detail"}}, {Key: "janela"}}}
	answer := func(component, view, state string) capturecore.ComponentCoverageAnswer {
		return capturecore.ComponentCoverageAnswer{RequirementKey: "env:sala", ComponentKey: component, ViewKey: view, State: state}
	}
	cases := []struct {
		name    string
		answers []capturecore.ComponentCoverageAnswer
		want    bool
	}{
		{"every view observed", []capturecore.ComponentCoverageAnswer{answer("piso", "wide", "OBSERVED"), answer("piso", "detail", "OBSERVED"), answer("janela", "", "OBSERVED")}, true},
		{"one expected view missing", []capturecore.ComponentCoverageAnswer{answer("piso", "wide", "OBSERVED"), answer("janela", "", "OBSERVED")}, false},
		{"partial view", []capturecore.ComponentCoverageAnswer{answer("piso", "wide", "OBSERVED"), answer("piso", "detail", "PARTIAL"), answer("janela", "", "OBSERVED")}, false},
		{"not applicable component leaves the denominator", []capturecore.ComponentCoverageAnswer{answer("piso", "wide", "OBSERVED"), answer("piso", "detail", "OBSERVED"), answer("janela", "", "NOT_APPLICABLE")}, true},
		{"later answer replaces an earlier one", []capturecore.ComponentCoverageAnswer{answer("piso", "wide", "OBSERVED"), answer("piso", "detail", "OBSERVED"), answer("janela", "", "OBSERVED"), answer("piso", "detail", "NOT_OBSERVED")}, false},
	}
	for _, tc := range cases {
		if got := roomCovered(room, tc.answers); got != tc.want {
			t.Fatalf("%s: roomCovered = %v, want %v", tc.name, got, tc.want)
		}
	}
	if roomCovered(compareScopeRoom{Key: "vazio"}, nil) {
		t.Fatal("a room without expected components has no proven coverage")
	}
}

func TestTask05CoverageEvidenceAcceptsEmptyAndRejectsMalformed(t *testing.T) {
	for _, raw := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(``), json.RawMessage(`[]`)} {
		ids, err := decodeCoverageEvidenceIDs(raw)
		if err != nil || len(ids) != 0 {
			t.Errorf("decodeCoverageEvidenceIDs(%s) = %v, %v; want empty list", raw, ids, err)
		}
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`{"not":"an array"}`), json.RawMessage(`{}`), json.RawMessage(`not json`), json.RawMessage(`[null]`), json.RawMessage(`['bad']`), json.RawMessage(`["00000000-0000-0000-0000-000000000000"]`)} {
		if _, err := decodeCoverageEvidenceIDs(raw); !errors.Is(err, messaging.ErrPermanent) {
			t.Errorf("decodeCoverageEvidenceIDs(%s) error = %v, want permanent error", raw, err)
		}
	}
}

func TestTask05CompareScopeRoomRequiresPinnedIdentity(t *testing.T) {
	rooms := []compareScopeRoom{{Key: "bedroom-2", Name: "Quarto", TypeCode: "BEDROOM"}}
	requirement := &capturecore.Requirement{Key: "env:old-baseline-id", EnvironmentName: "Quarto", EnvironmentTypeCode: ""}
	if room, matched := resolveCompareScopeRoom(rooms, requirement.Key, requirement); matched {
		t.Fatalf("room = %+v, matched = true; an unanchored or type-mismatched name must not bind scope", room)
	}
}

// strictFixture is a compare fixture whose inspection pins a descriptive
// scope, so its comparison follows the strict (report v3) rules.
func strictFixture(t *testing.T, covered bool, environments ...inventorytest.BaselineEnvironment) compareFixture {
	t.Helper()
	f := newCompareFixture(t, 1, environments...)
	env := f.baseline.Environments[0]
	f.seed.PinScope(t, inventorytest.ScopeRoom{Key: env.IdentityID.String(), Name: env.Name, TypeCode: env.TypeCode, Components: map[string][]string{"geral": {"wide"}}})
	state := "PARTIAL"
	if covered {
		state = "OBSERVED"
	}
	f.seed.ComponentAnswer(t, "env:"+env.IdentityID.String(), "geral", "wide", state, f.current)
	return f
}

// UT-118 (processor side): a strict comparison never stores model text that
// states responsibility, normal wear or repair cost; legacy text is untouched.
func TestTask05StrictComparisonDropsBlameTextFromModelDescriptions(t *testing.T) {
	blame := "Dano causado pelo inquilino; custo de reparo estimado em R$ 300."
	for _, strict := range []bool{true, false} {
		var f compareFixture
		if strict {
			f = strictFixture(t, true, living)
		} else {
			f = newCompareFixture(t, 1, living)
		}
		f.gateway.Respond = script(t, "SAME_VIEW", "SAME_VIEW", func(llm.StructuredRequest) string {
			return compareJSON(t, claim(f.baseline.Items[0], "CONDITION_CHANGED", "conditionAfter", "FAIR", "description", blame), claim(f.baseline.Items[1], "PRESENT_UNCHANGED"))
		})
		if err := f.process(t, f.job); err != nil {
			t.Fatal(err)
		}
		row := resultsOf(t, f)[f.baseline.Items[0].IdentityID.String()]
		var findings []database.FindingRecord
		if err := f.seed.DB.Where("inventory_job_id=?", f.job.ID).Find(&findings).Error; err != nil {
			t.Fatal(err)
		}
		if strict && (row.Description != nil || len(findings) != 1 || findings[0].Description == blame) {
			t.Fatalf("strict result = %+v findings = %+v; blame text must be dropped", row, findings)
		}
		if !strict && (row.Description == nil || *row.Description != blame) {
			t.Fatalf("legacy result description changed: %+v", row)
		}
	}
}

// A strict job keeps "no basis" apart from "environment not captured" when
// the room has no current evidence at all.
func TestTask05StrictUncapturedRoomKeepsNoBasisDistinct(t *testing.T) {
	seed := inventorytest.New(t, inventorytest.InspectionOptions{})
	baseline := seed.PinBaseline(t, inventorytest.BaselineEnvironment{Name: "Sala", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}, {Name: "Quadro", Condition: "GOOD", Declared: true}}})
	env := baseline.Environments[0]
	seed.PinScope(t, inventorytest.ScopeRoom{Key: env.IdentityID.String(), Name: env.Name, TypeCode: env.TypeCode, Components: map[string][]string{"geral": {"wide"}}})
	key := inventorytest.EnvironmentKey(env)
	seed.Answer(t, key, nil, "LOCKED")
	job := seed.Job(t, "COMPARE", key, baseline.Version, &env.IdentityID)
	h := newHarness(t, seed)
	if err := h.process(t, job); err != nil {
		t.Fatal(err)
	}
	var rows []database.InventoryItemResult
	if err := seed.DB.Where("job_id=?", job.ID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	reasons := map[identity.ID]string{}
	for _, row := range rows {
		reasons[*row.ItemIdentityID] = value(row.ReasonCode)
	}
	if reasons[baseline.Items[0].IdentityID] != "ENVIRONMENT_NOT_CAPTURED" || reasons[baseline.Items[1].IdentityID] != reasonNoBaselineView {
		t.Fatalf("reasons = %v; want not captured for the viewed item and no basis for the item without view", reasons)
	}
}
