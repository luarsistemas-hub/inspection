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

// Task is the code-owned purpose of an analysis prompt.
type Task string

const (
	Findings         Task = "FINDINGS"
	FindingsCurrent  Task = "FINDINGS_CURRENT"
	FindingsCompare  Task = "FINDINGS_COMPARE"
	InventoryCatalog Task = "INVENTORY_CATALOG"
	InventoryCompare Task = "INVENTORY_COMPARE"
)

// IsKnownTask reports whether task is one of the supported prompt tasks.
func IsKnownTask(task string) bool {
	return task == string(Findings) || task == string(FindingsCurrent) || task == string(FindingsCompare) || task == string(InventoryCatalog) || task == string(InventoryCompare)
}

// Definition is stored verbatim (in canonical JSON) in prompt rows and snapshots.
type Definition struct {
	SystemPrompt         string         `json:"systemPrompt"`
	ModelAlias           string         `json:"modelAlias"`
	OutputSchema         map[string]any `json:"outputSchema"`
	MinimumConfidenceBPS int            `json:"minimumConfidenceBps"`
	SchemaVersion        int            `json:"schemaVersion,omitempty"`
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

// DefaultDefinitionForTask returns the embedded prompt and immutable schema for a task.
func DefaultDefinitionForTask(task string) Definition {
	definition := DefaultDefinition()
	switch Task(task) {
	case FindingsCurrent, FindingsCompare:
		definition.SystemPrompt = findingsSystemPrompt(Task(task))
	case InventoryCatalog, InventoryCompare:
		definition.SystemPrompt = inventorySystemPrompt(Task(task))
		definition.OutputSchema = inventoryOutputSchema(Task(task))
	}
	return definition
}

// DefinitionForTaskVersion returns a code-owned prompt contract for an
// explicitly selected schema version. The default remains the historical
// reader until a version has passed its corpus release gate.
func DefinitionForTaskVersion(task string, version int) Definition {
	definition := DefaultDefinitionForTask(task)
	if Task(task) == InventoryCatalog && version == CurrentCatalogSchemaVersion {
		definition.SchemaVersion = version
		definition.OutputSchema = inventoryCatalogSchema(version)
		definition.SystemPrompt += "\n" + inventoryCatalogV2Instruction()
	}
	return definition
}

// OutputSchemaForTask returns a fresh copy of the fixed schema for task.
func OutputSchemaForTask(task string) map[string]any {
	switch Task(task) {
	case FindingsCurrent, FindingsCompare:
		return OutputSchema()
	case InventoryCatalog, InventoryCompare:
		if Task(task) == InventoryCatalog {
			return inventoryCatalogSchema(1)
		}
		return inventoryOutputSchema(Task(task))
	default:
		return OutputSchema()
	}
}

// OutputSchemaForVersion returns the immutable reader for a pinned task
// version. It is also used by compatibility fixtures for historical readers.
func OutputSchemaForVersion(task string, version int) map[string]any {
	return outputSchemaForVersion(task, version)
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
	return ValidateTask(string(Findings), definition)
}

// ValidateTask checks mutable prompt content against the immutable task schema.
func ValidateTask(task string, definition Definition) error {
	if !IsKnownTask(task) {
		return fmt.Errorf("unknown analysis task")
	}
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
	version := definition.SchemaVersion
	if version == 0 {
		version = 1 // snapshots created before schema versions remain readable
	}
	expectedSchema := outputSchemaForVersion(task, version)
	if len(expectedSchema) == 0 || !reflect.DeepEqual(definition.OutputSchema, expectedSchema) || len(actual) == 0 {
		return fmt.Errorf("invalid fixed output schema")
	}
	return nil
}

// ParsePinnedTask validates a prompt against the schema reader pinned by the
// immutable snapshot metadata, while checking the stored content digest.
func ParsePinnedTask(task string, payload []byte, digest string, schemaVersion int) (Definition, error) {
	definition, err := ParseTask(task, payload)
	if err != nil {
		return Definition{}, err
	}
	version := definition.SchemaVersion
	if version == 0 {
		version = 1
	}
	if schemaVersion > 0 && schemaVersion != version {
		return Definition{}, fmt.Errorf("pinned analysis schema version mismatch")
	}
	canonical, actualDigest, err := CanonicalizeTask(task, definition)
	if err != nil || len(canonical) == 0 || (digest != "" && actualDigest != digest) {
		return Definition{}, fmt.Errorf("pinned analysis schema digest mismatch")
	}
	return definition, nil
}

func outputSchemaForVersion(task string, version int) map[string]any {
	if Task(task) == InventoryCatalog {
		return inventoryCatalogSchema(version)
	}
	if version != 1 {
		return nil
	}
	return OutputSchemaForTask(task)
}

// CurrentCatalogSchemaVersion is the latest code-owned catalog contract.
const CurrentCatalogSchemaVersion = 2

// SchemaPin copies the immutable schema identity from a resolved prompt snapshot
// onto a newly created inventory generation.
func SchemaPin(version int, digest string) (int, *string) {
	return version, &digest
}

func findingsSystemPrompt(task Task) string {
	if task == FindingsCompare {
		return "Analise somente mudanças entre evidências de referência e atuais. Separe condição atual de mudança histórica e não atribua responsabilidade, desgaste normal ou custo. " + defaultSystemPrompt
	}
	return "Descreva a condição atual observável. Não conclua mudança histórica, ausência ou normalidade sem evidência suficiente. " + defaultSystemPrompt
}

// ParseTask decodes and validates one task's stored prompt.
func ParseTask(task string, payload []byte) (Definition, error) {
	var definition Definition
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&definition); err != nil {
		return Definition{}, fmt.Errorf("invalid analysis prompt: %w", err)
	}
	if err := ValidateTask(task, definition); err != nil {
		return Definition{}, err
	}
	return definition, nil
}

// CanonicalizeTask validates and serializes a task-specific prompt definition.
func CanonicalizeTask(task string, definition Definition) ([]byte, string, error) {
	if err := ValidateTask(task, definition); err != nil {
		return nil, "", err
	}
	payload, err := json.Marshal(definition)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(payload)
	return payload, hex.EncodeToString(sum[:]), nil
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
