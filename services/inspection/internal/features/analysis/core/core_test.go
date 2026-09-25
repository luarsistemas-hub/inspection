package core

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseResultRequiresNewContractAndAllowsNull(t *testing.T) {
	result, err := ParseResult([]byte(`{"noRelevantChange":null,"findings":[]}`))
	require.NoError(t, err)
	require.Nil(t, result.NoRelevantChange)
	_, err = ParseResult([]byte(`{"findings":[]}`))
	require.Error(t, err)
	_, err = ParseResult([]byte(`{"noRelevantChange":true,"findings":[],"coverageStatus":"COMPLETE"}`))
	require.Error(t, err)
}

func TestValidateResultCategoriesAndSeverity(t *testing.T) {
	for _, category := range []string{"CONSERVATION", "INVENTORY", "CLEANLINESS", "OBSTRUCTION"} {
		require.NoError(t, ValidateResult(Result{Findings: []Finding{finding(category, "LOW", "ADEQUATE", .85)}}))
	}
	require.NoError(t, ValidateResult(Result{Findings: []Finding{finding("EVIDENCE_QUALITY", "NONE", "INSUFFICIENT", .85)}}))
	require.Error(t, ValidateResult(Result{Findings: []Finding{finding("UNKNOWN", "LOW", "ADEQUATE", .9)}}))
	require.Error(t, ValidateResult(Result{Findings: []Finding{finding("CONSERVATION", "NONE", "INSUFFICIENT", .9)}}))
	require.Error(t, ValidateResult(Result{Findings: []Finding{finding("EVIDENCE_QUALITY", "LOW", "INSUFFICIENT", .9)}}))
}

func TestParseResultFindingsRemainIndependentOfQualityFindings(t *testing.T) {
	payload := map[string]any{"noRelevantChange": true, "findings": []any{
		map[string]any{"category": "OBSTRUCTION", "title": "Obstrução do gabinete inferior", "description": "Um saco encobre parte da frente do gabinete.", "severity": "LOW", "confidence": .9, "evidenceIds": []string{"current"}, "quality": "ADEQUATE", "recommendedAction": "Retirar o objeto e fotografar novamente."},
	}}
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	result, err := ParseResult(data)
	require.NoError(t, err)
	require.Len(t, result.Findings, 1)
	require.Equal(t, "OBSTRUCTION", result.Findings[0].Category)
}

func TestValidateResultDoesNotInferFromEmptyFindings(t *testing.T) {
	require.NoError(t, ValidateResult(Result{Findings: []Finding{}}))
}

func finding(category, severity, quality string, confidence float64) Finding {
	return Finding{Category: category, Title: "Observação", Description: "Condição visual observada.", Severity: severity, Confidence: confidence, EvidenceIDs: []string{"current"}, Quality: quality, RecommendedAction: "Verificar a região."}
}

func TestClassificationPrioritizesCriticalAndAttention(t *testing.T) {
	require.Equal(t, ClassificationNormal, Classify([]ComparisonFacts{{Terminal: true}}).Classification)
	require.Equal(t, ClassificationAttention, Classify([]ComparisonFacts{{Terminal: true, Inconclusive: true}}).Classification)
	require.Equal(t, ClassificationCritical, Classify([]ComparisonFacts{{Terminal: true, Findings: []Finding{{Severity: "CRITICAL"}}}}).Classification)
}
