// Package core owns immutable, internal advisory report snapshot construction.
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
)

// Snapshot is the canonical report representation. JSON and HTML derive from
// exactly this immutable value; PDF rendering may fail without changing it.
type Snapshot struct {
	SchemaVersion      int               `json:"schemaVersion"`
	ReportID           string            `json:"reportId"`
	InspectionID       string            `json:"inspectionId"`
	ProjectID          string            `json:"projectId,omitempty"`
	TemplateVersionID  string            `json:"templateVersionId"`
	ReferenceVersionID string            `json:"referenceVersionId"`
	ProfileVersionID   string            `json:"profileVersionId"`
	Mode               string            `json:"mode"`
	Classification     string            `json:"classification"`
	ReasonCodes        []string          `json:"reasonCodes"`
	Timeline           []TimelineEntry   `json:"timeline"`
	Coverage           map[string]string `json:"coverage"`
	Findings           []Finding         `json:"findings,omitempty"`
	Evidence           []Evidence        `json:"evidence,omitempty"`
	Advisory           string            `json:"advisory"`
}

// Finding is an immutable, neutral observation included in the internal
// report. It deliberately carries no fault, cost, liability, or automatic
// consequence.
type Finding struct {
	Category          string   `json:"category"`
	Title             string   `json:"title"`
	Description       string   `json:"description"`
	Severity          string   `json:"severity"`
	Confidence        float64  `json:"confidence"`
	EvidenceIDs       []string `json:"evidenceIds"`
	Quality           string   `json:"quality"`
	RecommendedAction string   `json:"recommendedAction"`
}

// Evidence is a report-safe view of current evidence metadata. Object keys,
// signed URLs, and media bytes never enter a snapshot.
type Evidence struct {
	ID             string   `json:"id"`
	RequirementKey string   `json:"requirementKey"`
	Description    string   `json:"description,omitempty"`
	CaptureSource  string   `json:"captureSource,omitempty"`
	Flags          []string `json:"flags,omitempty"`
}

type TimelineEntry struct {
	StageID        string `json:"stageId"`
	Classification string `json:"classification"`
	Status         string `json:"status"`
	Position       int    `json:"position"`
}

// CanonicalJSON returns stable JSON and its content digest for immutable
// storage. Map encoding is deterministic in encoding/json.
func CanonicalJSON(snapshot Snapshot) ([]byte, string, error) {
	if err := Validate(snapshot); err != nil {
		return nil, "", err
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

func Validate(snapshot Snapshot) error {
	if snapshot.SchemaVersion <= 0 || strings.TrimSpace(snapshot.ReportID) == "" || strings.TrimSpace(snapshot.InspectionID) == "" || strings.TrimSpace(snapshot.TemplateVersionID) == "" || strings.TrimSpace(snapshot.ReferenceVersionID) == "" || strings.TrimSpace(snapshot.ProfileVersionID) == "" {
		return fmt.Errorf("report snapshot identity is required")
	}
	if snapshot.Mode != "CONSOLIDATED" && snapshot.Mode != "HISTORICAL" {
		return fmt.Errorf("report mode is invalid")
	}
	if snapshot.Classification != "NORMAL" && snapshot.Classification != "ATTENTION" && snapshot.Classification != "CRITICAL" {
		return fmt.Errorf("report classification is invalid")
	}
	if snapshot.Advisory == "" {
		return fmt.Errorf("report advisory is required")
	}
	for key, value := range snapshot.Coverage {
		if strings.TrimSpace(key) == "" || strings.Contains(strings.ToLower(value), "http://") || strings.Contains(strings.ToLower(value), "https://") {
			return fmt.Errorf("report snapshot cannot persist mutable media URLs")
		}
	}
	for _, finding := range snapshot.Findings {
		if strings.TrimSpace(finding.Category) == "" || strings.TrimSpace(finding.Title) == "" || strings.TrimSpace(finding.Description) == "" || strings.TrimSpace(finding.Quality) == "" || strings.TrimSpace(finding.RecommendedAction) == "" || len(finding.EvidenceIDs) == 0 || finding.Confidence < 0 || finding.Confidence > 1 {
			return fmt.Errorf("report finding is invalid")
		}
		for _, evidenceID := range finding.EvidenceIDs {
			if strings.TrimSpace(evidenceID) == "" {
				return fmt.Errorf("report finding evidence is required")
			}
		}
	}
	for _, evidence := range snapshot.Evidence {
		if strings.TrimSpace(evidence.ID) == "" || strings.TrimSpace(evidence.RequirementKey) == "" {
			return fmt.Errorf("report evidence identity is required")
		}
		for _, flag := range evidence.Flags {
			if strings.Contains(strings.ToLower(flag), "http://") || strings.Contains(strings.ToLower(flag), "https://") {
				return fmt.Errorf("report evidence cannot persist mutable media URLs")
			}
		}
	}
	return nil
}

// HTML renders a safe internal view. It intentionally cannot include public
// media URLs, storage credentials, or an external-session link.
func HTML(snapshot Snapshot) ([]byte, error) {
	if err := Validate(snapshot); err != nil {
		return nil, err
	}
	const source = `<!doctype html><html lang="pt-BR"><body><h1>Laudo interno de vistoria</h1><p>{{.Advisory}}</p><p>Classificação: {{classification .Classification}} · Modalidade: {{mode .Mode}}</p><h2>Motivos</h2><ul>{{range .ReasonCodes}}<li>{{reason .}}</li>{{end}}</ul><h2>Evidências</h2><ul>{{range .Evidence}}<li data-evidence-id="{{.ID}}">{{requirement .RequirementKey}}: {{.Description}} ({{captureSource .CaptureSource}}){{range .Flags}} [{{flag .}}]{{end}}</li>{{end}}</ul><h2>Constatações</h2><ul>{{range .Findings}}<li><strong>{{.Title}}</strong> — {{.Description}} (severidade {{severity .Severity}}, confiança {{.Confidence}}, ação recomendada: {{action .RecommendedAction}})</li>{{end}}</ul></body></html>`
	t, err := template.New("report").Funcs(template.FuncMap{
		"classification": presentClassification, "mode": presentMode, "reason": presentReason,
		"requirement": presentRequirement, "captureSource": presentCaptureSource, "flag": presentFlag, "severity": presentSeverity, "action": presentAction,
	}).Parse(source)
	if err != nil {
		return nil, err
	}
	var rendered strings.Builder
	if err := t.Execute(&rendered, snapshot); err != nil {
		return nil, err
	}
	return []byte(rendered.String()), nil
}

func presentClassification(value string) string {
	return reportLabel(map[string]string{"NORMAL": "Sem alterações relevantes", "ATTENTION": "Requer atenção", "CRITICAL": "Crítica"}, value)
}
func presentMode(value string) string {
	return reportLabel(map[string]string{"CONSOLIDATED": "Consolidado", "HISTORICAL": "Histórico"}, value)
}
func presentReason(value string) string {
	return reportLabel(map[string]string{"MISSING_EVIDENCE": "Evidência ausente", "QUALITY_LOW": "Qualidade insuficiente", "CLASSIFICATION_CRITICAL": "Classificação crítica"}, value)
}
func presentRequirement(value string) string {
	if value == "overview" || value == "Property overview" {
		return "Visão geral do imóvel"
	}
	return "Requisito não reconhecido"
}
func presentCaptureSource(value string) string {
	return reportLabel(map[string]string{"CAMERA": "Câmera", "CAMERA_ONLY": "Câmera obrigatória", "GALLERY": "Galeria", "CAMERA_DEFAULT": "Câmera ou galeria conforme a política"}, value)
}
func presentFlag(value string) string {
	return reportLabel(map[string]string{"GPS_MISSING": "Localização ausente", "LOW_QUALITY": "Qualidade insuficiente", "SENSITIVE_DETECTION": "Detecção sensível"}, value)
}
func presentSeverity(value string) string {
	return reportLabel(map[string]string{"LOW": "Baixa", "MEDIUM": "Média", "HIGH": "Alta", "CRITICAL": "Crítica"}, value)
}
func presentAction(value string) string {
	return reportLabel(map[string]string{"REVIEW": "Revisar evidência", "RECOVER": "Solicitar complemento", "NO_ACTION": "Nenhuma ação adicional"}, value)
}
func reportLabel(labels map[string]string, value string) string {
	if label, ok := labels[value]; ok {
		return label
	}
	return "Situação não reconhecida"
}
