package process_comparison

import (
	"context"
	"testing"
	"time"

	analysis "inspection/services/inspection/internal/features/analysis/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"

	"gorm.io/gorm"
)

type gateway struct{ response llm.StructuredResult }

func (g gateway) CompleteStructured(context.Context, llm.StructuredRequest) (llm.StructuredResult, error) {
	return g.response, nil
}

func TestValidateEvidenceRejectsUnknownAndLowConfidence(t *testing.T) {
	request := llm.StructuredRequest{MinimumConfidenceBPS: 7000, Images: []llm.NormalizedImage{{EvidenceID: "known"}}}
	result := analysis.Result{CoverageStatus: "COMPLETE", ComparisonStatus: "CHANGED", Findings: []analysis.Finding{{Category: "CONSERVATION", ChangeType: "NEW_DAMAGE", Confidence: .9, EvidenceIDs: []string{"unknown"}}}}
	if err := validateEvidence(result, request); err == nil {
		t.Fatal("unknown evidence accepted")
	}
	result.Findings[0].EvidenceIDs = []string{"known"}
	result.Findings[0].Confidence = .69
	if err := validateEvidence(result, request); err == nil {
		t.Fatal("low confidence accepted")
	}
}

func TestIsInsufficientEvidence(t *testing.T) {
	if !isInsufficientEvidence(analysis.Result{CoverageStatus: "INSUFFICIENT", ComparisonStatus: "INCONCLUSIVE", Findings: []analysis.Finding{{Category: "EVIDENCE_QUALITY", ChangeType: "NOT_APPLICABLE", Quality: "INSUFFICIENT"}}}) {
		t.Fatal("insufficient evidence not detected")
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
