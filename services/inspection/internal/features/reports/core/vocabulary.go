package core

import "strings"

// unrecognizedLabel is shown for a code that has no Portuguese label; a raw
// code never reaches a reader.
const unrecognizedLabel = "Situação não reconhecida"

// EvidenceWithheld marks, only in an audience copy of a report, evidence that
// exists and is usable in the issued snapshot but is not shown to that
// audience (privacy block or pending screening). A stored snapshot never
// carries it.
const EvidenceWithheld = "WITHHELD"

// Customer-facing replacement and internal-only limitation texts of the
// descriptive checklist. The internal texts name the conflicting inputs
// (catalog, findings, voice and visual sources) and stay in internal reports;
// customer projections replace them with LimitationConditionUndetermined.
const (
	LimitationCatalogFindingConflict = "Há divergência entre o catálogo e uma constatação; ambos permanecem visíveis."
	LimitationSourceConflict         = "Há divergência entre fontes visual e por voz; as duas fontes permanecem identificadas para revisão."
	LimitationAmbiguousCatalog       = "Há mais de um item de catálogo associado ao componente; a condição não foi inferida."
	LimitationConditionUndetermined  = "A condição não foi determinada."
)

// IsInternalLimitation reports whether a checklist limitation reveals internal
// inputs and must never reach a customer projection.
func IsInternalLimitation(text string) bool {
	switch text {
	case LimitationCatalogFindingConflict, LimitationSourceConflict, LimitationAmbiguousCatalog:
		return true
	}
	// Earlier issued snapshots may carry variants of the same conflict texts.
	lower := strings.ToLower(text)
	return strings.Contains(lower, "divergência entre") || strings.Contains(lower, "item de catálogo")
}

// CustomerHandoverPayloadFields lists, per record kind, the only payload fields
// a customer may read. Free-text notes are internal and never listed.
var CustomerHandoverPayloadFields = map[string][]string{
	"KEY":     {"keyType", "quantity"},
	"CONTROL": {"controlType", "quantity"},
	"METER":   {"meterType", "reading", "unit", "date"},
}

// CustomerProcessingMessage returns the only processing message a customer
// projection may carry for a processing status.
func CustomerProcessingMessage(status string) string {
	switch status {
	case "FAILED":
		return "A análise não foi concluída."
	case "INCONCLUSIVE":
		return "A análise foi inconclusiva."
	default:
		return ""
	}
}

// flagLabels is the single Portuguese vocabulary of evidence flags emitted by
// capture, media verification and screening. Report v3 HTML, PDF and customer
// projections present flags only through FlagLabel.
var flagLabels = map[string]string{
	"GPS_MISSING":                      "Localização ausente",
	"GPS_MISSING_OR_INVALID":           "Localização ausente ou inválida",
	"GPS_OUTSIDE_GUIDED_WINDOW":        "Localização obtida fora da janela da captura",
	"GPS_LOW_ACCURACY":                 "Localização com baixa precisão",
	"OUT_OF_GEOFENCE":                  "Captura fora da área do imóvel",
	"GALLERY_SOURCE":                   "Imagem da galeria",
	"CAPTURE_TIME_CONFLICT":            "Horário da captura divergente",
	"CAPTURE_TIME_DEVICE_LOCAL":        "Horário local do dispositivo",
	"LOW_QUALITY":                      "Qualidade insuficiente",
	"SENSITIVE":                        "Conteúdo sensível",
	"SENSITIVE_DETECTION":              "Detecção sensível",
	"SENSITIVE_BLOCKED":                "Conteúdo sensível bloqueado",
	"SENSITIVE_CONTENT_FALSE_POSITIVE": "Alerta de conteúdo sensível descartado na revisão",
}

var qualityWarningLabels = map[string]string{"DARK": "imagem escura", "BLURRY": "imagem desfocada", "SHORT": "vídeo curto", "TOO_FEW_PHOTOS": "poucas fotos"}

var impossibilityLabels = map[string]string{"LOCKED": "local trancado", "NO_ACCESS": "sem acesso", "DOES_NOT_EXIST": "inexistente", "UNSAFE": "local inseguro", "OTHER": "outro motivo"}

// FlagLabel returns the Portuguese label of an evidence flag. Parameterized
// flags (quality warnings and declared impossibility) keep their reason code
// readable; an unknown flag is shown as "Situação não reconhecida".
func FlagLabel(flag string) string {
	upper := strings.ToUpper(strings.TrimSpace(flag))
	if label, ok := flagLabels[upper]; ok {
		return label
	}
	for _, prefix := range []struct {
		code, label string
		reasons     map[string]string
	}{
		{"QUALITY_WARNING_IGNORED:", "Alerta de qualidade ignorado", qualityWarningLabels},
		{"QUALITY_WARNING:", "Alerta de qualidade", qualityWarningLabels},
		{"IMPOSSIBILITY:", "Captura impossível", impossibilityLabels},
	} {
		if reason, ok := strings.CutPrefix(upper, prefix.code); ok {
			if detail := prefix.reasons[reason]; detail != "" {
				return prefix.label + ": " + detail
			}
			return prefix.label
		}
	}
	return unrecognizedLabel
}

// AnalysisStatusLabel is the single Portuguese vocabulary for analysis and
// processing statuses in report v3 (requirement analysis, processing state).
func AnalysisStatusLabel(status string) string {
	switch status {
	case "PENDING", "COMPLETED", "INCONCLUSIVE", "FAILED", "NOT_AUTHORIZED":
		return PresentationLabel(status)
	default:
		return unrecognizedLabel
	}
}

// codeLabels holds the remaining report enums shown by the PDF (roles,
// analysis modes, categories, severities, actions, inventory statuses and
// reference purposes). Reason codes keep their own vocabulary (ReasonLabel).
var codeLabels = map[string]string{
	"REFERENCE": "Referência", "CURRENT": "Vistoria atual",
	"CURRENT_ONLY": "Análise atual", "COMPARE_ORIGIN_CURRENT": "Comparação com referência",
	"CONSOLIDATED": "Consolidado", "HISTORICAL": "Histórico",
	"CONSERVATION": "Conservação", "INVENTORY": "Inventário", "CLEANLINESS": "Limpeza", "OBSTRUCTION": "Obstrução", "EVIDENCE_QUALITY": "Qualidade da evidência",
	"NONE": "Insuficiente", "LOW": "Baixa", "MEDIUM": "Média", "HIGH": "Alta", "CRITICAL": "Crítica",
	"REVIEW": "Revisar evidência", "RECOVER": "Solicitar complemento", "NO_ACTION": "Nenhuma ação adicional",
	"MISSING": "Item ausente", "CONDITION_CHANGED": "Condição alterada", "PRESENT_UNCHANGED": "Presente sem alteração relevante", "OUTSIDE_INVENTORY": "Fora do inventário", "IMPROVED": "Condição melhorada", "ENVIRONMENT_NOT_CAPTURED": "Ambiente não capturado",
	"MOVE_IN": "Entrada no imóvel", "CONSTRUCTION_HANDOVER": "Entrega da obra", "POST_RENOVATION": "Pós-reforma", "INVENTORY_UPDATE": "Atualização do inventário", "CONTESTATION_ADJUSTMENT": "Ajuste de contestação",
}

// CodeLabel presents any report v3 code through the shared vocabulary:
// statuses and conditions (PresentationLabel), evidence flags (FlagLabel) and
// the remaining report enums.
func CodeLabel(value string) string {
	if value == "" {
		return ""
	}
	if label := PresentationLabel(value); label != unrecognizedLabel {
		return label
	}
	if label, ok := codeLabels[value]; ok {
		return label
	}
	return FlagLabel(value)
}

// EvidenceNotice returns the report-level notice about the evidence of a v3
// snapshot: none when visible evidence exists, a no-evidence warning when the
// report has no usable evidence, and a distinct notice when usable evidence
// exists but is withheld from this audience.
func EvidenceNotice(snapshot Snapshot) string {
	if snapshot.SchemaVersion < 3 || hasVisibleEvidence(snapshot) {
		return ""
	}
	if !hasUsableEvidence(snapshot) {
		return "Nenhuma evidência registrada; isso não significa ausência de problemas."
	}
	return "As evidências registradas não são exibidas nesta versão do laudo por restrição de privacidade ou revisão pendente; isso não significa ausência de evidências."
}

func hasVisibleEvidence(snapshot Snapshot) bool {
	for _, item := range snapshot.Evidence {
		if item.Availability == "AVAILABLE" {
			return true
		}
	}
	return false
}
