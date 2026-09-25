package prompt

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultDefinitionIsCanonicalAndParseable(t *testing.T) {
	definition, digest, err := Canonicalize(DefaultDefinition())
	if err != nil {
		t.Fatal(err)
	}
	if len(digest) != 64 || !strings.Contains(string(definition), `"modelAlias":"inspection-vision"`) {
		t.Fatalf("unexpected canonical prompt: %s", definition)
	}
	parsed, err := Parse(definition)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.MinimumConfidenceBPS != MinimumConfidenceBPS || parsed.ModelAlias != ModelAlias {
		t.Fatalf("fixed configuration changed: %+v", parsed)
	}
	if MinimumConfidenceBPS != 8500 || MaxSystemPromptRunes != 20000 || len([]rune(parsed.SystemPrompt)) <= 12000 {
		t.Fatalf("new prompt contract limits not applied: confidence=%d limit=%d runes=%d", MinimumConfidenceBPS, MaxSystemPromptRunes, len([]rune(parsed.SystemPrompt)))
	}
}

func TestValidateRejectsChangesToFixedContract(t *testing.T) {
	definition := DefaultDefinition()
	definition.ModelAlias = "other-model"
	if err := Validate(definition); err == nil {
		t.Fatal("expected fixed model alias to be rejected")
	}
	definition = DefaultDefinition()
	definition.OutputSchema["additionalProperties"] = true
	if err := Validate(definition); err == nil {
		t.Fatal("expected fixed output schema to be rejected")
	}
	definition = DefaultDefinition()
	definition.SystemPrompt = "\x00"
	if err := Validate(definition); err == nil {
		t.Fatal("expected control character to be rejected")
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"systemPrompt":         "valid",
		"modelAlias":           ModelAlias,
		"outputSchema":         OutputSchema(),
		"minimumConfidenceBps": MinimumConfidenceBPS,
		"promptVersion":        "legacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(payload); err == nil {
		t.Fatal("expected removed promptVersion field to be rejected")
	}
}
