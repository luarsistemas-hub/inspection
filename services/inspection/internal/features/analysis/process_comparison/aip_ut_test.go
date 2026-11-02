package process_comparison

import (
	"testing"

	analysis "inspection/services/inspection/internal/features/analysis/core"
	"inspection/services/inspection/internal/platform/llm"
)

func aipComparativeRequest() llm.StructuredRequest {
	return llm.StructuredRequest{Mode: "COMPARE_ORIGIN_CURRENT", MinimumConfidenceBPS: analysis.MinimumConfidenceBPS, Images: []llm.NormalizedImage{
		{EvidenceID: "origin-1", Source: "ORIGIN", PairID: "wall", Position: "ORIGIN_1", Digest: "d-origin", VisibilityKnown: true, Visible: true},
		{EvidenceID: "current-1", Source: "CURRENT", PairID: "wall", Position: "CURRENT_1", Digest: "d-current", VisibilityKnown: true, Visible: true},
	}}
}

// TestAIP_UT_066AnalysisAcceptsEvidenceBoundObservationWithProvenance covers
// UT-066 of autonomous-inspection-platform: a neutral observation bound to
// authorized evidence, with bounded confidence and provenance, is accepted.
func TestAIP_UT_066AnalysisAcceptsEvidenceBoundObservationWithProvenance(t *testing.T) {
	payload := []byte(`{"noRelevantChange":false,"findings":[{"category":"CONSERVATION","title":"Mancha na parede","description":"Há uma mancha escura na parede norte, ausente na imagem de referência.","severity":"MEDIUM","confidence":0.91,"evidenceIds":["origin-1","current-1"],"quality":"ADEQUATE","recommendedAction":"Registrar a condição observada e acompanhar."}]}`)
	parsed, err := analysis.ParseResult(payload)
	if err != nil {
		t.Fatalf("neutral evidence-bound observation rejected: %v", err)
	}
	if len(parsed.Findings) != 1 || parsed.Findings[0].Confidence != 0.91 || len(parsed.Findings[0].EvidenceIDs) != 2 {
		t.Fatalf("unexpected parsed finding: %+v", parsed.Findings)
	}
	if err := validateEvidence(parsed, aipComparativeRequest()); err != nil {
		t.Fatalf("observation bound to known paired evidence rejected: %v", err)
	}
	for _, word := range []string{"culpa", "multa", "custo", "responsabilidade"} {
		if analysis.ValidateResult(analysis.Result{Findings: []analysis.Finding{{Category: "CONSERVATION", Title: "Mancha", Description: "Observação sem " + word + " atribuída.", Severity: "LOW", Confidence: .9, EvidenceIDs: []string{"current-1"}, Quality: "ADEQUATE", RecommendedAction: "Verificar."}}}) == nil {
			t.Fatalf("a finding mentioning %q must be rejected, it would assign a consequence", word)
		}
	}
}

// TestAIP_UT_067AnalysisRejectsUnknownEvidenceAndConfidenceBelowThreshold covers
// the request-bound half of UT-067: references must name evidence sent to the
// provider and confidence must reach the pinned minimum.
func TestAIP_UT_067AnalysisRejectsUnknownEvidenceAndConfidenceBelowThreshold(t *testing.T) {
	base := func(confidence float64, evidence ...string) analysis.Result {
		falseValue := false
		return analysis.Result{NoRelevantChange: &falseValue, Findings: []analysis.Finding{{Category: "CONSERVATION", Title: "Mancha na parede", Description: "Mancha escura observada.", Severity: "MEDIUM", Confidence: confidence, EvidenceIDs: evidence, Quality: "ADEQUATE", RecommendedAction: "Registrar."}}}
	}
	request := aipComparativeRequest()
	if err := validateEvidence(base(.9, "origin-1", "current-1"), request); err != nil {
		t.Fatalf("valid finding rejected: %v", err)
	}
	for name, result := range map[string]analysis.Result{
		"unknown evidence reference":   base(.9, "origin-1", "current-9"),
		"duplicate evidence reference": base(.9, "origin-1", "current-1", "current-1"),
		"below minimum confidence":     base(.8499, "origin-1", "current-1"),
		"reference without current":    base(.9, "origin-1"),
	} {
		if err := validateEvidence(result, request); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	if _, err := analysis.ParseResult([]byte(`{"noRelevantChange":false,"findings":[{"category":"CONSERVATION","title":"Mancha","description":"Mancha escura.","severity":"LOW","confidence":1.2,"evidenceIds":["current-1"],"quality":"ADEQUATE","recommendedAction":"Verificar."}]}`)); err == nil {
		t.Fatal("confidence above 1 accepted")
	}
}
