// Package real_estate_catalog owns the curated, versioned onboarding contract
// for the real-estate public journey.
package real_estate_catalog

import (
	"errors"
	"fmt"
	"strings"

	templatecatalog "inspection/services/inspection/internal/features/templates/catalog"
)

const (
	Segment              = "REAL_ESTATE"
	DefinitionSchema     = 1
	DefinitionVersion    = 1
	ChecklistTemplateKey = "real-estate-checklist"
	OriginTemplateKey    = "real-estate-fixed-origin"
	AnalysisProfileKey   = "real-estate-default"
)

var ErrUnsupportedSchemaVersion = errors.New("unsupported schema version")

type Field struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Placeholder string   `json:"placeholder,omitempty"`
	Options     []string `json:"options,omitempty"`
}

type Step struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Position int     `json:"position"`
	Required bool    `json:"required"`
	Fields   []Field `json:"fields"`
}

type OriginMode struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	TemplateKey string `json:"templateKey"`
	Required    bool   `json:"required"`
}

type Definition struct {
	SchemaVersion   int          `json:"schemaVersion"`
	Version         int          `json:"version"`
	Segment         string       `json:"segment"`
	SegmentVersion  string       `json:"segmentVersion"`
	Steps           []Step       `json:"steps"`
	Purposes        []string     `json:"purposes"`
	OriginModes     []OriginMode `json:"originModes"`
	Templates       []string     `json:"templates"`
	AnalysisProfile string       `json:"analysisProfile"`
}

var definition = Definition{
	SchemaVersion: DefinitionSchema, Version: DefinitionVersion, Segment: Segment,
	SegmentVersion: "real-estate-v1", Purposes: []string{"SALE", "RENTAL", "MAINTENANCE", "INSURANCE"},
	Templates: []string{ChecklistTemplateKey, OriginTemplateKey}, AnalysisProfile: AnalysisProfileKey,
	Steps: []Step{
		{Key: "agency", Label: "Agency", Position: 1, Required: true, Fields: []Field{{Key: "name", Label: "Agency name", Type: "text", Required: true}}},
		{Key: "property", Label: "Property", Position: 2, Required: true, Fields: []Field{{Key: "address", Label: "Property address", Type: "textarea", Required: true}, {Key: "propertyType", Label: "Property type", Type: "select", Required: true, Options: []string{"APARTMENT", "HOUSE", "COMMERCIAL", "LAND"}}, {Key: "rooms", Label: "Rooms", Type: "number", Required: true}, {Key: "purpose", Label: "Purpose", Type: "select", Required: true, Options: []string{"SALE", "RENTAL", "MAINTENANCE", "INSURANCE"}}, {Key: "deadline", Label: "Inspection deadline", Type: "date", Required: true}}},
		{Key: "origin", Label: "Reference photos", Position: 3, Required: true, Fields: []Field{{Key: "mode", Label: "Reference mode", Type: "select", Required: true, Options: []string{"CHECKLIST_ONLY", "FIXED_ORIGIN"}}}},
		{Key: "participant", Label: "Participant", Position: 4, Required: true, Fields: []Field{{Key: "mode", Label: "Who will inspect", Type: "select", Required: true, Options: []string{"SELF", "DELEGATE"}}, {Key: "name", Label: "Participant name", Type: "text", Required: false}, {Key: "email", Label: "Participant email", Type: "email", Required: false}}},
	},
	OriginModes: []OriginMode{{Key: "CHECKLIST_ONLY", Label: "First inspection", TemplateKey: ChecklistTemplateKey, Required: false}, {Key: "FIXED_ORIGIN", Label: "Compare with previous photos", TemplateKey: OriginTemplateKey, Required: true}},
}

// Resolve returns a copy of a supported definition. Unsupported schema
// versions fail closed so callers never receive mutable metadata they cannot
// safely interpret.
func Resolve(segment string) (Definition, error) {
	if !strings.EqualFold(strings.TrimSpace(segment), Segment) {
		return Definition{}, fmt.Errorf("definition not found")
	}
	if definition.SchemaVersion != DefinitionSchema {
		return Definition{}, ErrUnsupportedSchemaVersion
	}
	return clone(definition), nil
}

// Validate rejects malformed definitions, including future schema versions.
func Validate(value Definition) error {
	if value.SchemaVersion != DefinitionSchema {
		return ErrUnsupportedSchemaVersion
	}
	if value.Version <= 0 || value.Segment == "" || len(value.Steps) == 0 || len(value.OriginModes) != 2 {
		return fmt.Errorf("invalid onboarding definition")
	}
	for i, step := range value.Steps {
		if step.Key == "" || step.Position != i+1 || len(step.Fields) == 0 {
			return fmt.Errorf("invalid onboarding step")
		}
		for _, field := range step.Fields {
			if field.Key == "" || field.Label == "" || field.Type == "" {
				return fmt.Errorf("invalid onboarding field")
			}
		}
	}
	return nil
}

// TemplateDocuments returns the two immutable template contracts selected by
// the real-estate origin modes. Both use the same pinned analysis profile.
func TemplateDocuments() map[string]templatecatalog.TemplateDocument {
	base := func(mode templatecatalog.ComparisonMode) templatecatalog.TemplateDocument {
		return templatecatalog.TemplateDocument{
			SchemaVersion: DefinitionSchema, SegmentVersionID: "real-estate-v1",
			ParticipantRoles: []string{"TENANT_PARTICIPANT", "PROPERTY_OWNER"}, ComparisonMode: mode,
			Requirements: []templatecatalog.CaptureRequirement{{Key: "overview", Section: "property", Label: "Property overview", EvidenceKind: "PHOTO", MinimumCount: 1, MaximumCount: 10, Required: true, DescriptionRequired: true, CaptureSourcePolicy: "CAMERA_DEFAULT", ComparisonTarget: mode}},
			ReportMode:   "HISTORICAL", AnalysisProfile: AnalysisProfileKey,
			Policy: templatecatalog.Policy{GPSRequired: true, GeofenceMeters: templatecatalog.DefaultGeofence, AllowGallery: true},
		}
	}
	return map[string]templatecatalog.TemplateDocument{ChecklistTemplateKey: base(templatecatalog.ChecklistOnly), OriginTemplateKey: base(templatecatalog.FixedOrigin)}
}

// ValidateTemplates compiles both curated documents against the supplied
// segment/profile references before they are published.
func ValidateTemplates(refs templatecatalog.References) error {
	for key, document := range TemplateDocuments() {
		payload, _, err := templatecatalog.CanonicalJSON(document)
		if err != nil {
			return fmt.Errorf("template %s: canonicalize: %w", key, err)
		}
		if _, err := templatecatalog.Compile(payload, refs); err != nil {
			return fmt.Errorf("template %s: %w", key, err)
		}
	}
	return nil
}

func clone(value Definition) Definition {
	result := value
	result.Steps = append([]Step(nil), value.Steps...)
	for i := range result.Steps {
		result.Steps[i].Fields = append([]Field(nil), value.Steps[i].Fields...)
		for j := range result.Steps[i].Fields {
			result.Steps[i].Fields[j].Options = append([]string(nil), value.Steps[i].Fields[j].Options...)
		}
	}
	result.Purposes = append([]string(nil), value.Purposes...)
	result.Templates = append([]string(nil), value.Templates...)
	result.OriginModes = append([]OriginMode(nil), value.OriginModes...)
	return result
}
