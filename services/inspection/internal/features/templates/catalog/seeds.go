package catalog

import "fmt"

type Seed struct {
	SegmentKey  string
	TemplateKey string
	Document    TemplateDocument
}

func CuratedSeeds(analysisType, propertySegment, constructionSegment, cleaningSegment string) []Seed {
	base := func(segment string, roles []string) TemplateDocument {
		return TemplateDocument{SchemaVersion: SchemaVersion, SegmentVersionID: segment, ParticipantRoles: roles, DefaultComparisonMode: ChecklistOnly,
			Requirements: []CaptureRequirement{{Key: "overview", Section: "property", Label: "Visão geral do imóvel", Instructions: "Fotografe o imóvel de forma ampla, com boa iluminação e sem ocultar áreas relevantes.", EvidenceKind: "PHOTO", MinimumCount: 1, MaximumCount: 10, Required: true, DescriptionRequired: false, CaptureSourcePolicy: "CAMERA_DEFAULT"}},
			AnalysisType: analysisType, Policy: Policy{GPSRequired: true, GeofenceMeters: DefaultGeofence, AllowGallery: true}}
	}
	return []Seed{
		{SegmentKey: "property", TemplateKey: "property-periodic", Document: base(propertySegment, []string{"TENANT_PARTICIPANT", "PROPERTY_OWNER"})},
		{SegmentKey: "construction", TemplateKey: "construction-progress", Document: base(constructionSegment, []string{"CONSTRUCTION_RESPONSIBLE", "CONTRACTOR"})},
		{SegmentKey: "cleaning", TemplateKey: "cleaning-quality", Document: base(cleaningSegment, []string{"CLEANING_EXECUTOR", "CLEANING_SUPERVISOR"})},
	}
}

func ValidateSeeds(seeds []Seed, refs References) error {
	if len(seeds) != 3 {
		return fmt.Errorf("three curated seeds required")
	}
	for _, seed := range seeds {
		payload, _, _ := CanonicalJSON(seed.Document)
		if _, err := Compile(payload, refs); err != nil {
			return fmt.Errorf("seed %s: %w", seed.TemplateKey, err)
		}
	}
	return nil
}
