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
	request := llm.StructuredRequest{MinimumConfidenceBPS: 8500, Mode: "CURRENT_ONLY", Images: []llm.NormalizedImage{{EvidenceID: "known", Source: "CURRENT"}}}
	result := analysis.Result{Findings: []analysis.Finding{{Category: "CONSERVATION", Title: "Marca", Description: "Marca visível", Severity: "LOW", Quality: "ADEQUATE", RecommendedAction: "Verificar", Confidence: .9, EvidenceIDs: []string{"unknown"}}}}
	if err := validateEvidence(result, request); err == nil {
		t.Fatal("unknown evidence accepted")
	}
	result.Findings[0].EvidenceIDs = []string{"known"}
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
		result.Findings[0] = analysis.Finding{Category: category, Title: "Observação", Description: "Observação visual", Severity: severity, Quality: quality, RecommendedAction: "Verificar", Confidence: .8499, EvidenceIDs: []string{"known"}}
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
