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
	Category, Title, Description, Severity, RecommendedAction string
	Confidence                                                float64
	EvidenceIDs                                               []string
	Quality                                                   string
}

// Result is the strict structured response accepted from an analysis gateway.
// NoRelevantChange makes an empty findings list meaningful rather than ambiguous.
type Result struct {
	NoRelevantChange bool
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
		NoRelevantChange bool `json:"noRelevantChange"`
		Findings         []struct {
			Category          string   `json:"category"`
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
	result := Result{NoRelevantChange: wire.NoRelevantChange, Findings: make([]Finding, 0, len(wire.Findings))}
	for _, finding := range wire.Findings {
		result.Findings = append(result.Findings, Finding{Category: finding.Category, Title: finding.Title, Description: finding.Description, Severity: finding.Severity, Confidence: finding.Confidence, EvidenceIDs: finding.EvidenceIDs, Quality: finding.Quality, RecommendedAction: finding.RecommendedAction})
	}
	return result, ValidateResult(result)
}

// ComparisonFacts are terminal facts only. They are sufficient to reproduce a
// classification from the inspection's pinned analysis-profile version.
type ComparisonFacts struct {
	Terminal, Inconclusive, Missing, Skipped, Flagged, Uncorrected bool
	Findings                                                       []Finding
}

// Decision is the sole final classification plus stable reason codes.
type Decision struct {
	Classification string
	ReasonCodes    []string
}

// ValidateResult rejects loosely shaped provider output before it can become a
// finding. Empty findings are accepted only for an explicit no-change outcome.
func ValidateResult(result Result) error {
	if len(result.Findings) == 0 && !result.NoRelevantChange {
		return fmt.Errorf("%w: empty findings require noRelevantChange", ErrInvalidStructuredOutput)
	}
	if result.NoRelevantChange && len(result.Findings) != 0 {
		return fmt.Errorf("%w: noRelevantChange cannot include findings", ErrInvalidStructuredOutput)
	}
	for _, finding := range result.Findings {
		if strings.TrimSpace(finding.Category) == "" || !validText(finding.Title, MaxTitle) || !validText(finding.Description, MaxDescription) || !validText(finding.RecommendedAction, MaxAction) || len(finding.EvidenceIDs) == 0 {
			return fmt.Errorf("%w: finding fields are incomplete", ErrInvalidStructuredOutput)
		}
		if finding.Confidence < 0 || finding.Confidence > 1 || !validSeverity(finding.Severity) || strings.TrimSpace(finding.Quality) == "" {
			return fmt.Errorf("%w: finding severity, confidence, or quality is invalid", ErrInvalidStructuredOutput)
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

func assignsConsequence(value string) bool {
	value = strings.ToLower(value)
	for _, prohibited := range []string{"fault", "liable", "liability", "blame", "penalty", "fine", "charge", "cost", "compensation", "culpa", "responsável", "multa", "cobrar", "custo"} {
		if strings.Contains(value, prohibited) {
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
