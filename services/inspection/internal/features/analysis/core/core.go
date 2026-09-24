// Package core owns deterministic analysis validation and classification.
package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	MaxAttempts             = 4 // initial attempt plus the three broker retries
	MaxDescription          = 2000
	MaxAction               = 2000
	MaxTitle                = 200
	ClassificationNormal    = "NORMAL"
	ClassificationAttention = "ATTENTION"
	ClassificationCritical  = "CRITICAL"
)

var ErrInvalidStructuredOutput = errors.New("invalid structured analysis output")

// Finding is the provider-neutral, immutable result of one observation.
// It deliberately has no fault, cost, liability, or automatic-consequence field.
type Finding struct {
	Category, ChangeType, Title, Description, Severity, RecommendedAction string
	Confidence                                                            float64
	EvidenceIDs                                                           []string
	Quality                                                               string
}

// Result is the strict structured response accepted from an analysis gateway.
// Coverage and comparison status make an empty findings list unambiguous.
type Result struct {
	CoverageStatus   string
	ComparisonStatus string
	Findings         []Finding
}

// ParseResult accepts only one JSON object with the documented structured
// result shape. Provider prose is intentionally never interpreted as a
// finding; callers must turn a rejected response into a bounded retry or the
// terminal inconclusive fallback.
func ParseResult(data []byte) (Result, error) {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var wire struct {
		CoverageStatus   string `json:"coverageStatus"`
		ComparisonStatus string `json:"comparisonStatus"`
		Findings         []struct {
			Category          string   `json:"category"`
			ChangeType        string   `json:"changeType"`
			Title             string   `json:"title"`
			Description       string   `json:"description"`
			Severity          string   `json:"severity"`
			Confidence        float64  `json:"confidence"`
			EvidenceIDs       []string `json:"evidenceIds"`
			Quality           string   `json:"quality"`
			RecommendedAction string   `json:"recommendedAction"`
		} `json:"findings"`
	}
	if err := decoder.Decode(&wire); err != nil {
		return Result{}, fmt.Errorf("%w: malformed JSON", ErrInvalidStructuredOutput)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Result{}, fmt.Errorf("%w: trailing JSON", ErrInvalidStructuredOutput)
	}
	result := Result{CoverageStatus: wire.CoverageStatus, ComparisonStatus: wire.ComparisonStatus, Findings: make([]Finding, 0, len(wire.Findings))}
	for _, finding := range wire.Findings {
		result.Findings = append(result.Findings, Finding{Category: finding.Category, ChangeType: finding.ChangeType, Title: finding.Title, Description: finding.Description, Severity: finding.Severity, Confidence: finding.Confidence, EvidenceIDs: finding.EvidenceIDs, Quality: finding.Quality, RecommendedAction: finding.RecommendedAction})
	}
	return result, ValidateResult(result)
}

// ComparisonFacts are terminal facts only. They are sufficient to reproduce a
// classification from the inspection's pinned analysis-profile version.
type ComparisonFacts struct {
	Terminal, Inconclusive, TechnicalFailure, Missing, Skipped, Flagged, Uncorrected bool
	Findings                                                                         []Finding
}

// Decision is the sole final classification plus stable reason codes.
type Decision struct {
	Classification string
	ReasonCodes    []string
}

// ValidateResult rejects loosely shaped provider output before it can become a
// finding. Empty findings are accepted only for an explicit no-change outcome.
func ValidateResult(result Result) error {
	if !validCoverage(result.CoverageStatus) || !validComparison(result.ComparisonStatus) {
		return fmt.Errorf("%w: coverage or comparison status is invalid", ErrInvalidStructuredOutput)
	}
	if len(result.Findings) == 0 && !(result.CoverageStatus == "COMPLETE" && (result.ComparisonStatus == "UNCHANGED" || result.ComparisonStatus == "NOT_APPLICABLE")) {
		return fmt.Errorf("%w: empty findings require complete unchanged or current-only result", ErrInvalidStructuredOutput)
	}
	if result.ComparisonStatus == "UNCHANGED" && (result.CoverageStatus != "COMPLETE" || len(result.Findings) != 0) {
		return fmt.Errorf("%w: unchanged requires complete coverage and no findings", ErrInvalidStructuredOutput)
	}
	if result.ComparisonStatus == "CHANGED" && len(result.Findings) == 0 {
		return fmt.Errorf("%w: changed requires findings", ErrInvalidStructuredOutput)
	}
	if result.ComparisonStatus == "CHANGED" && allEvidenceQualityFindings(result.Findings) {
		return fmt.Errorf("%w: changed requires a documented change", ErrInvalidStructuredOutput)
	}
	if result.ComparisonStatus == "INCONCLUSIVE" && !hasEvidenceQualityFinding(result.Findings) {
		return fmt.Errorf("%w: inconclusive requires evidence quality finding", ErrInvalidStructuredOutput)
	}
	if result.CoverageStatus != "COMPLETE" && !hasEvidenceQualityFinding(result.Findings) {
		return fmt.Errorf("%w: incomplete coverage requires evidence quality finding", ErrInvalidStructuredOutput)
	}
	for _, finding := range result.Findings {
		if !validCategory(finding.Category) || !validChangeType(finding.ChangeType) || !validText(finding.Title, MaxTitle) || !validText(finding.Description, MaxDescription) || !validText(finding.RecommendedAction, MaxAction) || len(finding.EvidenceIDs) == 0 {
			return fmt.Errorf("%w: finding fields are incomplete", ErrInvalidStructuredOutput)
		}
		if finding.Confidence < 0 || finding.Confidence > 1 || !validSeverity(finding.Severity) || !validQuality(finding.Quality) {
			return fmt.Errorf("%w: finding severity, confidence, or quality is invalid", ErrInvalidStructuredOutput)
		}
		if finding.Category == "EVIDENCE_QUALITY" {
			if finding.Severity != "NONE" || finding.Quality != "INSUFFICIENT" || finding.ChangeType != "NOT_APPLICABLE" {
				return fmt.Errorf("%w: evidence quality finding is malformed", ErrInvalidStructuredOutput)
			}
		} else if finding.ChangeType == "CURRENT_CONDITION" && result.ComparisonStatus != "NOT_APPLICABLE" {
			return fmt.Errorf("%w: current condition is only valid without origin", ErrInvalidStructuredOutput)
		} else if result.ComparisonStatus == "NOT_APPLICABLE" && finding.ChangeType != "CURRENT_CONDITION" {
			return fmt.Errorf("%w: current-only result cannot contain temporal change", ErrInvalidStructuredOutput)
		}
		for _, evidenceID := range finding.EvidenceIDs {
			if strings.TrimSpace(evidenceID) == "" {
				return fmt.Errorf("%w: evidence reference is required", ErrInvalidStructuredOutput)
			}
		}
		if assignsConsequence(finding.Title) || assignsConsequence(finding.Description) || assignsConsequence(finding.RecommendedAction) {
			return fmt.Errorf("%w: finding assigns fault, cost, or automatic consequence", ErrInvalidStructuredOutput)
		}
	}
	return nil
}

func validQuality(value string) bool {
	return value == "ADEQUATE" || value == "LIMITED" || value == "INSUFFICIENT"
}

func validCoverage(value string) bool {
	return value == "COMPLETE" || value == "PARTIAL" || value == "INSUFFICIENT"
}
func validComparison(value string) bool {
	return value == "CHANGED" || value == "UNCHANGED" || value == "INCONCLUSIVE" || value == "NOT_APPLICABLE"
}
func validCategory(value string) bool {
	return value == "CONSERVATION" || value == "INVENTORY" || value == "EVIDENCE_QUALITY"
}
func validChangeType(value string) bool {
	switch value {
	case "CURRENT_CONDITION", "NEW_DAMAGE", "WORSENED", "REMOVED", "ADDED", "REPLACED", "MOVED", "IMPROVED", "NOT_APPLICABLE":
		return true
	}
	return false
}
func hasEvidenceQualityFinding(findings []Finding) bool {
	for _, finding := range findings {
		if finding.Category == "EVIDENCE_QUALITY" && finding.Severity == "NONE" && finding.Quality == "INSUFFICIENT" && finding.ChangeType == "NOT_APPLICABLE" {
			return true
		}
	}
	return false
}

func allEvidenceQualityFindings(findings []Finding) bool {
	if len(findings) == 0 {
		return false
	}
	for _, finding := range findings {
		if finding.Category != "EVIDENCE_QUALITY" {
			return false
		}
	}
	return true
}

func assignsConsequence(value string) bool {
	prohibitedWords := map[string]struct{}{}
	for _, word := range []string{"fault", "liable", "liability", "blame", "penalty", "fine", "charge", "cost", "compensation", "culpa", "responsável", "responsabilidade", "responsabilização", "multa", "penalidade", "cobrar", "cobrança", "custo", "indenização"} {
		prohibitedWords[word] = struct{}{}
	}
	for _, word := range strings.Fields(strings.ToLower(value)) {
		word = strings.Trim(word, ".,;:!?()[]{}\"'")
		if _, prohibited := prohibitedWords[word]; prohibited {
			return true
		}
	}
	return false
}

// TerminalFallback converts exhausted provider retries or insufficient input to
// a visible terminal outcome, ensuring downstream reporting never blocks.
func TerminalFallback() ComparisonFacts {
	return ComparisonFacts{Terminal: true, Inconclusive: true}
}

// Classify applies only deterministic terminal facts; model prose never makes
// the final priority decision. Every non-complete condition forces ATTENTION.
func Classify(comparisons []ComparisonFacts) Decision {
	reasons := make([]string, 0)
	critical := false
	attention := false
	if len(comparisons) == 0 {
		return Decision{Classification: ClassificationAttention, ReasonCodes: []string{"NO_COMPARISONS"}}
	}
	for _, comparison := range comparisons {
		if !comparison.Terminal {
			attention = true
			reasons = appendReason(reasons, "PENDING_COMPARISON")
		}
		if comparison.Inconclusive {
			attention = true
			reasons = appendReason(reasons, "INCONCLUSIVE")
		}
		if comparison.TechnicalFailure {
			attention = true
			reasons = appendReason(reasons, "ANALYSIS_FAILED")
		}
		if comparison.Missing {
			attention = true
			reasons = appendReason(reasons, "MISSING_EVIDENCE")
		}
		if comparison.Skipped {
			attention = true
			reasons = appendReason(reasons, "SKIPPED_STAGE")
		}
		if comparison.Flagged {
			attention = true
			reasons = appendReason(reasons, "EVIDENCE_FLAG")
		}
		if comparison.Uncorrected {
			attention = true
			reasons = appendReason(reasons, "UNCORRECTED_RECAPTURE")
		}
		for _, finding := range comparison.Findings {
			if finding.Severity == "CRITICAL" {
				critical = true
				reasons = appendReason(reasons, "CRITICAL_FINDING")
			} else if finding.Severity != "NONE" {
				attention = true
				reasons = appendReason(reasons, "OBSERVED_CHANGE")
			}
		}
	}
	if critical {
		return Decision{Classification: ClassificationCritical, ReasonCodes: reasons}
	}
	if attention {
		return Decision{Classification: ClassificationAttention, ReasonCodes: reasons}
	}
	return Decision{Classification: ClassificationNormal, ReasonCodes: reasons}
}

func appendReason(reasons []string, reason string) []string {
	for _, existing := range reasons {
		if existing == reason {
			return reasons
		}
	}
	return append(reasons, reason)
}

func validSeverity(value string) bool {
	switch value {
	case "NONE", "LOW", "MEDIUM", "HIGH", "CRITICAL":
		return true
	}
	return false
}

func validText(value string, maximum int) bool {
	value = strings.TrimSpace(value)
	return value != "" && utf8.RuneCountInString(value) <= maximum
}
