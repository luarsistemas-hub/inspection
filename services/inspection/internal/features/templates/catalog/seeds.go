package catalog

import "fmt"

type Seed struct {
	SegmentKey  string
	TemplateKey string
	Document    TemplateDocument
}

func CuratedSeeds(analysisType, propertySegment, constructionSegment, cleaningSegment string) []Seed {
	base := func(segment string, roles, environments []string) TemplateDocument {
		limits := DefaultEnvironmentCapture()
		return TemplateDocument{SchemaVersion: EnvironmentSchemaVersion, SegmentVersionID: segment, ParticipantRoles: roles, DefaultComparisonMode: ChecklistOnly,
			CaptureLayout: EnvironmentsLayout, EnvironmentCapture: limits, SuggestedEnvironmentTypes: environments,
			AnalysisType: analysisType, Policy: Policy{GPSRequired: true, GeofenceMeters: DefaultGeofence, AllowGallery: true}}
	}
	return []Seed{
		{SegmentKey: "property", TemplateKey: "property-periodic", Document: base(propertySegment, []string{"TENANT_PARTICIPANT", "PROPERTY_OWNER"}, []string{"LIVING_ROOM", "KITCHEN", "BEDROOM", "BATHROOM", "LAUNDRY", "BALCONY", "OTHER"})},
		{SegmentKey: "construction", TemplateKey: "construction-progress", Document: base(constructionSegment, []string{"CONSTRUCTION_RESPONSIBLE", "CONTRACTOR"}, []string{"LIVING_ROOM", "KITCHEN", "BEDROOM", "BATHROOM", "EXTERNAL_AREA", "OTHER"})},
		{SegmentKey: "cleaning", TemplateKey: "cleaning-quality", Document: base(cleaningSegment, []string{"CLEANING_EXECUTOR", "CLEANING_SUPERVISOR"}, []string{"LIVING_ROOM", "KITCHEN", "BEDROOM", "BATHROOM", "TOILET", "SERVICE_AREA", "OTHER"})},
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
