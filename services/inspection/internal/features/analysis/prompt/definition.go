// Package prompt contains the immutable contract shared by global analysis
// prompt slices. Only SystemPrompt is editable at runtime.
package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	RealEstate           = "REAL_ESTATE"
	ModelAlias           = "inspection-vision"
	MinimumConfidenceBPS = 8500
	MaxSystemPromptRunes = 20000
)

// Definition is stored verbatim (in canonical JSON) in prompt rows and snapshots.
type Definition struct {
	SystemPrompt         string         `json:"systemPrompt"`
	ModelAlias           string         `json:"modelAlias"`
	OutputSchema         map[string]any `json:"outputSchema"`
	MinimumConfidenceBPS int            `json:"minimumConfidenceBps"`
}

const outputSchemaJSON = `{"type":"object","additionalProperties":false,"required":["noRelevantChange","findings"],"properties":{"noRelevantChange":{"type":["boolean","null"]},"findings":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["category","title","description","severity","confidence","evidenceIds","quality","recommendedAction"],"properties":{"category":{"type":"string","enum":["CONSERVATION","INVENTORY","CLEANLINESS","OBSTRUCTION","EVIDENCE_QUALITY"]},"title":{"type":"string"},"description":{"type":"string"},"severity":{"type":"string","enum":["NONE","LOW","MEDIUM","HIGH","CRITICAL"]},"confidence":{"type":"number","minimum":0,"maximum":1},"evidenceIds":{"type":"array","items":{"type":"string"},"minItems":1},"quality":{"type":"string","enum":["ADEQUATE","LIMITED","INSUFFICIENT"]},"recommendedAction":{"type":"string"}}}}}}`

// OutputSchema returns the sole schema understood by the structured-result parser.
func OutputSchema() map[string]any {
	var schema map[string]any
	_ = json.Unmarshal([]byte(outputSchemaJSON), &schema)
	return schema
}

// DefaultDefinition returns the embedded seed only. Runtime data always comes from PostgreSQL.
func DefaultDefinition() Definition {
	return Definition{SystemPrompt: defaultSystemPrompt, ModelAlias: ModelAlias, OutputSchema: OutputSchema(), MinimumConfidenceBPS: MinimumConfidenceBPS}
}

func IsKnownType(value string) bool { return strings.TrimSpace(value) == RealEstate }

func Canonicalize(definition Definition) ([]byte, string, error) {
	if err := Validate(definition); err != nil {
		return nil, "", err
	}
	payload, err := json.Marshal(definition)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(payload)
	return payload, hex.EncodeToString(sum[:]), nil
}

func Parse(payload []byte) (Definition, error) {
	var definition Definition
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&definition); err != nil {
		return Definition{}, fmt.Errorf("invalid analysis prompt: %w", err)
	}
	if err := Validate(definition); err != nil {
		return Definition{}, err
	}
	return definition, nil
}

func Validate(definition Definition) error {
	if !safePrompt(definition.SystemPrompt) {
		return fmt.Errorf("invalid system prompt")
	}
	if definition.ModelAlias != ModelAlias || definition.MinimumConfidenceBPS != MinimumConfidenceBPS {
		return fmt.Errorf("invalid fixed analysis configuration")
	}
	actual, err := json.Marshal(definition.OutputSchema)
	if err != nil {
		return fmt.Errorf("invalid fixed output schema")
	}
	var expectedSchema map[string]any
	if err := json.Unmarshal([]byte(outputSchemaJSON), &expectedSchema); err != nil || !reflect.DeepEqual(definition.OutputSchema, expectedSchema) || len(actual) == 0 {
		return fmt.Errorf("invalid fixed output schema")
	}
	return nil
}

func safePrompt(value string) bool {
	if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > MaxSystemPromptRunes {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}
