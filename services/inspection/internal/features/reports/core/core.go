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
	Context            Context           `json:"context"`
	Requirements       []Requirement     `json:"requirements,omitempty"`
	Findings           []Finding         `json:"findings,omitempty"`
	Evidence           []Evidence        `json:"evidence,omitempty"`
	Advisory           string            `json:"advisory"`
}

// Context is the immutable, reader-friendly identity of the inspection at the
// moment its report is generated. It deliberately contains no contact details
// or precise location coordinates.
type Context struct {
	Asset       AssetContext       `json:"asset"`
	Participant ParticipantContext `json:"participant"`
	Template    TemplateContext    `json:"template"`
	Inspection  InspectionContext  `json:"inspection"`
}

type AssetContext struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ExternalKey string `json:"externalKey"`
	Address     string `json:"address"`
}

type ParticipantContext struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type TemplateContext struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int    `json:"version"`
}

type InspectionContext struct {
	ProjectID   string `json:"projectId,omitempty"`
	StageID     string `json:"stageId,omitempty"`
	StageLabel  string `json:"stageLabel,omitempty"`
	DueAt       string `json:"dueAt,omitempty"`
	SubmittedAt string `json:"submittedAt,omitempty"`
	GeneratedAt string `json:"generatedAt"`
}

// Requirement preserves the labels that make a captured image meaningful to
// a reader even when its source template changes later.
type Requirement struct {
	Key                 string `json:"key"`
	Section             string `json:"section"`
	Label               string `json:"label"`
	Instructions        string `json:"instructions,omitempty"`
	Coverage            string `json:"coverage,omitempty"`
	ImpossibilityReason string `json:"impossibilityReason,omitempty"`
}

// Finding is an immutable, neutral observation included in the internal
// report. It deliberately carries no fault, cost, liability, or automatic
// consequence.
type Finding struct {
	ID                string   `json:"id,omitempty"`
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
	Role           string   `json:"role"`
	Description    string   `json:"description,omitempty"`
	CaptureSource  string   `json:"captureSource,omitempty"`
	CapturedAt     string   `json:"capturedAt,omitempty"`
	DisplayDigest  string   `json:"displayDigest,omitempty"`
	Availability   string   `json:"availability,omitempty"`
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
	if strings.TrimSpace(snapshot.Context.Asset.ID) == "" || strings.TrimSpace(snapshot.Context.Asset.Name) == "" || strings.TrimSpace(snapshot.Context.Asset.Address) == "" || strings.TrimSpace(snapshot.Context.Participant.ID) == "" || strings.TrimSpace(snapshot.Context.Participant.Name) == "" || strings.TrimSpace(snapshot.Context.Template.ID) == "" || strings.TrimSpace(snapshot.Context.Template.Name) == "" || snapshot.Context.Template.Version < 1 || strings.TrimSpace(snapshot.Context.Inspection.GeneratedAt) == "" {
		return fmt.Errorf("report context is required")
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
		if strings.TrimSpace(evidence.ID) == "" || strings.TrimSpace(evidence.RequirementKey) == "" || (evidence.Role != "" && evidence.Role != "CURRENT" && evidence.Role != "REFERENCE") {
			return fmt.Errorf("report evidence identity is required")
		}
		for _, flag := range evidence.Flags {
			if strings.Contains(strings.ToLower(flag), "http://") || strings.Contains(strings.ToLower(flag), "https://") {
				return fmt.Errorf("report evidence cannot persist mutable media URLs")
			}
		}
	}
	for _, requirement := range snapshot.Requirements {
		if strings.TrimSpace(requirement.Key) == "" || strings.TrimSpace(requirement.Section) == "" || strings.TrimSpace(requirement.Label) == "" {
			return fmt.Errorf("report requirement is invalid")
		}
	}
	return nil
}

// HTML renders a safe internal view. It intentionally cannot include public
// media URLs, storage credentials, or an external-session link.
func HTML(snapshot Snapshot) ([]byte, error) {
	return HTMLForPDF(snapshot, nil, true)
}

// HTMLForPDF renders a self-contained document whose image paths are local
// asset names supplied to the private renderer. Availability is evaluated at
// render time so a permanently missing derivative receives a clear marker.
func HTMLForPDF(snapshot Snapshot, availability map[string]bool, internal bool) ([]byte, error) {
	if err := Validate(snapshot); err != nil {
		return nil, err
	}
	const source = `<!doctype html><html lang="pt-BR"><head><meta charset="utf-8"><style>body{font:14px Arial,sans-serif;color:#172033;margin:32px}h1{margin-bottom:4px}.muted{color:#526078}.badge{display:inline-block;padding:4px 8px;background:#e8edf8;border-radius:999px}.requirement{break-inside:avoid;margin:24px 0}.gallery{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}.evidence{border:1px solid #d7deea;padding:8px}.evidence img{width:100%;max-height:260px;object-fit:contain}.marker{padding:32px 12px;background:#f2f4f8;color:#526078;text-align:center}.finding{border-left:3px solid #516fc4;padding-left:10px;margin:10px 0}</style></head><body><h1>{{if .Internal}}Laudo interno de vistoria{{else}}Laudo de vistoria{{end}}</h1><p class="muted">{{.Snapshot.Context.Asset.Name}} · {{.Snapshot.Context.Asset.Address}}</p><p>Responsável: {{.Snapshot.Context.Participant.Name}} · Modelo: {{.Snapshot.Context.Template.Name}}</p><p><span class="badge">{{classification .Snapshot.Classification}}</span> · {{mode .Snapshot.Mode}}</p><p>{{.Snapshot.Advisory}}</p><h2>Motivos</h2><ul>{{range .Snapshot.ReasonCodes}}<li>{{reason .}}</li>{{end}}</ul><h2>Evidências</h2>{{range .EvidenceGroups}}<section class="requirement"><h3>{{.Label}}</h3><p class="muted">{{.Instructions}}</p><div class="gallery">{{range .Evidence}}<figure class="evidence" data-evidence-id="{{.ID}}">{{if available .ID}}<img src="{{assetName .ID}}" alt="{{.Role}} · {{.Description}}">{{else}}<div class="marker">Imagem indisponível</div>{{end}}<figcaption><strong>{{evidenceRole .Role}}</strong>{{if .Description}} · {{.Description}}{{end}}{{if .CapturedAt}} · {{.CapturedAt}}{{end}}{{range .Flags}} · {{flag .}}{{end}}</figcaption></figure>{{end}}</div></section>{{end}}{{if .Internal}}<h2>Constatações</h2>{{if .Snapshot.Findings}}{{range .Snapshot.Findings}}<section class="finding"><strong>{{.Title}}</strong><p>{{.Description}}</p><p>Severidade: {{severity .Severity}} · Confiança: {{.Confidence}} · Ação recomendada: {{action .RecommendedAction}}</p></section>{{end}}{{else}}<p>Nenhuma constatação foi registrada.</p>{{end}}{{end}}</body></html>`
	t, err := template.New("report").Funcs(template.FuncMap{
		"classification": presentClassification, "mode": presentMode, "reason": presentReason,
		"requirement": presentRequirement, "captureSource": presentCaptureSource, "flag": presentFlag, "severity": presentSeverity, "action": presentAction,
		"evidenceRole": presentEvidenceRole, "assetName": evidenceAssetName,
		"available": func(id string) bool { return availability == nil || availability[id] },
	}).Parse(source)
	if err != nil {
		return nil, err
	}
	var rendered strings.Builder
	if err := t.Execute(&rendered, htmlModel{Snapshot: snapshot, EvidenceGroups: groupEvidence(snapshot), Internal: internal}); err != nil {
		return nil, err
	}
	return []byte(rendered.String()), nil
}

type evidenceGroup struct {
	Label, Instructions string
	Evidence            []Evidence
}
type htmlModel struct {
	Snapshot       Snapshot
	EvidenceGroups []evidenceGroup
	Internal       bool
}

func groupEvidence(snapshot Snapshot) []evidenceGroup {
	byKey := make(map[string]*evidenceGroup, len(snapshot.Requirements))
	groups := make([]evidenceGroup, 0, len(snapshot.Requirements))
	for _, requirement := range snapshot.Requirements {
		groups = append(groups, evidenceGroup{Label: requirement.Label, Instructions: requirement.Instructions})
		byKey[requirement.Key] = &groups[len(groups)-1]
	}
	for _, item := range snapshot.Evidence {
		group, ok := byKey[item.RequirementKey]
		if !ok {
			groups = append(groups, evidenceGroup{Label: presentRequirement(item.RequirementKey)})
			group = &groups[len(groups)-1]
			byKey[item.RequirementKey] = group
		}
		group.Evidence = append(group.Evidence, item)
	}
	return groups
}

func evidenceAssetName(id string) string {
	return "evidence-" + strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return -1
	}, id) + ".jpg"
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
func presentEvidenceRole(value string) string {
	return reportLabel(map[string]string{"REFERENCE": "Referência", "CURRENT": "Vistoria atual"}, value)
}
func reportLabel(labels map[string]string, value string) string {
	if label, ok := labels[value]; ok {
		return label
	}
	return "Situação não reconhecida"
}
