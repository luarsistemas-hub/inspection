package core

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// comparisonSnapshot is a valid v3 snapshot with a comparison of n unchanged
// items plus one item of every other outcome, each with its own evidence.
func comparisonSnapshot(unchanged int) Snapshot {
	snapshot := validV3Snapshot()
	section := &ComparisonSection{Baseline: ComparisonBaseline{VersionID: "baseline-version", VersionNumber: 3, Purpose: "MOVE_IN", ActivatedAt: "2026-10-01T12:00:00Z"}, Status: "COMPLETED", Items: []ComparisonItem{}, OutsideBaseline: []ComparisonObservation{{Room: "Sala", TypeCode: "OTHER", Name: "Luminária"}}}
	pair := func(n int) []ComparisonPair {
		baseline, current := fmt.Sprintf("ref-%d", n), fmt.Sprintf("cur-%d", n)
		snapshot.Evidence = append(snapshot.Evidence, Evidence{ID: baseline, RequirementKey: "env:sala", Role: "REFERENCE"}, Evidence{ID: current, RequirementKey: "env:sala", Role: "CURRENT"})
		return []ComparisonPair{{BaselineEvidenceID: baseline, CurrentEvidenceID: current}}
	}
	n := 0
	for ; n < unchanged; n++ {
		section.Items = append(section.Items, ComparisonItem{ItemIdentityID: fmt.Sprintf("item-%d", n), Room: "Sala", TypeCode: "CHAIR", Name: fmt.Sprintf("Cadeira %d", n), Outcome: ComparisonUnchanged, BaselineCondition: "GOOD", CurrentCondition: "GOOD", Pairs: pair(n)})
	}
	section.Items = append(section.Items,
		ComparisonItem{ItemIdentityID: "item-changed", Room: "Sala", TypeCode: "SOFA", Name: "Sofá", Outcome: ComparisonChanged, BaselineCondition: "GOOD", CurrentCondition: "FAIR", Pairs: pair(n + 1)},
		ComparisonItem{ItemIdentityID: "item-improved", Room: "Sala", TypeCode: "TABLE", Name: "Mesa", Outcome: ComparisonImproved, BaselineCondition: "POOR", CurrentCondition: "GOOD", Pairs: pair(n + 2)},
		ComparisonItem{ItemIdentityID: "item-missing", Room: "Sala", TypeCode: "TV", Name: "Televisor", Outcome: ComparisonMissing, BaselineCondition: "GOOD", Pairs: pair(n + 3)},
		ComparisonItem{ItemIdentityID: "item-not-observed", Room: "Sala", TypeCode: "CURTAIN_BLIND", Name: "Cortina", Outcome: ComparisonNotObserved, BaselineCondition: "GOOD", Limitations: []string{LimitationNoCurrentView}},
		ComparisonItem{ItemIdentityID: "item-no-basis", Room: "Sala", TypeCode: "OTHER", Name: "Quadro", Outcome: ComparisonNoBasis, BaselineCondition: "GOOD", Limitations: []string{LimitationNoBaselineView}},
	)
	section.Counts = CountComparison(section.Items)
	snapshot.Comparison = section
	return snapshot
}

// UT-074 (boundary): many unchanged items stay reachable through filters
// without hiding the total denominator.
func TestUT074ManyUnchangedItemsKeepTheTotalDenominatorUnderFilters(t *testing.T) {
	snapshot := comparisonSnapshot(40)
	if err := Validate(snapshot); err != nil {
		t.Fatalf("comparison snapshot rejected: %v", err)
	}
	section := *snapshot.Comparison
	if section.Counts.Total != 45 || section.Counts.Unchanged != 40 {
		t.Fatalf("counts = %+v; want 45 items with 40 unchanged", section.Counts)
	}
	sum := 0
	for _, outcome := range ComparisonOutcomes {
		filtered, err := FilterComparison(section, outcome)
		if err != nil {
			t.Fatal(err)
		}
		if len(filtered.Items) != section.Counts.ComparisonCount(outcome) || filtered.Counts != section.Counts {
			t.Fatalf("%s filter shows %d items with counts %+v; want %d items and the full denominator", outcome, len(filtered.Items), filtered.Counts, section.Counts.ComparisonCount(outcome))
		}
		sum += len(filtered.Items)
	}
	if sum != section.Counts.Total {
		t.Fatalf("filters cover %d items; want the total %d", sum, section.Counts.Total)
	}
	if _, err := FilterComparison(section, "EVERYTHING_FINE"); !errors.Is(err, ErrInvalidComparisonFilter) {
		t.Fatalf("unknown filter err = %v", err)
	}
	html, err := HTML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"45 itens da referência · 40 sem alteração", "Sem alteração · 40 de 45 itens", "Alterado · 1 de 45 itens", "Cadeira 39"} {
		if !strings.Contains(string(html), want) {
			t.Fatalf("HTML misses %q", want)
		}
	}
}

func TestTask05ComparisonValidationRejectsUnsupportedOrInconsistentFacts(t *testing.T) {
	cases := map[string]func(*Snapshot){
		"counts differ from items": func(s *Snapshot) { s.Comparison.Counts.Unchanged++ },
		"conclusion without pair":  func(s *Snapshot) { s.Comparison.Items[0].Pairs = nil },
		"gap without limitation":   func(s *Snapshot) { s.Comparison.Items[len(s.Comparison.Items)-1].Limitations = nil },
		"pair with unknown evidence": func(s *Snapshot) {
			s.Comparison.Items[0].Pairs = []ComparisonPair{{BaselineEvidenceID: "ref-0", CurrentEvidenceID: "elsewhere"}}
		},
		"blame in description": func(s *Snapshot) {
			s.Comparison.Items[0].Description = "Desgaste natural de uso; sem custo para o locatário."
		},
		"blame in limitation":       func(s *Snapshot) { s.Comparison.Items[0].Limitations = []string{"Responsabilidade do inquilino."} },
		"unknown outcome":           func(s *Snapshot) { s.Comparison.Items[0].Outcome = "MAYBE" },
		"baseline without version":  func(s *Snapshot) { s.Comparison.Baseline.VersionNumber = 0 },
		"invalid comparison status": func(s *Snapshot) { s.Comparison.Status = "DONE" },
		"comparison before v3":      func(s *Snapshot) { s.SchemaVersion = 2 },
	}
	for name, mutate := range cases {
		snapshot := comparisonSnapshot(1)
		mutate(&snapshot)
		if err := Validate(snapshot); err == nil {
			t.Fatalf("%s: snapshot accepted", name)
		}
	}
	customer := comparisonSnapshot(1)
	customer.Scope, customer.Provenance, customer.Findings = nil, nil, nil
	if err := ValidateCustomerSnapshot(customer); err == nil {
		t.Fatal("a customer copy with item identities and the baseline ID was accepted")
	}
}

func TestTask05ComparisonSanitizesURLsWithoutChangingCounts(t *testing.T) {
	snapshot := comparisonSnapshot(1)
	snapshot.Comparison.Items[0].Description = "Ver https://example.test/foto.jpg"
	cleaned := SanitizeURLs(snapshot)
	if strings.Contains(cleaned.Comparison.Items[0].Description, "https://") || cleaned.Comparison.Counts != snapshot.Comparison.Counts {
		t.Fatalf("sanitized comparison = %+v", cleaned.Comparison.Items[0])
	}
	if snapshot.Comparison.Items[0].Description != "Ver https://example.test/foto.jpg" {
		t.Fatal("the caller's snapshot was modified")
	}
	if err := Validate(cleaned); err != nil {
		t.Fatalf("sanitized snapshot rejected: %v", err)
	}
}

func TestTask05ComparisonLabelsCoverEveryOutcome(t *testing.T) {
	for _, outcome := range ComparisonOutcomes {
		if label := ComparisonOutcomeLabel(outcome); label == "" || label == unrecognizedLabel {
			t.Fatalf("outcome %s has no Portuguese label", outcome)
		}
	}
	for _, status := range []string{"COMPLETED", "FAILED", "INCONCLUSIVE"} {
		if label := ComparisonStatusLabel(status); label == unrecognizedLabel {
			t.Fatalf("status %s has no Portuguese label", status)
		}
	}
}
