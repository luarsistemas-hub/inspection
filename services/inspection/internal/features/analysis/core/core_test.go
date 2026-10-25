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

func TestClassifyInventoryAppliesReasonCodesAndPriority(t *testing.T) {
	base := Decision{Classification: ClassificationNormal}
	got := ClassifyInventory(base, InventoryFacts{Worsened: 1, NotObserved: 1, BaselineItems: 2})
	if got.Classification != ClassificationAttention || len(got.ReasonCodes) != 2 || got.ReasonCodes[0] != "INVENTORY_CONDITION_WORSENED" || got.ReasonCodes[1] != "INVENTORY_ITEM_NOT_OBSERVED" {
		t.Fatalf("unexpected inventory decision: %+v", got)
	}
	got = ClassifyInventory(got, InventoryFacts{Missing: 1, BaselineItems: 2})
	if got.Classification != ClassificationCritical || got.ReasonCodes[len(got.ReasonCodes)-1] != "INVENTORY_ITEM_MISSING" {
		t.Fatalf("missing item must be critical: %+v", got)
	}
	got = ClassifyInventory(Decision{Classification: ClassificationNormal}, InventoryFacts{})
	if got.Classification != ClassificationAttention || got.ReasonCodes[0] != "INVENTORY_EMPTY_BASELINE" {
		t.Fatalf("empty inventory must require attention: %+v", got)
	}
}

func TestClassifyInventoryRetainsAllReasonsWhenMissingIsCritical(t *testing.T) {
	got := ClassifyInventory(Decision{Classification: ClassificationNormal}, InventoryFacts{Missing: 1, Worsened: 1, NotObserved: 2, EnvironmentsNotCaptured: 1, BaselineItems: 4})
	require.Equal(t, ClassificationCritical, got.Classification)
	require.Equal(t, []string{"INVENTORY_ITEM_MISSING", "INVENTORY_CONDITION_WORSENED", "INVENTORY_ITEM_NOT_OBSERVED", "INVENTORY_ENVIRONMENT_NOT_CAPTURED"}, got.ReasonCodes)
}

func TestParseResultPresentsFriendlyImageLabels(t *testing.T) {
	payload := []byte(`{"noRelevantChange":false,"findings":[{"category":"OBSTRUCTION","title":"Obstrução · CURRENT","description":"Na imagem CURRENT, um saco encobre o gabinete. Não estava presente na imagem ORIGIN.","severity":"LOW","confidence":0.9,"evidenceIds":["current","origin"],"quality":"ADEQUATE","recommendedAction":"Refazer a captura de CURRENT e comparar com ORIGIN."}]}`)
	result, err := ParseResult(payload)
	require.NoError(t, err)
	require.Equal(t, "Obstrução · vistoria atual", result.Findings[0].Title)
	require.Equal(t, "Na vistoria atual, um saco encobre o gabinete. Não estava presente na imagem de referência.", result.Findings[0].Description)
	require.Equal(t, "Refazer a captura da vistoria atual e comparar com a imagem de referência.", result.Findings[0].RecommendedAction)
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

func TestUT240To248InventoryClassificationMatrix(t *testing.T) {
	normal := Classify([]ComparisonFacts{{Terminal: true}})
	cases := []struct {
		id     string
		base   Decision
		facts  InventoryFacts
		want   string
		reason string
	}{
		{"UT-240", normal, InventoryFacts{BaselineItems: 3}, ClassificationNormal, ""},
		{"UT-241", normal, InventoryFacts{Worsened: 1, BaselineItems: 3}, ClassificationAttention, "INVENTORY_CONDITION_WORSENED"},
		{"UT-242", normal, InventoryFacts{NotObserved: 1, BaselineItems: 3}, ClassificationAttention, "INVENTORY_ITEM_NOT_OBSERVED"},
		{"UT-243", normal, InventoryFacts{EnvironmentsNotCaptured: 1, BaselineItems: 3}, ClassificationAttention, "INVENTORY_ENVIRONMENT_NOT_CAPTURED"},
		{"UT-244", normal, InventoryFacts{Missing: 1, BaselineItems: 3}, ClassificationCritical, "INVENTORY_ITEM_MISSING"},
		{"UT-245", Classify([]ComparisonFacts{{Terminal: true, Findings: []Finding{{Severity: "CRITICAL"}}}}), InventoryFacts{BaselineItems: 3}, ClassificationCritical, ""},
		{"UT-246", Classify([]ComparisonFacts{{Terminal: true, Findings: []Finding{{Severity: "HIGH"}}}}), InventoryFacts{BaselineItems: 3}, ClassificationAttention, ""},
		// UT-247: outside-inventory and improved items are not inventory facts.
		{"UT-247", normal, InventoryFacts{BaselineItems: 3}, ClassificationNormal, ""},
		{"UT-248", normal, InventoryFacts{}, ClassificationAttention, "INVENTORY_EMPTY_BASELINE"},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			got := ClassifyInventory(tc.base, tc.facts)
			require.Equal(t, tc.want, got.Classification)
			if tc.reason != "" {
				require.Contains(t, got.ReasonCodes, tc.reason)
			}
		})
	}
}
