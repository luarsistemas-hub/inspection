package prompt

import (
	"encoding/json"
	"fmt"
	"strings"

	inventoryvocabulary "inspection/services/inspection/internal/features/inventory/vocabulary"
)

// inventoryCatalogSchemaJSON is the strict output of one INVENTORY_CATALOG call:
// the items visible in one captured environment, with their evidence.
const inventoryCatalogSchemaJSON = `{"type":"object","additionalProperties":false,"required":["environments"],"properties":{"environments":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["typeCode","name","items"],"properties":{"typeCode":{"type":"string","enum":%[1]s},"name":{"type":"string","minLength":1,"maxLength":80},"items":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["typeCode","name","material","color","quantity","condition","observation","limitation","confidenceBps","evidence"],"properties":{"typeCode":{"type":"string","enum":%[2]s},"name":{"type":"string","minLength":1,"maxLength":80},"material":{"type":["string","null"],"maxLength":200},"color":{"type":["string","null"],"maxLength":100},"quantity":{"type":["integer","null"],"minimum":1,"maximum":999},"condition":{"type":"string","enum":%[3]s},"observation":{"type":"string","enum":%[4]s},"limitation":{"type":["string","null"],"maxLength":200},"confidenceBps":{"type":"integer","minimum":0,"maximum":10000},"evidence":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["mediaId","frameId"],"properties":{"mediaId":{"type":"string"},"frameId":{"type":["string","null"]}}}}}}}}}}}}`

// inventoryCompareSchemaJSON is the strict output of one INVENTORY_COMPARE call:
// one result per baseline item plus objects found outside the inventory.
const inventoryCompareSchemaJSON = `{"type":"object","additionalProperties":false,"required":["items"],"properties":{"items":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["itemIdentityId","status","conditionAfter","reasonCode","description","outsideTypeCode","outsideName"],"properties":{"itemIdentityId":{"type":["string","null"]},"status":{"type":"string","enum":["PRESENT_UNCHANGED","CONDITION_CHANGED","MISSING","NOT_OBSERVED","OUTSIDE_INVENTORY","IMPROVED"]},"conditionAfter":{"type":["string","null"],"enum":%[1]s},"reasonCode":{"type":["string","null"],"enum":["OBSTRUCTED","FRAMING","QUALITY","NO_CORRESPONDING_VIEW","ENVIRONMENT_NOT_CAPTURED","ANALYSIS_FAILED",null]},"description":{"type":["string","null"],"maxLength":500},"outsideTypeCode":{"type":["string","null"],"enum":%[2]s},"outsideName":{"type":["string","null"],"minLength":1,"maxLength":80}}}}}}`

func inventoryOutputSchema(task Task) map[string]any {
	var schemaJSON string
	switch task {
	case InventoryCatalog:
		schemaJSON = fmt.Sprintf(inventoryCatalogSchemaJSON, jsonList(environmentCodes(), false), jsonList(itemCodes(), false), jsonList(conditionCodes(), false), jsonList(observationCodes(), false))
	case InventoryCompare:
		schemaJSON = fmt.Sprintf(inventoryCompareSchemaJSON, jsonList(conditionCodes(), true), jsonList(itemCodes(), true))
	default:
		return nil
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
		panic(fmt.Sprintf("invalid %s output schema: %v", task, err))
	}
	return schema
}

// inventorySystemPrompt is the seeded, editable instruction of an inventory task.
func inventorySystemPrompt(task Task) string {
	var b strings.Builder
	switch task {
	case InventoryCatalog:
		b.WriteString("Você cataloga o inventário de um ambiente de imóvel a partir das evidências da vistoria (fotos e quadros de vídeo).\n\n")
		b.WriteString("Regras:\n")
		b.WriteString("- Liste somente itens visíveis nas evidências; nunca invente itens ou atributos.\n")
		b.WriteString("- Use apenas os códigos de tipo informados no schema; use OTHER com um nome descritivo quando nenhum tipo servir.\n")
		b.WriteString("- Material, cor e quantidade desconhecidos devem ser null.\n")
		b.WriteString("- Cite em evidence somente os mediaId e frameId recebidos que mostram o item.\n")
		b.WriteString("- Use observation PARTIAL ou NOT_ASSESSABLE e descreva a limitação quando a evidência não permitir avaliar o item.\n")
		b.WriteString("- Ignore objetos pessoais e transitórios (roupas, alimentos, embalagens).\n")
	case InventoryCompare:
		b.WriteString("Você compara os itens do inventário de referência de um ambiente com as evidências atuais da vistoria.\n\n")
		b.WriteString("Regras:\n")
		b.WriteString("- Retorne um resultado para cada item do inventário, usando o itemIdentityId recebido.\n")
		b.WriteString("- MISSING exige uma vista atual confirmada do local do item em que ele claramente não aparece.\n")
		b.WriteString("- Sem vista correspondente confiável, use NOT_OBSERVED com o reasonCode adequado; nunca conclua que o item está inalterado sem evidência.\n")
		b.WriteString("- CONDITION_CHANGED e IMPROVED exigem conditionAfter na escala abaixo.\n")
		b.WriteString("- Objetos relevantes fora do inventário usam OUTSIDE_INVENTORY, itemIdentityId null, outsideTypeCode e outsideName.\n")
	}
	b.WriteString("\nEscala de condição:\n")
	for _, level := range inventoryvocabulary.Conditions() {
		fmt.Fprintf(&b, "- %s (%s): %s.\n", level.Code, level.Label, level.Description)
	}
	b.WriteString("\nResponda em português do Brasil, somente no formato JSON do schema.")
	return b.String()
}

// ValidateInventoryCompareOutput enforces the nullable identity fields' status
// relationship after decoding a structured model response.
func ValidateInventoryCompareOutput(payload []byte) error {
	var result struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return fmt.Errorf("invalid inventory comparison output: %w", err)
	}
	for i, item := range result.Items {
		for _, key := range []string{"itemIdentityId", "status", "outsideTypeCode", "outsideName"} {
			if _, ok := item[key]; !ok {
				return fmt.Errorf("inventory comparison item %d is missing %s", i, key)
			}
		}
		var status string
		var identityID, outsideType, outsideName *string
		if err := json.Unmarshal(item["status"], &status); err != nil {
			return fmt.Errorf("inventory comparison item %d has invalid status", i)
		}
		if err := json.Unmarshal(item["itemIdentityId"], &identityID); err != nil {
			return fmt.Errorf("inventory comparison item %d has invalid identity", i)
		}
		if err := json.Unmarshal(item["outsideTypeCode"], &outsideType); err != nil {
			return fmt.Errorf("inventory comparison item %d has invalid outside type", i)
		}
		if err := json.Unmarshal(item["outsideName"], &outsideName); err != nil {
			return fmt.Errorf("inventory comparison item %d has invalid outside name", i)
		}
		if status == "OUTSIDE_INVENTORY" {
			if identityID != nil || outsideType == nil || !inventoryvocabulary.IsItemType(*outsideType) || outsideName == nil || strings.TrimSpace(*outsideName) == "" {
				return fmt.Errorf("inventory comparison item %d has invalid outside identity", i)
			}
		} else if identityID == nil || strings.TrimSpace(*identityID) == "" || outsideType != nil || outsideName != nil {
			return fmt.Errorf("inventory comparison item %d has invalid baseline identity", i)
		}
	}
	return nil
}

func environmentCodes() []string {
	var codes []string
	for _, term := range inventoryvocabulary.EnvironmentTypes() {
		codes = append(codes, string(term.Code))
	}
	return codes
}

func itemCodes() []string {
	var codes []string
	for _, term := range inventoryvocabulary.ItemTypes() {
		codes = append(codes, string(term.Code))
	}
	return codes
}

func conditionCodes() []string {
	var codes []string
	for _, level := range inventoryvocabulary.Conditions() {
		codes = append(codes, string(level.Code))
	}
	return codes
}

func observationCodes() []string {
	var codes []string
	for _, observation := range inventoryvocabulary.Observations() {
		codes = append(codes, string(observation))
	}
	return codes
}

// jsonList encodes codes as a JSON array, optionally allowing null.
func jsonList(codes []string, nullable bool) string {
	values := make([]any, 0, len(codes)+1)
	for _, code := range codes {
		values = append(values, code)
	}
	if nullable {
		values = append(values, nil)
	}
	payload, _ := json.Marshal(values)
	return string(payload)
}
