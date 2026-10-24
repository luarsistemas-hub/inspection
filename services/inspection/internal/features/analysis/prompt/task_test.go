package prompt

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestPromptTasks_UT170ToUT173(t *testing.T) {
	t.Run("structured output schemas stay in the gateway strict subset", func(t *testing.T) {
		for _, task := range []string{string(Findings), string(InventoryCatalog), string(InventoryCompare)} {
			if err := assertStrictSchema(OutputSchemaForTask(task), "$"); err != nil {
				t.Errorf("%s schema: %v", task, err)
			}
		}
	})
	t.Run("UT170 inventory catalog has a task-owned schema and digest", func(t *testing.T) {
		definition := DefaultDefinitionForTask(string(InventoryCatalog))
		canonical, digest, err := CanonicalizeTask(string(InventoryCatalog), definition)
		if err != nil {
			t.Fatal(err)
		}
		if len(digest) != 64 || len(canonical) == 0 || definition.OutputSchema["required"] == nil {
			t.Fatalf("catalog prompt not resolved: digest=%q", digest)
		}
	})
	t.Run("UT171 changed schema is rejected", func(t *testing.T) {
		definition := DefaultDefinitionForTask(string(InventoryCatalog))
		definition.OutputSchema = OutputSchema()
		if err := ValidateTask(string(InventoryCatalog), definition); err == nil {
			t.Fatal("non-code-owned schema accepted")
		}
	})
	t.Run("INVENTORY_COMPARE represents outside inventory items", func(t *testing.T) {
		payload, err := json.Marshal(OutputSchemaForTask(string(InventoryCompare)))
		if err != nil {
			t.Fatal(err)
		}
		for _, fragment := range []string{`"outsideTypeCode"`, `"outsideName"`, `"itemIdentityId":{"type":["string","null"]}`, `"OUTSIDE_INVENTORY"`} {
			if !strings.Contains(string(payload), fragment) {
				t.Fatalf("compare schema missing %s: %s", fragment, payload)
			}
		}
	})
	t.Run("INVENTORY_COMPARE enforces outside versus baseline identity", func(t *testing.T) {
		valid := `{"items":[{"itemIdentityId":null,"status":"OUTSIDE_INVENTORY","outsideTypeCode":"CHAIR","outsideName":"Cadeira"}]}`
		if err := ValidateInventoryCompareOutput([]byte(valid)); err != nil {
			t.Fatalf("valid outside item rejected: %v", err)
		}
		invalid := `{"items":[{"itemIdentityId":null,"status":"MISSING","outsideTypeCode":null,"outsideName":null}]}`
		if err := ValidateInventoryCompareOutput([]byte(invalid)); err == nil {
			t.Fatal("baseline item without identity accepted")
		}
	})
	t.Run("inventory schemas restrict codes to the vocabulary", func(t *testing.T) {
		catalog := OutputSchemaForTask(string(InventoryCatalog))
		environment := catalog["properties"].(map[string]any)["environments"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
		item := environment["items"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
		if got := len(environment["typeCode"].(map[string]any)["enum"].([]any)); got != 19 {
			t.Fatalf("environment typeCode enum=%d, want 19", got)
		}
		if got := len(item["typeCode"].(map[string]any)["enum"].([]any)); got != 30 {
			t.Fatalf("item typeCode enum=%d, want 30", got)
		}
		unknownOutside := `{"items":[{"itemIdentityId":null,"status":"OUTSIDE_INVENTORY","outsideTypeCode":"GARDEN_X","outsideName":"Jardim"}]}`
		if err := ValidateInventoryCompareOutput([]byte(unknownOutside)); err == nil {
			t.Fatal("outside item with unknown type accepted")
		}
	})
	t.Run("inventory seed prompts carry the condition scale", func(t *testing.T) {
		for _, task := range []string{string(InventoryCatalog), string(InventoryCompare)} {
			prompt := DefaultDefinitionForTask(task).SystemPrompt
			for _, fragment := range []string{"NEW (Novo)", "NOT_ASSESSABLE (Não avaliável)", "português do Brasil"} {
				if !strings.Contains(prompt, fragment) {
					t.Errorf("%s seed prompt misses %q", task, fragment)
				}
			}
		}
	})
	t.Run("UT173 unknown task rejected", func(t *testing.T) {
		if IsKnownTask("SUMMARY") {
			t.Fatal("SUMMARY unexpectedly accepted")
		}
		if _, _, err := CanonicalizeTask("SUMMARY", DefaultDefinition()); err == nil {
			t.Fatal("unknown task canonicalized")
		}
	})
}

func assertStrictSchema(value any, path string) error {
	forbidden := map[string]bool{"allOf": true, "oneOf": true, "if": true, "then": true, "else": true, "not": true}
	switch node := value.(type) {
	case map[string]any:
		for key := range node {
			if forbidden[key] {
				return fmt.Errorf("unsupported keyword %s at %s", key, path)
			}
		}
		if properties, ok := node["properties"].(map[string]any); ok {
			required := map[string]bool{}
			if values, ok := node["required"].([]any); ok {
				for _, value := range values {
					if name, ok := value.(string); ok {
						required[name] = true
					}
				}
			}
			if len(required) != len(properties) {
				return fmt.Errorf("required has %d properties, properties has %d at %s", len(required), len(properties), path)
			}
			for name := range properties {
				if !required[name] {
					return fmt.Errorf("property %s is not required at %s", name, path)
				}
			}
		}
		for key, child := range node {
			if err := assertStrictSchema(child, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		for i, child := range node {
			if err := assertStrictSchema(child, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}
