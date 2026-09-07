package catalog

import "fmt"

type Seed struct {
	SegmentKey  string
	TemplateKey string
	Document    TemplateDocument
}

func CuratedSeeds(analysisProfile, propertySegment, constructionSegment, cleaningSegment string) []Seed {
	base := func(segment string, mode ComparisonMode, multi bool, report string, roles []string) TemplateDocument {
		return TemplateDocument{SchemaVersion: SchemaVersion, SegmentVersionID: segment, ParticipantRoles: roles, ComparisonMode: mode,
			Requirements: []CaptureRequirement{{Key: "overview", Section: "general", Label: "Overview", EvidenceKind: "PHOTO", MinimumCount: 1, MaximumCount: 10, Required: true, DescriptionRequired: true, CaptureSourcePolicy: "CAMERA_DEFAULT", ComparisonTarget: mode}},
			MultiStage:   multi, ReportMode: report, AnalysisProfile: analysisProfile, Policy: Policy{GPSRequired: true, GeofenceMeters: DefaultGeofence, AllowGallery: true}}
	}
	construction := base(constructionSegment, PlannedStage, true, "CONSOLIDATED", []string{"CONSTRUCTION_RESPONSIBLE", "CONTRACTOR"})
	construction.Stages = []Stage{{Key: "planned", Label: "Planned stage", Position: 1}}
	cleaning := base(cleaningSegment, BeforeAfter, true, "HISTORICAL", []string{"CLEANING_EXECUTOR", "CLEANING_SUPERVISOR"})
	cleaning.Stages = []Stage{{Key: "origin", Label: "Origin", Position: 1}, {Key: "after", Label: "After", Position: 2}}
	return []Seed{
		{SegmentKey: "property", TemplateKey: "property-periodic", Document: base(propertySegment, FixedOrigin, false, "HISTORICAL", []string{"TENANT_PARTICIPANT", "PROPERTY_OWNER"})},
		{SegmentKey: "construction", TemplateKey: "construction-progress", Document: construction},
		{SegmentKey: "cleaning", TemplateKey: "cleaning-quality", Document: cleaning},
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
