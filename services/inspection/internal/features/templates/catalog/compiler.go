// Package catalog owns the versioned declarative template contract shared by
// template operations.
package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	SchemaVersion      = 1
	MaxNameCodePoints  = 200
	MaxTextCodePoints  = 2000
	MaxTemplateBytes   = 1 << 20
	MaxSections        = 100
	MaxRequirements    = 200
	MaxStages          = 100
	MaxAssetsPerTenant = 100000
	MaxInspections     = 1000000
	MaxActivePhotos    = 200
	MaxOriginalBytes   = 20 << 20
	DefaultGeofence    = 150
	MinGeofence        = 25
	MaxGeofence        = 10000
)

type ComparisonMode string

const (
	FixedOrigin        ComparisonMode = "FIXED_ORIGIN"
	PlannedStage       ComparisonMode = "PLANNED_STAGE"
	PreviousInspection ComparisonMode = "PREVIOUS_INSPECTION"
	BeforeAfter        ComparisonMode = "BEFORE_AFTER"
	ChecklistOnly      ComparisonMode = "CHECKLIST_ONLY"
)

type CaptureRequirement struct {
	Key                 string         `json:"key"`
	Section             string         `json:"section"`
	Label               string         `json:"label"`
	Instructions        string         `json:"instructions,omitempty"`
	EvidenceKind        string         `json:"evidenceKind"`
	MinimumCount        int            `json:"minimumCount"`
	MaximumCount        int            `json:"maximumCount"`
	Required            bool           `json:"required"`
	DescriptionRequired bool           `json:"descriptionRequired"`
	CaptureSourcePolicy string         `json:"captureSourcePolicy"`
	ComparisonTarget    ComparisonMode `json:"comparisonTarget"`
	Applicability       string         `json:"applicability,omitempty"`
}

type Stage struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Position int    `json:"position"`
}

type Policy struct {
	GPSRequired    bool `json:"gpsRequired"`
	GeofenceMeters int  `json:"geofenceMeters"`
	AllowGallery   bool `json:"allowGallery"`
}

type TemplateDocument struct {
	SchemaVersion    int                  `json:"schemaVersion"`
	SegmentVersionID string               `json:"segmentVersionId"`
	ParticipantRoles []string             `json:"participantRoles"`
	ComparisonMode   ComparisonMode       `json:"comparisonMode"`
	Requirements     []CaptureRequirement `json:"requirements"`
	MultiStage       bool                 `json:"multiStage"`
	Stages           []Stage              `json:"stages,omitempty"`
	ReportMode       string               `json:"reportMode"`
	AnalysisProfile  string               `json:"analysisProfile"`
	Policy           Policy               `json:"policy"`
}

type References interface {
	SegmentExists(string) bool
	AnalysisProfileExists(string) bool
}

type Compiled struct {
	Canonical []byte
	Digest    string
	Document  TemplateDocument
}

var safeExpression = regexp.MustCompile(`^[\pL\pN_."'()!<>=&| \t-]*$`)
var expressionToken = regexp.MustCompile(`^\s*(\(|\)|&&|\|\||==|!=|<=|>=|<|>|!|-?[0-9]+(?:\.[0-9]+)?|true|false|null|"[^"\\]*"|'[^'\\]*'|[\pL_][\pL\pN_.]*)`)

type ExpressionAST struct{ Tokens []string }

func CompileExpression(value string) (ExpressionAST, error) {
	if strings.TrimSpace(value) == "" {
		return ExpressionAST{}, nil
	}
	if !safeExpression.MatchString(value) || strings.ContainsAny(value, ";{}[]`") {
		return ExpressionAST{}, fmt.Errorf("unsafe applicability expression")
	}
	rest := value
	ast := ExpressionAST{}
	depth := 0
	for strings.TrimSpace(rest) != "" {
		match := expressionToken.FindStringSubmatchIndex(rest)
		if match == nil {
			return ExpressionAST{}, fmt.Errorf("unsafe applicability expression")
		}
		token := rest[match[2]:match[3]]
		if token == "(" {
			if len(ast.Tokens) > 0 {
				previous := ast.Tokens[len(ast.Tokens)-1]
				if regexp.MustCompile(`^[\pL_]`).MatchString(previous) && previous != "true" && previous != "false" && previous != "null" {
					return ExpressionAST{}, fmt.Errorf("function calls are forbidden")
				}
			}
			depth++
		}
		if token == ")" {
			depth--
			if depth < 0 {
				return ExpressionAST{}, fmt.Errorf("unbalanced applicability expression")
			}
		}
		ast.Tokens = append(ast.Tokens, token)
		rest = rest[match[1]:]
	}
	if depth != 0 {
		return ExpressionAST{}, fmt.Errorf("unbalanced applicability expression")
	}
	return ast, nil
}

func Compile(payload []byte, refs References) (Compiled, error) {
	if len(payload) == 0 || len(payload) > MaxTemplateBytes {
		return Compiled{}, fmt.Errorf("template size must be between 1 and %d bytes", MaxTemplateBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var doc TemplateDocument
	if err := decoder.Decode(&doc); err != nil {
		return Compiled{}, fmt.Errorf("invalid template document: %w", err)
	}
	if doc.SchemaVersion != SchemaVersion {
		return Compiled{}, fmt.Errorf("unsupported schema version")
	}
	if refs == nil || !refs.SegmentExists(doc.SegmentVersionID) || !refs.AnalysisProfileExists(doc.AnalysisProfile) {
		return Compiled{}, fmt.Errorf("unknown semantic reference")
	}
	if len(doc.Requirements) == 0 || len(doc.Requirements) > MaxRequirements {
		return Compiled{}, fmt.Errorf("capture requirements must contain 1..%d entries", MaxRequirements)
	}
	if len(doc.ParticipantRoles) == 0 {
		return Compiled{}, fmt.Errorf("at least one participant role is required")
	}
	if len(doc.Stages) > MaxStages || (!doc.MultiStage && len(doc.Stages) != 0) {
		return Compiled{}, fmt.Errorf("incompatible stage configuration")
	}
	if doc.MultiStage && len(doc.Stages) == 0 {
		return Compiled{}, fmt.Errorf("multi-stage template requires stages")
	}
	if (doc.ComparisonMode == PlannedStage || doc.ComparisonMode == BeforeAfter) && !doc.MultiStage {
		return Compiled{}, fmt.Errorf("comparison mode requires multi-stage behavior")
	}
	if doc.ReportMode == "CONSOLIDATED" && !doc.MultiStage {
		return Compiled{}, fmt.Errorf("consolidated reports require multi-stage behavior")
	}
	if doc.ReportMode != "CONSOLIDATED" && doc.ReportMode != "HISTORICAL" {
		return Compiled{}, fmt.Errorf("invalid report mode")
	}
	if !validMode(doc.ComparisonMode) {
		return Compiled{}, fmt.Errorf("invalid comparison mode")
	}
	if doc.Policy.GeofenceMeters == 0 {
		doc.Policy.GeofenceMeters = DefaultGeofence
	}
	if doc.Policy.GeofenceMeters < MinGeofence || doc.Policy.GeofenceMeters > MaxGeofence {
		return Compiled{}, fmt.Errorf("geofence out of range")
	}
	seen, sections := map[string]struct{}{}, map[string]struct{}{}
	stageKeys := map[string]struct{}{}
	for i, stage := range doc.Stages {
		if strings.TrimSpace(stage.Key) == "" || !ValidName(stage.Label) || stage.Position != i+1 {
			return Compiled{}, fmt.Errorf("invalid stage order")
		}
		if _, exists := stageKeys[stage.Key]; exists {
			return Compiled{}, fmt.Errorf("duplicate stage key")
		}
		stageKeys[stage.Key] = struct{}{}
	}
	for _, requirement := range doc.Requirements {
		if err := validateRequirement(requirement, doc.ComparisonMode); err != nil {
			return Compiled{}, err
		}
		if _, exists := seen[requirement.Key]; exists {
			return Compiled{}, fmt.Errorf("duplicate requirement key")
		}
		seen[requirement.Key] = struct{}{}
		sections[requirement.Section] = struct{}{}
	}
	if len(sections) > MaxSections {
		return Compiled{}, fmt.Errorf("section limit exceeded")
	}
	canonical, err := json.Marshal(doc)
	if err != nil {
		return Compiled{}, err
	}
	sum := sha256.Sum256(canonical)
	return Compiled{Canonical: canonical, Digest: hex.EncodeToString(sum[:]), Document: doc}, nil
}

func validateRequirement(r CaptureRequirement, mode ComparisonMode) error {
	if strings.TrimSpace(r.Key) == "" || strings.TrimSpace(r.Section) == "" || strings.TrimSpace(r.Label) == "" {
		return fmt.Errorf("requirement key, section, and label are required")
	}
	if !ValidName(r.Label) || !ValidDescription(r.Instructions) {
		return fmt.Errorf("requirement text limit exceeded")
	}
	if r.MinimumCount < 0 || r.MaximumCount < 1 || r.MaximumCount > MaxActivePhotos || r.MinimumCount > r.MaximumCount {
		return fmt.Errorf("requirement count out of range")
	}
	if r.ComparisonTarget != mode {
		return fmt.Errorf("incompatible comparison mode")
	}
	if _, err := CompileExpression(r.Applicability); err != nil {
		return err
	}
	return nil
}

func validMode(mode ComparisonMode) bool {
	return mode == FixedOrigin || mode == PlannedStage || mode == PreviousInspection || mode == BeforeAfter || mode == ChecklistOnly
}

func ValidName(value string) bool {
	n := utf8.RuneCountInString(value)
	return n > 0 && n <= MaxNameCodePoints
}
func ValidDescription(value string) bool { return utf8.RuneCountInString(value) <= MaxTextCodePoints }

func CanonicalJSON(value any) ([]byte, string, error) {
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(sum[:]), nil
}

func ValidateCapacity(assets, inspections, activePhotos int, originalBytes int64) error {
	if assets > MaxAssetsPerTenant || inspections > MaxInspections || activePhotos > MaxActivePhotos || originalBytes > MaxOriginalBytes {
		return fmt.Errorf("confirmed platform capacity exceeded")
	}
	return nil
}

type StorageMeter struct{ Bytes int64 }

func (m *StorageMeter) Add(bytes int64) error {
	if bytes < 0 {
		return fmt.Errorf("negative bytes")
	}
	m.Bytes += bytes
	return nil
}
