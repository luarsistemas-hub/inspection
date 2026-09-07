package core

import "testing"

func TestValidateResultRequiresExplicitEmptyOutcome(t *testing.T) {
	if err := ValidateResult(Result{}); err == nil {
		t.Fatal("expected invalid empty output")
	}
	if err := ValidateResult(Result{NoRelevantChange: true}); err != nil {
		t.Fatalf("explicit empty result: %v", err)
	}
}

func TestClassifyIsDeterministicAndConservative(t *testing.T) {
	tests := []struct {
		name  string
		facts []ComparisonFacts
		want  string
	}{
		{"normal", []ComparisonFacts{{Terminal: true}}, ClassificationNormal},
		{"critical", []ComparisonFacts{{Terminal: true, Findings: []Finding{{Severity: "CRITICAL"}}}}, ClassificationCritical},
		{"inconclusive", []ComparisonFacts{TerminalFallback()}, ClassificationAttention},
		{"flagged", []ComparisonFacts{{Terminal: true, Flagged: true}}, ClassificationAttention},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Classify(test.facts).Classification; got != test.want {
				t.Fatalf("got %s, want %s", got, test.want)
			}
		})
	}
}

func TestParseResultRejectsUnknownOrLooseFields(t *testing.T) {
	if _, err := ParseResult([]byte(`{"noRelevantChange":true,"narrative":"looks fine"}`)); err == nil {
		t.Fatal("unexpected loose provider output accepted")
	}
	result, err := ParseResult([]byte(`{"noRelevantChange":true,"findings":[]}`))
	if err != nil || !result.NoRelevantChange {
		t.Fatalf("strict valid result rejected: %#v %v", result, err)
	}
}

func TestValidateResultRejectsBlameAndCostLanguage(t *testing.T) {
	err := ValidateResult(Result{Findings: []Finding{{Category: "condition", Title: "Tenant fault", Description: "Observed change", Severity: "LOW", Confidence: .8, EvidenceIDs: []string{"evidence-1"}, Quality: "clear", RecommendedAction: "Review"}}})
	if err == nil {
		t.Fatal("blame language was accepted")
	}
}
