// Package core owns deterministic analysis validation and classification.
package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	MaxAttempts             = 4
	MaxDescription          = 2000
	MaxAction               = 2000
	MaxTitle                = 200
	MinimumConfidenceBPS    = 8500
	ClassificationNormal    = "NORMAL"
	ClassificationAttention = "ATTENTION"
	ClassificationCritical  = "CRITICAL"
)

var ErrInvalidStructuredOutput = errors.New("invalid structured analysis output")

var customerImageLabels = []struct {
	pattern *regexp.Regexp
	replace string
}{
	{regexp.MustCompile(`(?i)\bde\s+CURRENT\b`), "da vistoria atual"},
	{regexp.MustCompile(`(?i)\bcom\s+CURRENT\b`), "com a vistoria atual"},
	{regexp.MustCompile(`(?i)\bcom\s+ORIGIN\b`), "com a imagem de referência"},
	{regexp.MustCompile(`(?i)\bem\s+ORIGIN\b`), "na imagem de referência"},
	{regexp.MustCompile(`(?i)\bna imagem\s+CURRENT\b`), "na vistoria atual"},
	{regexp.MustCompile(`(?i)\bda imagem\s+CURRENT\b`), "da vistoria atual"},
	{regexp.MustCompile(`(?i)\bimagem\s+CURRENT\b`), "vistoria atual"},
	{regexp.MustCompile(`(?i)\bna imagem\s+ORIGIN\b`), "na imagem de referência"},
	{regexp.MustCompile(`(?i)\bda imagem\s+ORIGIN\b`), "da imagem de referência"},
	{regexp.MustCompile(`(?i)\bimagem\s+ORIGIN\b`), "imagem de referência"},
	{regexp.MustCompile(`(?i)\bCURRENT\b`), "vistoria atual"},
	{regexp.MustCompile(`(?i)\bORIGIN\b`), "imagem de referência"},
}

// Finding is the provider-neutral result of one visual observation.
type Finding struct {
	Category, Title, Description, Severity, RecommendedAction string
	Confidence                                                float64
	EvidenceIDs                                               []string
	Quality                                                   string
}

// Result is the strict structured response accepted from an analysis gateway.
type Result struct {
	NoRelevantChange *bool
	Findings         []Finding
}

// ParseResult accepts only the current analysis contract and rejects extra fields.
func ParseResult(data []byte) (Result, error) {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var wire struct {
		NoRelevantChange *bool `json:"noRelevantChange"`
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
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Result{}, fmt.Errorf("%w: malformed JSON", ErrInvalidStructuredOutput)
	}
	if _, ok := fields["noRelevantChange"]; !ok {
		return Result{}, fmt.Errorf("%w: noRelevantChange is required", ErrInvalidStructuredOutput)
	}
	if _, ok := fields["findings"]; !ok {
		return Result{}, fmt.Errorf("%w: findings are required", ErrInvalidStructuredOutput)
	}
	if len(fields["findings"]) == 0 || fields["findings"][0] != '[' {
		return Result{}, fmt.Errorf("%w: findings must be an array", ErrInvalidStructuredOutput)
	}
	result := Result{NoRelevantChange: wire.NoRelevantChange, Findings: make([]Finding, 0, len(wire.Findings))}
	for _, finding := range wire.Findings {
		result.Findings = append(result.Findings, Finding{Category: finding.Category, Title: presentCustomerImageLabels(finding.Title), Description: presentCustomerImageLabels(finding.Description), Severity: finding.Severity, Confidence: finding.Confidence, EvidenceIDs: finding.EvidenceIDs, Quality: finding.Quality, RecommendedAction: presentCustomerImageLabels(finding.RecommendedAction)})
	}
	return result, ValidateResult(result)
}

func presentCustomerImageLabels(value string) string {
	for _, label := range customerImageLabels {
		value = label.pattern.ReplaceAllStringFunc(value, func(match string) string {
			if strings.HasPrefix(match, "Na imagem") || strings.HasPrefix(match, "Da imagem") {
				return strings.ToUpper(label.replace[:1]) + label.replace[1:]
			}
			return label.replace
		})
	}
	return value
}

// ComparisonFacts are terminal facts only. They are sufficient to reproduce a classification.
type ComparisonFacts struct {
	Terminal, Inconclusive, TechnicalFailure, Missing, Skipped, Flagged, Uncorrected bool
	Findings                                                                         []Finding
}

// Decision is the sole final classification plus stable reason codes.
type Decision struct {
	Classification string
	ReasonCodes    []string
}

// ValidateResult rejects malformed findings independently of comparison mode.
func ValidateResult(result Result) error {
	for _, finding := range result.Findings {
		if !validCategory(finding.Category) || !validText(finding.Title, MaxTitle) || !validText(finding.Description, MaxDescription) || !validText(finding.RecommendedAction, MaxAction) || len(finding.EvidenceIDs) == 0 {
			return fmt.Errorf("%w: finding fields are incomplete", ErrInvalidStructuredOutput)
		}
		if finding.Confidence < 0 || finding.Confidence > 1 || !validSeverity(finding.Severity) || !validQuality(finding.Quality) {
			return fmt.Errorf("%w: finding severity, confidence, or quality is invalid", ErrInvalidStructuredOutput)
		}
		if finding.Category == "EVIDENCE_QUALITY" {
			if finding.Severity != "NONE" || finding.Quality != "INSUFFICIENT" {
				return fmt.Errorf("%w: evidence quality finding is malformed", ErrInvalidStructuredOutput)
			}
		} else if finding.Severity == "NONE" {
			return fmt.Errorf("%w: NONE severity is exclusive to evidence quality", ErrInvalidStructuredOutput)
		}
		for _, id := range finding.EvidenceIDs {
			if strings.TrimSpace(id) == "" {
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
func validCategory(value string) bool {
	return value == "CONSERVATION" || value == "INVENTORY" || value == "CLEANLINESS" || value == "OBSTRUCTION" || value == "EVIDENCE_QUALITY"
}
func validText(value string, maximum int) bool {
	value = strings.TrimSpace(value)
	return value != "" && utf8.RuneCountInString(value) <= maximum
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
