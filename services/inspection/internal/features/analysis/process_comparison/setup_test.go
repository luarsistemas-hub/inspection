package process_comparison

import (
	"context"
	"testing"
	"time"

	analysis "inspection/services/inspection/internal/features/analysis/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

type gateway struct{ response llm.StructuredResult }

func (g gateway) CompleteStructured(context.Context, llm.StructuredRequest) (llm.StructuredResult, error) {
	return g.response, nil
}

func TestValidateEvidenceEnforcesConfidenceAndKnownReferences(t *testing.T) {
	request := llm.StructuredRequest{MinimumConfidenceBPS: 8500, Mode: "CURRENT_ONLY", Images: []llm.NormalizedImage{{EvidenceID: "F11", Source: "CURRENT"}}}
	result := analysis.Result{Findings: []analysis.Finding{{Category: "CONSERVATION", Title: "Marca", Description: "Marca visível", Severity: "LOW", Quality: "ADEQUATE", RecommendedAction: "Verificar", Confidence: .9, EvidenceIDs: []string{"F99"}}}}
	if err := validateEvidence(result, request); err == nil {
		t.Fatal("unknown evidence accepted")
	}
	result.Findings[0].EvidenceIDs = []string{"F11"}
	result.Findings[0].Confidence = .8499
	if err := validateEvidence(result, request); err == nil {
		t.Fatal("low confidence accepted")
	}
	result.Findings[0].Confidence = .85
	if err := validateEvidence(result, request); err != nil {
		t.Fatalf("threshold confidence rejected: %v", err)
	}
	for _, category := range []string{"INVENTORY", "CLEANLINESS", "OBSTRUCTION", "EVIDENCE_QUALITY"} {
		severity, quality := "LOW", "ADEQUATE"
		if category == "EVIDENCE_QUALITY" {
			severity, quality = "NONE", "INSUFFICIENT"
		}
		result.Findings[0] = analysis.Finding{Category: category, Title: "Observação", Description: "Observação visual", Severity: severity, Quality: quality, RecommendedAction: "Verificar", Confidence: .8499, EvidenceIDs: []string{"F11"}}
		if err := validateEvidence(result, request); err == nil {
			t.Fatalf("below-threshold %s finding accepted", category)
		}
		result.Findings[0].Confidence = .85
		if err := validateEvidence(result, request); err != nil {
			t.Fatalf("threshold %s finding rejected: %v", category, err)
		}
	}
}

func TestValidateEvidenceModes(t *testing.T) {
	current := llm.StructuredRequest{Mode: "CURRENT_ONLY", MinimumConfidenceBPS: 8500, Images: []llm.NormalizedImage{{EvidenceID: "c", Source: "CURRENT"}}}
	if err := validateEvidence(analysis.Result{}, current); err != nil {
		t.Fatalf("current-only null rejected: %v", err)
	}
	trueValue := true
	if err := validateEvidence(analysis.Result{NoRelevantChange: &trueValue}, current); err == nil {
		t.Fatal("current-only boolean accepted")
	}
	comparative := llm.StructuredRequest{Mode: "COMPARE_ORIGIN_CURRENT", MinimumConfidenceBPS: 8500, Images: []llm.NormalizedImage{{EvidenceID: "o", Source: "ORIGIN", PairID: "p"}, {EvidenceID: "c", Source: "CURRENT", PairID: "p"}}}
	falseValue := false
	f := analysis.Finding{Category: "OBSTRUCTION", Title: "Gabinete obstruído", Description: "Saco encobre o gabinete", Severity: "LOW", Confidence: .9, EvidenceIDs: []string{"o", "c"}, Quality: "ADEQUATE", RecommendedAction: "Retirar o objeto e fotografar"}
	if err := validateEvidence(analysis.Result{NoRelevantChange: &falseValue, Findings: []analysis.Finding{f}}, comparative); err != nil {
		t.Fatalf("paired changed result rejected: %v", err)
	}
	if err := validateEvidence(analysis.Result{Findings: []analysis.Finding{f}}, comparative); err != nil {
		t.Fatalf("inconclusive result with finding rejected: %v", err)
	}
	if err := validateEvidence(analysis.Result{}, comparative); err == nil {
		t.Fatal("empty inconclusive comparison accepted")
	}
	if err := validateEvidence(analysis.Result{NoRelevantChange: &falseValue, Findings: []analysis.Finding{analysis.Finding{Category: "OBSTRUCTION", Title: "Gabinete obstruído", Description: "Saco encobre o gabinete", Severity: "LOW", Confidence: .9, EvidenceIDs: []string{"c"}, Quality: "ADEQUATE", RecommendedAction: "Retirar o objeto e fotografar"}}}, comparative); err == nil {
		t.Fatal("unpaired change was accepted")
	}
	if isInsufficientEvidence(analysis.Result{NoRelevantChange: &trueValue}, "CURRENT_ONLY") {
		t.Fatal("current-only result was marked inconclusive because its comparison value is null")
	}
	if !isInsufficientEvidence(analysis.Result{Findings: []analysis.Finding{{Category: "EVIDENCE_QUALITY"}}}, "CURRENT_ONLY") {
		t.Fatal("evidence quality finding did not mark the assessment incomplete")
	}
}

func TestVV_IT_212HistoricalAbsenceRequiresPairedOriginAndCurrentEvidence(t *testing.T) {
	request := llm.StructuredRequest{Mode: "COMPARE_ORIGIN_CURRENT", MinimumConfidenceBPS: 8500, Images: []llm.NormalizedImage{{EvidenceID: "origin", Source: "ORIGIN", PairID: "sink", VisibilityKnown: true, Visible: true}, {EvidenceID: "current", Source: "CURRENT", PairID: "sink", VisibilityKnown: true, Visible: false}}}
	finding := analysis.Finding{Category: "INVENTORY", Title: "Item REMOVED", Description: "O item não está mais visível.", Severity: "LOW", Quality: "ADEQUATE", RecommendedAction: "Verificar", Confidence: .9, EvidenceIDs: []string{"current"}}
	if err := validateEvidence(analysis.Result{Findings: []analysis.Finding{finding}}, request); err == nil {
		t.Fatal("unsupported historical absence passed without paired origin evidence")
	}
	finding.EvidenceIDs = []string{"origin", "current"}
	if err := validateEvidence(analysis.Result{Findings: []analysis.Finding{finding}}, request); err == nil {
		t.Fatal("historical absence was accepted when current view is obstructed")
	}
	request.Images[1].Visible = true
	if err := validateEvidence(analysis.Result{Findings: []analysis.Finding{finding}}, request); err != nil {
		t.Fatalf("visible paired historical evidence rejected: %v", err)
	}
}

func TestCurrentOnlyRejectsStructuredRepairClaimUT048(t *testing.T) {
	for name, output := range map[string]string{
		"unsupported category":     `{"noRelevantChange":null,"findings":[{"category":"REPAIRED","title":"Pia","description":"Pia reparada","severity":"LOW","confidence":0.9,"evidenceIds":["F11"],"quality":"ADEQUATE","recommendedAction":"Verificar"}]}`,
		"unsupported change field": `{"noRelevantChange":null,"findings":[{"category":"CONSERVATION","changeType":"REPAIRED","title":"Pia","description":"Pia visível","severity":"LOW","confidence":0.9,"evidenceIds":["F11"],"quality":"ADEQUATE","recommendedAction":"Verificar"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := analysis.ParseResult([]byte(output)); err == nil {
				t.Fatal("unsupported historical claim passed structured response validation")
			}
		})
	}
}

func TestAttemptCountIncludesBrokerDeliveries(t *testing.T) {
	job := database.ComparisonJob{Attempts: 1}
	if got := attemptCount(context.Background(), job); got != 2 {
		t.Fatalf("direct attempt count = %d, want 2", got)
	}
	ctx := messaging.WithAttempt(context.Background(), 3)
	if got := attemptCount(ctx, job); got != 4 {
		t.Fatalf("broker attempt count = %d, want 4", got)
	}
}

func TestSetupRequiresAuthorizedRequestSeam(t *testing.T) {
	if _, err := Setup(Dependencies{Gateway: gateway{}}); err == nil {
		t.Fatal("missing authorized request builder accepted")
	}
	if _, err := Setup(Dependencies{Gateway: gateway{}, BuildRequest: func(context.Context, *gorm.DB, database.ComparisonJob, eventsPayload) (llm.StructuredRequest, error) {
		return llm.StructuredRequest{}, nil
	}, Now: time.Now}); err != nil {
		t.Fatalf("valid dependencies rejected: %v", err)
	}
}

func TestValidateEvidenceRejectsHistoricalChangeClaimsWithoutBaseline(t *testing.T) {
	current := llm.StructuredRequest{MinimumConfidenceBPS: 8500, Mode: "CURRENT_ONLY", Images: []llm.NormalizedImage{{EvidenceID: "F11", Source: "CURRENT"}}}
	observed := analysis.Finding{Category: "CONSERVATION", Title: "Risco na pia", Description: "Risco visível na pia da cozinha.", Severity: "LOW", Quality: "ADEQUATE", RecommendedAction: "Acompanhar", Confidence: .9, EvidenceIDs: []string{"F11"}}
	if err := validateEvidence(analysis.Result{Findings: []analysis.Finding{observed}}, current); err != nil {
		t.Fatalf("current observed damage rejected: %v", err)
	}
	for _, claim := range []string{"Torneira removida", "Armário adicionado", "Pia substituída", "Rodapé reparado", "Novo risco na pia", "Window replaced"} {
		finding := observed
		finding.Title = claim
		if err := validateEvidence(analysis.Result{Findings: []analysis.Finding{finding}}, current); err == nil {
			t.Fatalf("current-only historical claim %q accepted", claim)
		}
	}
	// A comparative pair may describe a replacement it can support.
	comparative := llm.StructuredRequest{MinimumConfidenceBPS: 8500, Mode: "COMPARE_ORIGIN_CURRENT", Images: []llm.NormalizedImage{{EvidenceID: "F01", Source: "ORIGIN", PairID: "P1", VisibilityKnown: true, Visible: true}, {EvidenceID: "F11", Source: "CURRENT", PairID: "P1", VisibilityKnown: true, Visible: true}}}
	replaced := observed
	replaced.Title, replaced.EvidenceIDs = "Pia substituída", []string{"F01", "F11"}
	changed := false
	if err := validateEvidence(analysis.Result{NoRelevantChange: &changed, Findings: []analysis.Finding{replaced}}, comparative); err != nil {
		t.Fatalf("paired comparative replacement rejected: %v", err)
	}
}
