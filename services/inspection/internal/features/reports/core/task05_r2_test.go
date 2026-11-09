package core

import (
	"strings"
	"testing"
)

// Item 1 (review round 2): the neutrality backstop folds case and accents
// and covers Portuguese and English inflections; every controlled text a v3
// comparison is built from stays allowed.
func TestTask05R2ComparisonTextFilterCatchesInflectionsAndKeepsControlledTexts(t *testing.T) {
	blocked := []string{
		"O morador é responsável pelo dano", "deve ser cobrado do ocupante", "Dano causado por mau uso", "O conserto custará cerca de 300 reais",
		"Reparo estimado em 300 reais", "preço do reparo", "o proprietário deve pagar", "Desgaste esperado", "Deterioração natural",
		"normal wear", "The occupant is responsible", "repair will cost", "damage caused by misuse", "uso indevido", "Valor: R$ 300",
		"RESPONSÁVEL", "responsavel", "COBRADO", "cobranca", "Custará", "pagará", "wear and tear", "Liability", "pay for it", "it was charged",
	}
	for _, text := range blocked {
		for _, variant := range []string{text, strings.ToUpper(text), strings.ToLower(text)} {
			if ComparisonTextAllowed(variant) {
				t.Errorf("%q accepted as neutral comparison text", variant)
			}
		}
	}
	allowed := []string{
		LimitationNoBaselineView, LimitationNoCurrentView, LimitationPairRejected, LimitationInconsistentClaim, LimitationRoomCoverage, LimitationEnvironmentMissing,
		LimitationComparisonFailed, LimitationObstructed, LimitationFraming, LimitationQuality, LimitationUnsupportedOutcome, LimitationComparisonNoResult,
		"Sala · Sofá", "Condição na referência: Bom", "Condição atual: Não avaliável", "Avaliação visual da pia", "Pagina inicial apagada",
	}
	for _, outcome := range ComparisonOutcomes {
		allowed = append(allowed, ComparisonOutcomeLabel(outcome))
	}
	for _, text := range allowed {
		if !ComparisonTextAllowed(text) {
			t.Errorf("controlled text %q rejected", text)
		}
	}
}

// Item 6: a pair must link a REFERENCE evidence of the item's room to a
// distinct CURRENT evidence of the same room; the probe's CURRENT↔CURRENT
// pair, a pair with one evidence on both sides, reversed roles and cross-room
// pairs are rejected for staff and customer copies.
func TestTask05R2ComparisonPairsLinkBaselineToCurrentOfTheSameRoom(t *testing.T) {
	build := func(evidence []Evidence, pairs ...ComparisonPair) Snapshot {
		s := Snapshot{SchemaVersion: 3, Evidence: evidence, Comparison: &ComparisonSection{Baseline: ComparisonBaseline{VersionID: "v", VersionNumber: 1, Purpose: "MOVE_IN", ActivatedAt: "2026-01-01T00:00:00Z"}, Status: "COMPLETED", Items: []ComparisonItem{{Room: "Sala", Name: "Sofá", Outcome: ComparisonUnchanged, Pairs: pairs}}}}
		s.Comparison.Counts = CountComparison(s.Comparison.Items)
		return s
	}
	reference := Evidence{ID: "ref", RequirementKey: "env:sala", Role: "REFERENCE"}
	current := Evidence{ID: "cur", RequirementKey: "env:sala", Role: "CURRENT"}
	otherCurrent := Evidence{ID: "other", RequirementKey: "env:sala", Role: "CURRENT"}
	kitchen := Evidence{ID: "kitchen", RequirementKey: "env:cozinha", Role: "CURRENT"}
	kitchenReference := Evidence{ID: "kref", RequirementKey: "env:cozinha", Role: "REFERENCE"}
	valid := build([]Evidence{reference, current}, ComparisonPair{BaselineEvidenceID: "ref", CurrentEvidenceID: "cur"})
	if err := validateComparison(valid, false); err != nil {
		t.Fatalf("valid pair rejected: %v", err)
	}
	cases := map[string]Snapshot{
		"current evidence as baseline (probe)": build([]Evidence{current, otherCurrent}, ComparisonPair{BaselineEvidenceID: "cur", CurrentEvidenceID: "other"}),
		"same evidence on both sides (probe)":  build([]Evidence{current}, ComparisonPair{BaselineEvidenceID: "cur", CurrentEvidenceID: "cur"}),
		"reversed roles":                       build([]Evidence{reference, current}, ComparisonPair{BaselineEvidenceID: "cur", CurrentEvidenceID: "ref"}),
		"current evidence of another room":     build([]Evidence{reference, kitchen}, ComparisonPair{BaselineEvidenceID: "ref", CurrentEvidenceID: "kitchen"}),
		"pairs of one item in two rooms":       build([]Evidence{reference, current, kitchenReference, kitchen}, ComparisonPair{BaselineEvidenceID: "ref", CurrentEvidenceID: "cur"}, ComparisonPair{BaselineEvidenceID: "kref", CurrentEvidenceID: "kitchen"}),
	}
	for name, snapshot := range cases {
		for _, customer := range []bool{false, true} {
			s := snapshot
			if customer {
				section := *snapshot.Comparison
				section.Baseline.VersionID = ""
				s.Comparison = &section
			}
			if err := validateComparison(s, customer); err == nil {
				t.Errorf("%s (customer=%v) accepted", name, customer)
			}
		}
	}
}
