package generate_snapshot

import (
	"strings"

	report "inspection/services/inspection/internal/features/reports/core"
)

// Report-level scope statements for video snapshots. They keep conclusions
// within what the sealed plan actually observed.
const (
	VideoNoReferenceStatement            = "Nenhuma comparação com referência foi realizada; o laudo descreve somente a condição atual observada no vídeo."
	VideoNoComparativeConclusion         = "As evidências não sustentam conclusão comparativa: todos os itens comparados ficaram inconclusivos ou com falha técnica."
	videoObservedScopePrefix             = "Conclusões limitadas ao escopo observado: "
	videoObservedScopeWithoutPropertyEnd = ". Isto não constitui avaliação do imóvel inteiro."
)

// ReferenceFree is the reference version recorded for inspections compared
// against no baseline.
const ReferenceFree = "reference-free"

// hasReference reports whether the snapshot pins a comparison baseline.
func hasReference(referenceVersionID string) bool {
	return referenceVersionID != "" && referenceVersionID != ReferenceFree
}

// VideoScopeStatements derives deterministic statements from the plan's
// reference and the requirement projection of a video snapshot.
func VideoScopeStatements(hasReference bool, requirements []report.Requirement) []string {
	if len(requirements) == 0 {
		return nil
	}
	comparativeCompleted := false
	observed := make([]string, 0, len(requirements))
	seen := map[string]struct{}{}
	for _, requirement := range requirements {
		if requirement.AnalysisMode == "COMPARE_ORIGIN_CURRENT" && requirement.AnalysisStatus == "COMPLETED" {
			comparativeCompleted = true
		}
		label := strings.TrimSpace(requirement.Label)
		if requirement.AnalysisStatus != "COMPLETED" || label == "" {
			continue
		}
		if _, ok := seen[label]; !ok {
			seen[label] = struct{}{}
			observed = append(observed, label)
		}
	}
	statements := []string{}
	if !hasReference {
		statements = append(statements, VideoNoReferenceStatement)
	}
	if hasReference && !comparativeCompleted {
		statements = append(statements, VideoNoComparativeConclusion)
	}
	if len(observed) > 0 {
		statements = append(statements, videoObservedScopePrefix+strings.Join(observed, "; ")+videoObservedScopeWithoutPropertyEnd)
	}
	return statements
}
