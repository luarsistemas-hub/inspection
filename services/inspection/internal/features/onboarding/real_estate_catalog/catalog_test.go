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
	if got.SchemaVersion != DefinitionSchema || got.Version != DefinitionVersion || len(got.Steps) != 4 || len(got.OriginModes) != 2 {
		t.Fatalf("unexpected definition: %+v", got)
	}
	if got.OriginModes[0].TemplateKey != got.OriginModes[1].TemplateKey || len(got.Templates) != 1 {
		t.Fatal("comparison choice must reuse the same inspection model")
	}
}

func TestResolveVersionPreservesTheLegacyAddressContract(t *testing.T) {
	got, err := ResolveVersion(Segment, DefinitionVersion-1)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != 1 || got.Version != 4 || got.Steps[1].Fields[0].Key != "address" || got.Steps[1].Fields[0].Type != "textarea" {
		t.Fatalf("legacy onboarding definition changed: %+v", got)
	}
}

func TestDefinitionChoicesKeepInternalValuesAndPortugueseLabels(t *testing.T) {
	got, err := Resolve(Segment)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		step, field, value, label string
	}{
		{"property", "propertyType", "APARTMENT", "Apartamento"},
		{"property", "purpose", "RENTAL", "Locação"},
		{"origin", "mode", "CHECKLIST_ONLY", "Registrar estado inicial"},
		{"participant", "mode", "SELF", "Eu farei a vistoria"},
	}
	for _, check := range checks {
		var found *Field
		for stepIndex := range got.Steps {
			if got.Steps[stepIndex].Key != check.step {
				continue
			}
			for fieldIndex := range got.Steps[stepIndex].Fields {
				if got.Steps[stepIndex].Fields[fieldIndex].Key == check.field {
					found = &got.Steps[stepIndex].Fields[fieldIndex]
				}
			}
		}
		if found == nil || len(found.Options) == 0 || found.OptionLabels[check.value] != check.label {
			t.Fatalf("missing choice %s/%s: %+v", check.step, check.field, found)
		}
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

func TestCuratedTemplateLeavesComparisonChoiceToInspection(t *testing.T) {
	templates := TemplateDocuments("development")
	if len(templates) != 1 || templates[ChecklistTemplateKey].DefaultComparisonMode != "CHECKLIST_ONLY" {
		t.Fatal("one reusable checklist model is required")
	}
	if templates[ChecklistTemplateKey].AnalysisType != "REAL_ESTATE" {
		t.Fatal("curated templates do not pin the analysis type")
	}
	refs := templateRefs{}
	if err := ValidateTemplates(refs); err != nil {
		t.Fatal(err)
	}
}

type templateRefs struct{}

func (templateRefs) SegmentExists(string) bool      { return true }
func (templateRefs) AnalysisTypeExists(string) bool { return true }
