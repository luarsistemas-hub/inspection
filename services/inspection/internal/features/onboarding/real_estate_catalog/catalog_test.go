package real_estate_catalog

import "testing"

func TestResolveSupportedDefinition(t *testing.T) {
	got, err := Resolve("real_estate")
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != 1 || len(got.Steps) != 4 || len(got.OriginModes) != 2 {
		t.Fatalf("unexpected definition: %+v", got)
	}
	if got.OriginModes[0].TemplateKey != ChecklistTemplateKey || got.OriginModes[1].TemplateKey != OriginTemplateKey {
		t.Fatal("origin modes are not pinned to curated templates")
	}
}

func TestResolveFailsClosedForUnsupportedDefinition(t *testing.T) {
	if _, err := Resolve("future-segment"); err == nil {
		t.Fatal("unsupported segment was accepted")
	}
	value, err := Resolve(Segment)
	if err != nil {
		t.Fatal(err)
	}
	value.SchemaVersion++
	if err := Validate(value); err == nil {
		t.Fatal("future schema version was accepted")
	}
}

func TestDefinitionMetadataIsExtensible(t *testing.T) {
	value, err := Resolve(Segment)
	if err != nil {
		t.Fatal(err)
	}
	value.Steps[1].Fields[0].Label = "Custom property label"
	if err := Validate(value); err != nil {
		t.Fatal(err)
	}
	if value.Steps[1].Fields[0].Label != "Custom property label" {
		t.Fatal("fixture metadata was not retained")
	}
}

func TestCuratedTemplatesPinOriginModeAndProfile(t *testing.T) {
	templates := TemplateDocuments()
	if templates[ChecklistTemplateKey].ComparisonMode != "CHECKLIST_ONLY" || templates[OriginTemplateKey].ComparisonMode != "FIXED_ORIGIN" {
		t.Fatal("curated templates have incorrect comparison modes")
	}
	if templates[ChecklistTemplateKey].AnalysisProfile != AnalysisProfileKey || templates[OriginTemplateKey].AnalysisProfile != AnalysisProfileKey {
		t.Fatal("curated templates do not pin the analysis profile")
	}
	refs := templateRefs{}
	if err := ValidateTemplates(refs); err != nil {
		t.Fatal(err)
	}
}

type templateRefs struct{}

func (templateRefs) SegmentExists(string) bool         { return true }
func (templateRefs) AnalysisProfileExists(string) bool { return true }
