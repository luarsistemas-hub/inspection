package core

import (
	"encoding/json"
	"strconv"
	"strings"
)

// HandoverPresentation contains only report-safe declaration facts.
type HandoverPresentation struct {
	Label, Type, Value, Unit, Date, Declarant, Note string
}

// PresentHandover turns a typed supplemental record into readable labels.
func PresentHandover(entry HandoverEntry) HandoverPresentation {
	out := HandoverPresentation{Label: HandoverLabel(entry.Kind), Declarant: entry.Declarant}
	if entry.Status == "PENDING" || len(entry.Payload) == 0 {
		return out
	}
	var payload map[string]any
	if json.Unmarshal(entry.Payload, &payload) != nil {
		return out
	}
	switch entry.Kind {
	case "NOTE":
		out.Note = handoverString(payload["note"])
	case "KEY":
		out.Type, out.Value = handoverString(payload["keyType"]), handoverString(payload["quantity"])
	case "CONTROL":
		out.Type, out.Value = handoverString(payload["controlType"]), handoverString(payload["quantity"])
	case "METER":
		out.Type, out.Value = handoverTypeLabel(handoverString(payload["meterType"])), handoverString(payload["reading"])
		out.Unit, out.Date = handoverString(payload["unit"]), handoverString(payload["date"])
	}
	return out
}

func handoverTypeLabel(value string) string {
	switch value {
	case "ENERGY":
		return "Energia"
	case "WATER":
		return "Água"
	case "GAS":
		return "Gás"
	default:
		return value
	}
}

// HandoverLabel returns a Portuguese label without exposing an internal key.
func HandoverLabel(kind string) string {
	switch kind {
	case "KEY":
		return "Chaves"
	case "CONTROL":
		return "Controles"
	case "METER":
		return "Medidor"
	case "NOTE":
		return "Observação declarada"
	default:
		return "Registro complementar"
	}
}

func handoverString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}

// FormatAttributeSource renders provenance categories without transcript text or internal note identifiers.
func FormatAttributeSource(source AttributeSource) string {
	sourceLabel := map[string]string{"VISUAL": "Fonte visual", "VOICE": "Declaração por voz", "HUMAN_REVIEW": "Revisão humana"}[source.Source]
	if sourceLabel == "" {
		sourceLabel = "Fonte registrada"
	}
	attribute := map[string]string{"name": "item", "condition": "condição", "quantity": "quantidade", "material": "material", "color": "cor", "description": "descrição"}[source.Attribute]
	if attribute == "" {
		attribute = source.Attribute
	}
	value := sourceLabel + " · " + attribute
	if len(source.EvidenceIDs) > 0 {
		value += " · evidência " + strings.Join(source.EvidenceIDs, ", ")
	}
	return value
}
