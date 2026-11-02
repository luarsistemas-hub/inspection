package core

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAIP_UT_034ClassifierMapsTerminalFindingsToCategoryAndOrderedReasons covers
// UT-034 of autonomous-inspection-platform: severities map to exactly one
// category and the reason codes are stable, deduplicated and ordered by the
// first condition observed.
func TestAIP_UT_034ClassifierMapsTerminalFindingsToCategoryAndOrderedReasons(t *testing.T) {
	cases := []struct {
		name    string
		input   []ComparisonFacts
		want    string
		reasons []string
	}{
		{"terminal without findings is normal", []ComparisonFacts{{Terminal: true}}, ClassificationNormal, []string{}},
		{"evidence quality finding carries no severity", []ComparisonFacts{{Terminal: true, Findings: []Finding{{Severity: "NONE"}}}}, ClassificationNormal, []string{}},
		{"low severity requires attention", []ComparisonFacts{{Terminal: true, Findings: []Finding{{Severity: "LOW"}}}}, ClassificationAttention, []string{"OBSERVED_CHANGE"}},
		{"high severity is still attention", []ComparisonFacts{{Terminal: true, Findings: []Finding{{Severity: "HIGH"}}}}, ClassificationAttention, []string{"OBSERVED_CHANGE"}},
		{"critical severity is critical", []ComparisonFacts{{Terminal: true, Findings: []Finding{{Severity: "CRITICAL"}}}}, ClassificationCritical, []string{"CRITICAL_FINDING"}},
		{"reasons keep first-seen order and are deduplicated", []ComparisonFacts{
			{Terminal: true, Flagged: true, Findings: []Finding{{Severity: "MEDIUM"}, {Severity: "CRITICAL"}, {Severity: "LOW"}, {Severity: "CRITICAL"}}},
			{Terminal: true, Flagged: true, Findings: []Finding{{Severity: "HIGH"}}},
		}, ClassificationCritical, []string{"EVIDENCE_FLAG", "OBSERVED_CHANGE", "CRITICAL_FINDING"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.input)
			require.Equal(t, tc.want, got.Classification)
			require.Equal(t, tc.reasons, append([]string{}, got.ReasonCodes...))
			// Determinism: classifying the same facts again never changes the decision.
			require.Equal(t, got, Classify(tc.input))
		})
	}
	// A critical finding is never hidden by a later non-critical comparison.
	mixed := Classify([]ComparisonFacts{{Terminal: true, Findings: []Finding{{Severity: "CRITICAL"}}}, {Terminal: true}})
	require.Equal(t, ClassificationCritical, mixed.Classification)
}

// TestAIP_UT_035MissingOrFailedComparisonsAreNeverConforming covers UT-035:
// every missing, skipped, flagged, uncorrected, failed, pending or inconclusive
// condition forces at least ATTENTION.
func TestAIP_UT_035MissingOrFailedComparisonsAreNeverConforming(t *testing.T) {
	cases := map[string]struct {
		facts  []ComparisonFacts
		reason string
	}{
		"no comparison at all":     {nil, "NO_COMPARISONS"},
		"pending comparison":       {[]ComparisonFacts{{}}, "PENDING_COMPARISON"},
		"inconclusive comparison":  {[]ComparisonFacts{{Terminal: true, Inconclusive: true}}, "INCONCLUSIVE"},
		"technical failure":        {[]ComparisonFacts{{Terminal: true, TechnicalFailure: true}}, "ANALYSIS_FAILED"},
		"missing evidence":         {[]ComparisonFacts{{Terminal: true, Missing: true}}, "MISSING_EVIDENCE"},
		"skipped stage":            {[]ComparisonFacts{{Terminal: true, Skipped: true}}, "SKIPPED_STAGE"},
		"flagged evidence":         {[]ComparisonFacts{{Terminal: true, Flagged: true}}, "EVIDENCE_FLAG"},
		"uncorrected recapture":    {[]ComparisonFacts{{Terminal: true, Uncorrected: true}}, "UNCORRECTED_RECAPTURE"},
		"terminal fallback":        {[]ComparisonFacts{TerminalFallback()}, "INCONCLUSIVE"},
		"one failed among normal":  {[]ComparisonFacts{{Terminal: true}, {Terminal: true, TechnicalFailure: true}, {Terminal: true}}, "ANALYSIS_FAILED"},
		"missing even when no row": {[]ComparisonFacts{{Terminal: true, Missing: true, Findings: nil}}, "MISSING_EVIDENCE"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := Classify(tc.facts)
			require.NotEqual(t, ClassificationNormal, got.Classification, "an incomplete comparison must never be NORMAL")
			require.Equal(t, ClassificationAttention, got.Classification)
			require.Contains(t, got.ReasonCodes, tc.reason)
		})
	}
	// Stricter outcomes are kept: a failure next to a critical finding stays CRITICAL.
	got := Classify([]ComparisonFacts{{Terminal: true, TechnicalFailure: true}, {Terminal: true, Findings: []Finding{{Severity: "CRITICAL"}}}})
	require.Equal(t, ClassificationCritical, got.Classification)
	require.Equal(t, []string{"ANALYSIS_FAILED", "CRITICAL_FINDING"}, got.ReasonCodes)
}

// TestAIP_UT_067AnalysisRejectsBlameCostMissingEvidenceAndConfidenceRange
// covers UT-067: neutral observed-change language only, with bounded
// confidence and at least one evidence reference per finding.
func TestAIP_UT_067AnalysisRejectsBlameCostMissingEvidenceAndConfidenceRange(t *testing.T) {
	valid := func() Finding { return finding("CONSERVATION", "LOW", "ADEQUATE", .9) }
	require.NoError(t, ValidateResult(Result{Findings: []Finding{valid()}}))

	mutate := func(change func(*Finding)) error {
		item := valid()
		change(&item)
		return ValidateResult(Result{Findings: []Finding{item}})
	}
	t.Run("missing evidence", func(t *testing.T) {
		require.ErrorIs(t, mutate(func(f *Finding) { f.EvidenceIDs = nil }), ErrInvalidStructuredOutput)
		require.ErrorIs(t, mutate(func(f *Finding) { f.EvidenceIDs = []string{"  "} }), ErrInvalidStructuredOutput)
	})
	t.Run("confidence range", func(t *testing.T) {
		require.ErrorIs(t, mutate(func(f *Finding) { f.Confidence = -0.01 }), ErrInvalidStructuredOutput)
		require.ErrorIs(t, mutate(func(f *Finding) { f.Confidence = 1.01 }), ErrInvalidStructuredOutput)
		require.NoError(t, mutate(func(f *Finding) { f.Confidence = 0 }))
		require.NoError(t, mutate(func(f *Finding) { f.Confidence = 1 }))
	})
	t.Run("unknown vocabulary", func(t *testing.T) {
		require.ErrorIs(t, mutate(func(f *Finding) { f.Category = "REQUIREMENT_X" }), ErrInvalidStructuredOutput)
		require.ErrorIs(t, mutate(func(f *Finding) { f.Severity = "SEVERE" }), ErrInvalidStructuredOutput)
		require.ErrorIs(t, mutate(func(f *Finding) { f.Quality = "GOOD" }), ErrInvalidStructuredOutput)
	})
	t.Run("blame and cost in every presented field", func(t *testing.T) {
		for _, word := range []string{"culpa", "Responsável", "responsabilidade", "multa", "custo", "cobrança", "indenização", "at fault", "liable", "liability", "blame", "penalty", "cost", "compensation", "charged for",
			"cobra", "cobrou", "cobrará", "custa", "custará", "culpam", "indeniza", "indenizou", "multou", "penaliza", "responsabiliza", "arcar", "ressarcimento", "reembolso", "prejuízo", "negligência", "R$ 350", "R$350", "200 reais",
			"responsible", "responsibility", "negligence", "reimburse", "fined", "a fine of", "an extra charge", "the tenant's fault"} {
			for field, set := range map[string]func(*Finding, string){
				"title":       func(f *Finding, text string) { f.Title = text },
				"description": func(f *Finding, text string) { f.Description = text },
				"action":      func(f *Finding, text string) { f.RecommendedAction = text },
			} {
				text := fmt.Sprintf("Observação com %s, registrada.", word)
				err := mutate(func(f *Finding) { set(f, text) })
				require.ErrorIsf(t, err, ErrInvalidStructuredOutput, "%s in %s was accepted", word, field)
				require.Containsf(t, err.Error(), "fault, cost, or automatic consequence", "%s in %s was rejected for another reason", word, field)
			}
		}
	})
	t.Run("decomposed accents cannot hide a prohibited word", func(t *testing.T) {
		// "responsável", "indenização" and "negligência" written with combining accents (NFD).
		for _, word := range []string{"responsa\u0301vel", "indenizac\u0327a\u0303o", "neglige\u0302ncia", "cobranc\u0327a"} {
			err := mutate(func(f *Finding) { f.Description = "Observação com " + word + ", registrada." })
			require.ErrorIsf(t, err, ErrInvalidStructuredOutput, "%q was accepted", word)
		}
	})
	t.Run("neutral technical vocabulary is not blame or cost", func(t *testing.T) {
		for _, text := range []string{
			"Fissuras finas (fine cracks) na parede, com fine dust no rodapé.",
			"A fault line aparece no reboco, sem relação com pessoas.",
			"Bateria fully charged no suporte da parede.",
			"Há uma fine layer de poeira sobre a bancada.",
			"A viga arcou levemente e há uma arca de madeira no canto.",
			"A costa do imóvel fica ao norte e a porta está desalinhada.",
			"Mancha escura de dez centímetros na parede norte, 2 metros acima do piso.",
		} {
			require.NoErrorf(t, mutate(func(f *Finding) { f.Description = text }), "neutral text was rejected: %s", text)
		}
	})
	t.Run("neutral observation text is accepted", func(t *testing.T) {
		require.NoError(t, mutate(func(f *Finding) {
			f.Title = "Marca visível na parede"
			f.Description = "Há uma mancha escura de cerca de dez centímetros na parede norte, ausente na referência."
			f.RecommendedAction = "Registrar a condição observada e fotografar novamente."
		}))
	})
	t.Run("whole payload is rejected before any finding is kept", func(t *testing.T) {
		payload := `{"noRelevantChange":false,"findings":[{"category":"CONSERVATION","title":"Parede","description":"O inquilino é o culpado pela mancha.","severity":"LOW","confidence":0.9,"evidenceIds":["current"],"quality":"ADEQUATE","recommendedAction":"Verificar."},{"category":"CONSERVATION","title":"Piso","description":"Marca visível no piso.","severity":"LOW","confidence":0.9,"evidenceIds":["current"],"quality":"ADEQUATE","recommendedAction":"Verificar."}]}`
		result, err := ParseResult([]byte(payload))
		require.ErrorIs(t, err, ErrInvalidStructuredOutput)
		require.NotContains(t, strings.ToLower(fmt.Sprint(err)), "piso")
		_ = result
	})
}
