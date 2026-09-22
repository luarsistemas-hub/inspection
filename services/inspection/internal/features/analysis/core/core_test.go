package core

import "testing"

func TestValidateResultRequiresExplicitEmptyOutcome(t *testing.T) {
	if err := ValidateResult(Result{}); err == nil {
		t.Fatal("expected invalid empty output")
	}
	if err := ValidateResult(Result{CoverageStatus: "COMPLETE", ComparisonStatus: "UNCHANGED"}); err != nil {
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
	result, err := ParseResult([]byte(`{"coverageStatus":"COMPLETE","comparisonStatus":"UNCHANGED","findings":[]}`))
	if err != nil || result.ComparisonStatus != "UNCHANGED" {
		t.Fatalf("strict valid result rejected: %#v %v", result, err)
	}
}

func TestUnchangedComparisonRejectsFindings(t *testing.T) {
	result := Result{CoverageStatus: "COMPLETE", ComparisonStatus: "UNCHANGED", Findings: []Finding{{Category: "CONSERVATION", ChangeType: "NEW_DAMAGE", Title: "Dano", Description: "Dano visível", Severity: "LOW", Confidence: .9, EvidenceIDs: []string{"current"}, Quality: "ADEQUATE", RecommendedAction: "Revisar"}}}
	if err := ValidateResult(result); err == nil {
		t.Fatal("unchanged comparison accepted a finding")
	}
}

func TestInsufficientComparisonRequiresQualityFinding(t *testing.T) {
	result := Result{CoverageStatus: "INSUFFICIENT", ComparisonStatus: "INCONCLUSIVE"}
	if err := ValidateResult(result); err == nil {
		t.Fatal("inconclusive result without quality finding was accepted")
	}
	result.Findings = []Finding{{Category: "EVIDENCE_QUALITY", ChangeType: "NOT_APPLICABLE", Title: "Evidência insuficiente", Description: "A origem não cobre a área", Severity: "NONE", Confidence: .9, EvidenceIDs: []string{"current"}, Quality: "INSUFFICIENT", RecommendedAction: "Solicitar nova captura"}}
	if err := ValidateResult(result); err != nil {
		t.Fatalf("valid inconclusive result rejected: %v", err)
	}
}

func TestCurrentOnlyAcceptsCurrentCondition(t *testing.T) {
	result := Result{CoverageStatus: "COMPLETE", ComparisonStatus: "NOT_APPLICABLE", Findings: []Finding{{Category: "CONSERVATION", ChangeType: "CURRENT_CONDITION", Title: "Desgaste visível", Description: "Há desgaste visível na superfície.", Severity: "LOW", Confidence: .8, EvidenceIDs: []string{"current"}, Quality: "ADEQUATE", RecommendedAction: "Revisar presencialmente"}}}
	if err := ValidateResult(result); err != nil {
		t.Fatalf("current-only condition rejected: %v", err)
	}
}

func TestValidateResultRejectsContradictoryStatusesAndFindings(t *testing.T) {
	quality := Finding{Category: "EVIDENCE_QUALITY", ChangeType: "NOT_APPLICABLE", Title: "Cobertura limitada", Description: "A área não está visível.", Severity: "NONE", Confidence: .9, EvidenceIDs: []string{"current"}, Quality: "INSUFFICIENT", RecommendedAction: "Solicitar nova captura"}
	if err := ValidateResult(Result{CoverageStatus: "COMPLETE", ComparisonStatus: "CHANGED", Findings: []Finding{quality}}); err == nil {
		t.Fatal("changed result with only a quality finding was accepted")
	}
	if err := ValidateResult(Result{CoverageStatus: "COMPLETE", ComparisonStatus: "INCONCLUSIVE", Findings: []Finding{{Category: "CONSERVATION", ChangeType: "NEW_DAMAGE", Title: "Dano", Description: "Dano visível.", Severity: "LOW", Confidence: .9, EvidenceIDs: []string{"current"}, Quality: "ADEQUATE", RecommendedAction: "Revisar"}}}); err == nil {
		t.Fatal("inconclusive result without quality finding was accepted")
	}
	if err := ValidateResult(Result{CoverageStatus: "COMPLETE", ComparisonStatus: "NOT_APPLICABLE", Findings: []Finding{{Category: "CONSERVATION", ChangeType: "NEW_DAMAGE", Title: "Dano", Description: "Dano visível.", Severity: "LOW", Confidence: .9, EvidenceIDs: []string{"current"}, Quality: "ADEQUATE", RecommendedAction: "Revisar"}}}); err == nil {
		t.Fatal("current-only result with temporal finding was accepted")
	}
}

func TestValidateResultRejectsBlameAndCostLanguage(t *testing.T) {
	err := ValidateResult(Result{CoverageStatus: "COMPLETE", ComparisonStatus: "CHANGED", Findings: []Finding{{Category: "CONSERVATION", ChangeType: "NEW_DAMAGE", Title: "Tenant fault", Description: "Observed change", Severity: "LOW", Confidence: .8, EvidenceIDs: []string{"evidence-1"}, Quality: "ADEQUATE", RecommendedAction: "Review"}}})
	if err == nil {
		t.Fatal("blame language was accepted")
	}
	err = ValidateResult(Result{CoverageStatus: "COMPLETE", ComparisonStatus: "CHANGED", Findings: []Finding{{Category: "CONSERVATION", ChangeType: "NEW_DAMAGE", Title: "Alteração.", Description: "A responsabilidade é indeterminada.", Severity: "LOW", Confidence: .8, EvidenceIDs: []string{"evidence-1"}, Quality: "ADEQUATE", RecommendedAction: "Review"}}})
	if err == nil {
		t.Fatal("punctuated responsibility language was accepted")
	}
	err = ValidateResult(Result{CoverageStatus: "COMPLETE", ComparisonStatus: "CHANGED", Findings: []Finding{{Category: "CONSERVATION", ChangeType: "NEW_DAMAGE", Title: "Custo-benefício visual", Description: "Alteração observada.", Severity: "LOW", Confidence: .8, EvidenceIDs: []string{"evidence-1"}, Quality: "ADEQUATE", RecommendedAction: "Review"}}})
	if err != nil {
		t.Fatalf("unrelated substring was rejected: %v", err)
	}
}
