package real_estate_catalog

import (
	"testing"

	templatecatalog "inspection/services/inspection/internal/features/templates/catalog"
)

func TestResolveSupportedDefinition(t *testing.T) {
	got, err := Resolve("real_estate")
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != DefinitionSchema || got.Version != DefinitionVersion || len(got.Steps) != 3 {
		t.Fatalf("unexpected definition: %+v", got)
	}
	if len(got.Templates) != 1 {
		t.Fatalf("unexpected templates: %+v", got.Templates)
	}
}

func TestResolveVersionUsesCurrentContract(t *testing.T) {
	got, err := ResolveVersion(Segment, DefinitionVersion-1)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != DefinitionSchema || got.Version != DefinitionVersion || got.Steps[1].Fields[0].Key != "addressDetails" {
		t.Fatalf("current onboarding definition changed: %+v", got)
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

func TestOnboardingPublishesV2ButKeepsLegacyConsumerTemplateV1(t *testing.T) {
	refs := templateRefs{}
	for label, documents := range map[string]map[string]templatecatalog.TemplateDocument{
		"published v2":     TemplateDocuments("production"),
		"legacy active v1": LegacyTemplateDocuments("production"),
	} {
		document, ok := documents[ChecklistTemplateKey]
		if !ok {
			t.Fatalf("%s document missing", label)
		}
		payload, _, err := templatecatalog.CanonicalJSON(document)
		if err != nil {
			t.Fatal(err)
		}
		compiled, err := templatecatalog.Compile(payload, refs)
		if err != nil {
			t.Fatalf("%s document did not compile: %v", label, err)
		}
		if label == "published v2" && compiled.Document.SchemaVersion != templatecatalog.EnvironmentSchemaVersion {
			t.Fatalf("published onboarding template schemaVersion=%d", compiled.Document.SchemaVersion)
		}
		if label == "legacy active v1" && (compiled.Document.SchemaVersion != TemplateSchema || len(compiled.Document.Requirements) == 0) {
			t.Fatalf("legacy onboarding template is not capturable v1: %+v", compiled.Document)
		}
	}
}

type templateRefs struct{}

func (templateRefs) SegmentExists(string) bool      { return true }
func (templateRefs) AnalysisTypeExists(string) bool { return true }
