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
	VideoPlanID        string            `json:"videoPlanId,omitempty"`
	PromptDigest       string            `json:"promptDigest"`
	Mode               string            `json:"mode"`
	Classification     string            `json:"classification"`
	ReasonCodes        []string          `json:"reasonCodes"`
	Timeline           []TimelineEntry   `json:"timeline"`
	Coverage           map[string]string `json:"coverage"`
	Context            Context           `json:"context"`
	Requirements       []Requirement     `json:"requirements,omitempty"`
	Findings           []Finding         `json:"findings,omitempty"`
	Evidence           []Evidence        `json:"evidence,omitempty"`
	Inventory          *InventorySection `json:"inventory,omitempty"`
	// ScopeStatements bound video conclusions to the observed scope.
	ScopeStatements []string `json:"scopeStatements,omitempty"`
	Advisory        string   `json:"advisory"`
}

// InventorySection is the exception-first inventory projection in snapshot v2.
type InventorySection struct {
	Reference     InventoryReference `json:"reference"`
	Coverage      *InventoryCoverage `json:"coverage,omitempty"`
	Problems      []InventoryEntry   `json:"problems"`
	Informational []InventoryEntry   `json:"informational"`
}

type InventoryReference struct {
	VersionID     string `json:"versionId"`
	VersionNumber int    `json:"versionNumber"`
	Purpose       string `json:"purpose"`
	ActivatedAt   string `json:"activatedAt"`
}

type InventoryCoverage struct {
	ExpectedItems                  int      `json:"expectedItems"`
	AssessedItems                  int      `json:"assessedItems"`
	NotObserved                    int      `json:"notObserved"`
	EnvironmentsNotCaptured        []string `json:"environmentsNotCaptured"`
	EnvironmentsAddedDuringCapture []string `json:"environmentsAddedDuringCapture"`
}

type InventoryEntry struct {
	ItemIdentityID  string                  `json:"itemIdentityId,omitempty"`
	Environment     string                  `json:"environment"`
	Type            string                  `json:"type"`
	Name            string                  `json:"name"`
	Status          string                  `json:"status"`
	ConditionBefore string                  `json:"conditionBefore,omitempty"`
	ConditionAfter  string                  `json:"conditionAfter,omitempty"`
	Reason          string                  `json:"reason,omitempty"`
	Pairs           []InventoryEvidencePair `json:"pairs,omitempty"`
}

type InventoryEvidencePair struct {
	Origin      string `json:"origin,omitempty"`
	Current     string `json:"current,omitempty"`
	Decision    string `json:"decision"`
	TimestampMS *int64 `json:"timestampMs,omitempty"`
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
	Key                   string `json:"key"`
	Section               string `json:"section"`
	Label                 string `json:"label"`
	PresentationLocalized bool   `json:"presentationLocalized,omitempty"`
	Instructions          string `json:"instructions,omitempty"`
	Coverage              string `json:"coverage,omitempty"`
	ImpossibilityReason   string `json:"impossibilityReason,omitempty"`
	NoRelevantChange      *bool  `json:"noRelevantChange"`
	AnalysisMode          string `json:"analysisMode"`
	AnalysisStatus        string `json:"analysisStatus"`
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
	ID                 string   `json:"id"`
	RequirementKey     string   `json:"requirementKey"`
	Role               string   `json:"role"`
	EvidenceKind       string   `json:"evidenceKind,omitempty"`
	FrameID            string   `json:"frameId,omitempty"`
	SourceMediaID      string   `json:"sourceMediaId,omitempty"`
	PreparationID      string   `json:"preparationId,omitempty"`
	PTSMicros          *int64   `json:"ptsMicros,omitempty"`
	TimestampMS        *int64   `json:"timestampMs,omitempty"`
	FrameDigest        string   `json:"frameDigest,omitempty"`
	SourceAvailability string   `json:"sourceAvailability,omitempty"`
	Description        string   `json:"description,omitempty"`
	CaptureSource      string   `json:"captureSource,omitempty"`
	CapturedAt         string   `json:"capturedAt,omitempty"`
	DisplayDigest      string   `json:"displayDigest,omitempty"`
	Availability       string   `json:"availability,omitempty"`
	Flags              []string `json:"flags,omitempty"`
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
	if snapshot.SchemaVersion <= 0 || strings.TrimSpace(snapshot.ReportID) == "" || strings.TrimSpace(snapshot.InspectionID) == "" || strings.TrimSpace(snapshot.TemplateVersionID) == "" || strings.TrimSpace(snapshot.ReferenceVersionID) == "" || strings.TrimSpace(snapshot.PromptDigest) == "" {
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
	if snapshot.Inventory != nil {
		ref := snapshot.Inventory.Reference
		if snapshot.SchemaVersion != 2 || strings.TrimSpace(ref.VersionID) == "" || ref.VersionNumber < 1 {
			return fmt.Errorf("inventory report reference is invalid")
		}
		// A comparative report references an activated baseline; a cataloging
		// report references its own draft and carries no coverage.
		if snapshot.Inventory.Coverage != nil && (strings.TrimSpace(ref.Purpose) == "" || strings.TrimSpace(ref.ActivatedAt) == "") {
			return fmt.Errorf("inventory report reference is invalid")
		}
		known := make(map[string]bool, len(snapshot.Evidence))
		for _, evidence := range snapshot.Evidence {
			known[evidence.ID] = true
		}
		entries := append(append([]InventoryEntry{}, snapshot.Inventory.Problems...), snapshot.Inventory.Informational...)
		for _, entry := range entries {
			if strings.TrimSpace(entry.Environment) == "" || strings.TrimSpace(entry.Type) == "" || strings.TrimSpace(entry.Name) == "" {
				return fmt.Errorf("inventory report entry is invalid")
			}
			for _, pair := range entry.Pairs {
				if pair.Decision != "SAME_VIEW" && pair.Decision != "NOT_COMPARABLE" && pair.Decision != "UNCERTAIN" {
					return fmt.Errorf("inventory pair decision is invalid")
				}
				if (pair.Origin == "" && pair.Current == "") || (pair.Origin != "" && !known[pair.Origin]) || (pair.Current != "" && !known[pair.Current]) {
					return fmt.Errorf("inventory pair references unknown evidence")
				}
			}
		}
	} else if snapshot.SchemaVersion == 2 {
		return fmt.Errorf("inventory report v2 requires inventory section")
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
		if finding.Category != "CONSERVATION" && finding.Category != "INVENTORY" && finding.Category != "CLEANLINESS" && finding.Category != "OBSTRUCTION" && finding.Category != "EVIDENCE_QUALITY" {
			return fmt.Errorf("report finding category is invalid")
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
		if evidence.EvidenceKind != "" && evidence.EvidenceKind != "PHOTO" && evidence.EvidenceKind != "VIDEO_FRAME" {
			return fmt.Errorf("report evidence kind is invalid")
		}
		if evidence.EvidenceKind == "VIDEO_FRAME" && (evidence.FrameID != evidence.ID || strings.TrimSpace(evidence.SourceMediaID) == "" || strings.TrimSpace(evidence.PreparationID) == "" || evidence.TimestampMS == nil || *evidence.TimestampMS < 0 || len(evidence.FrameDigest) != 64) {
			return fmt.Errorf("video frame provenance is incomplete")
		}
		if evidence.EvidenceKind == "VIDEO_FRAME" && evidence.PTSMicros != nil && (*evidence.PTSMicros < 0 || *evidence.PTSMicros/1000 != *evidence.TimestampMS) {
			return fmt.Errorf("video frame timestamp is inconsistent")
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
		if requirement.AnalysisMode != "CURRENT_ONLY" && requirement.AnalysisMode != "COMPARE_ORIGIN_CURRENT" {
			return fmt.Errorf("report requirement analysis mode is invalid")
		}
		if requirement.AnalysisStatus != "PENDING" && requirement.AnalysisStatus != "COMPLETED" && requirement.AnalysisStatus != "INCONCLUSIVE" && requirement.AnalysisStatus != "FAILED" {
			return fmt.Errorf("report requirement analysis status is invalid")
		}
		if requirement.AnalysisMode == "CURRENT_ONLY" && requirement.NoRelevantChange != nil {
			return fmt.Errorf("current-only report must have null noRelevantChange")
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
	originalScopeStatements := append([]string(nil), snapshot.ScopeStatements...)
	snapshot = LocalizeVideoPresentation(snapshot)
	const source = `<!doctype html><html lang="pt-BR"><head><meta charset="utf-8"><style>body{font:14px Arial,sans-serif;color:#172033;margin:32px}h1{margin-bottom:4px}.muted{color:#526078}.badge{display:inline-block;padding:4px 8px;background:#e8edf8;border-radius:999px}.requirement{break-inside:avoid;margin:24px 0}.gallery{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}.evidence{border:1px solid #d7deea;padding:8px}.evidence img{width:100%;max-height:260px;object-fit:contain}.marker{padding:32px 12px;background:#f2f4f8;color:#526078;text-align:center}.finding{border-left:3px solid #516fc4;padding-left:10px;margin:10px 0}</style></head><body><h1>{{if .Internal}}Laudo interno de vistoria{{else}}Laudo de vistoria{{end}}</h1><p class="muted">{{.Snapshot.Context.Asset.Name}} · {{.Snapshot.Context.Asset.Address}}</p><p>Responsável: {{.Snapshot.Context.Participant.Name}} · Modelo: {{.Snapshot.Context.Template.Name}}</p><p><span class="badge">{{classification .Snapshot.Classification}}</span> · {{mode .Snapshot.Mode}}</p><p>{{.Snapshot.Advisory}}</p>{{if .Snapshot.ScopeStatements}}<h2>Escopo da análise</h2><ul>{{range $index, $statement := .Snapshot.ScopeStatements}}<li data-source-statement="{{index $.OriginalScopeStatements $index}}">{{$statement}}</li>{{end}}</ul>{{end}}<h2>Motivos</h2><ul>{{range .Snapshot.ReasonCodes}}<li>{{reason .}}</li>{{end}}</ul>{{if .Snapshot.Inventory}}<section id="inventory-summary"><p>{{if .Snapshot.Inventory.Reference.Purpose}}Referência: {{inventoryPurpose .Snapshot.Inventory.Reference.Purpose}}{{else}}Inventário catalogado nesta vistoria{{end}}{{if .Snapshot.Inventory.Coverage}} · {{.Snapshot.Inventory.Coverage.AssessedItems}} de {{.Snapshot.Inventory.Coverage.ExpectedItems}} itens avaliados · {{.Snapshot.Inventory.Coverage.NotObserved}} não observados{{end}}</p></section>{{if .Snapshot.Inventory.Problems}}<section id="inventory-problems"><h2>Problemas do inventário</h2>{{range .Snapshot.Inventory.Problems}}<article><h3>{{.Environment}} · {{.Name}}</h3><p>{{.Type}} · {{inventoryStatus .Status}}{{if .ConditionBefore}} · {{condition .ConditionBefore}} → {{condition .ConditionAfter}}{{end}}</p>{{if .Reason}}<p>{{.Reason}}</p>{{end}}{{range .Pairs}}<div class="gallery"><figure class="evidence">{{if available .Origin}}<img src="{{assetName .Origin}}" alt="Evidência da referência">{{else}}<div class="marker">Imagem indisponível</div>{{end}}<figcaption>Referência</figcaption></figure><figure class="evidence">{{if available .Current}}<img src="{{assetName .Current}}" alt="Evidência atual">{{else}}<div class="marker">Imagem indisponível</div>{{end}}<figcaption>Vistoria atual{{if .TimestampMS}} · {{.TimestampMS}} ms{{end}}</figcaption></figure></div>{{end}}</article>{{end}}</section>{{end}}{{end}}<h2>Evidências</h2>{{range .EvidenceGroups}}<section class="requirement"><h3>{{.Label}}</h3><p class="muted">{{.Instructions}}</p><p class="muted">{{analysisMode .AnalysisMode}} · {{analysisStatus .AnalysisStatus}}{{if .HasNoRelevantChange}} · {{noRelevantChange .NoRelevantChange}}{{end}}</p>{{if .Coverage}}<p class="muted">{{.Coverage}}</p>{{end}}<div class="gallery">{{range .Evidence}}<figure class="evidence" data-evidence-id="{{.ID}}">{{if available .ID}}<img src="{{assetName .ID}}" alt="{{.Role}} · {{.Description}}">{{else}}<div class="marker">Imagem indisponível</div>{{end}}<figcaption><strong>{{evidenceRole .Role}}</strong>{{if .Description}} · {{.Description}}{{end}}{{if .CapturedAt}} · {{.CapturedAt}}{{end}}{{range .Flags}} · {{flag .}}{{end}}</figcaption></figure>{{end}}</div></section>{{end}}{{if .Internal}}<h2>Constatações</h2>{{if .Snapshot.Findings}}{{range .Snapshot.Findings}}<section class="finding"><strong>{{category .Category}} · {{.Title}}</strong><p>{{.Description}}</p><p>Severidade: {{severity .Severity}} · Confiança: {{.Confidence}} · Ação recomendada: {{action .RecommendedAction}}</p></section>{{end}}{{else}}<p>{{noFindings .Snapshot}}</p>{{end}}{{end}}</body></html>`
	t, err := template.New("report").Funcs(template.FuncMap{
		"classification": presentClassification, "mode": presentMode, "reason": presentReason,
		"requirement": presentRequirement, "captureSource": presentCaptureSource, "flag": presentFlag, "severity": presentSeverity, "action": presentAction,
		"evidenceRole": presentEvidenceRole, "assetName": evidenceAssetName, "analysisMode": presentAnalysisMode, "analysisStatus": presentAnalysisStatus, "noRelevantChange": presentNoRelevantChange, "hasNoRelevantChange": hasNoRelevantChange, "category": presentCategory, "noFindings": noFindings,
		"available": func(id string) bool { return availability == nil || availability[id] },
		"inventoryPurpose": func(value string) string {
			return reportLabel(map[string]string{"MOVE_IN": "Entrada no imóvel", "CONSTRUCTION_HANDOVER": "Entrega da obra", "POST_RENOVATION": "Pós-reforma", "INVENTORY_UPDATE": "Atualização do inventário", "CONTESTATION_ADJUSTMENT": "Ajuste de contestação"}, value)
		},
		"inventoryStatus": func(value string) string {
			return reportLabel(map[string]string{"MISSING": "Item ausente", "CONDITION_CHANGED": "Condição alterada", "NOT_OBSERVED": "Não observado", "ENVIRONMENT_NOT_CAPTURED": "Ambiente não capturado"}, value)
		},
		"condition": func(value string) string {
			return reportLabel(map[string]string{"NEW": "Novo", "GOOD": "Bom", "FAIR": "Regular", "POOR": "Ruim", "NOT_ASSESSABLE": "Não avaliável"}, value)
		},
	}).Parse(source)
	if err != nil {
		return nil, err
	}
	var rendered strings.Builder
	if err := t.Execute(&rendered, htmlModel{Snapshot: snapshot, OriginalScopeStatements: originalScopeStatements, EvidenceGroups: groupEvidence(snapshot), Internal: internal}); err != nil {
		return nil, err
	}
	return []byte(rendered.String()), nil
}

type evidenceGroup struct {
	Label, Instructions, Coverage, AnalysisMode, AnalysisStatus string
	NoRelevantChange                                            *bool
	HasNoRelevantChange                                         bool
	Evidence                                                    []Evidence
}
type htmlModel struct {
	Snapshot                Snapshot
	OriginalScopeStatements []string
	EvidenceGroups          []evidenceGroup
	Internal                bool
}

func groupEvidence(snapshot Snapshot) []evidenceGroup {
	byKey := make(map[string]*evidenceGroup, len(snapshot.Requirements))
	groups := make([]evidenceGroup, 0, len(snapshot.Requirements))
	for _, requirement := range snapshot.Requirements {
		groups = append(groups, evidenceGroup{Label: requirement.Label, Instructions: requirement.Instructions, Coverage: requirement.Coverage, AnalysisMode: requirement.AnalysisMode, AnalysisStatus: requirement.AnalysisStatus, NoRelevantChange: requirement.NoRelevantChange, HasNoRelevantChange: requirement.AnalysisMode == "COMPARE_ORIGIN_CURRENT" && requirement.NoRelevantChange != nil})
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
	return reportLabel(map[string]string{"NORMAL": "Sem alertas identificados", "ATTENTION": "Requer atenção", "CRITICAL": "Crítica"}, value)
}
func presentMode(value string) string {
	return reportLabel(map[string]string{"CONSOLIDATED": "Consolidado", "HISTORICAL": "Histórico"}, value)
}
func presentReason(value string) string {
	return reportLabel(map[string]string{"MISSING_EVIDENCE": "Evidência ausente", "QUALITY_LOW": "Qualidade insuficiente", "INCONCLUSIVE": "Comparação inconclusiva", "ANALYSIS_FAILED": "Falha técnica na análise", "OBSERVED_CHANGE": "Mudança observada", "CRITICAL_FINDING": "Achado crítico", "CLASSIFICATION_CRITICAL": "Classificação crítica"}, value)
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
	return reportLabel(map[string]string{"NONE": "Insuficiente", "LOW": "Baixa", "MEDIUM": "Média", "HIGH": "Alta", "CRITICAL": "Crítica"}, value)
}
func presentAnalysisMode(value string) string {
	return reportLabel(map[string]string{"CURRENT_ONLY": "Análise atual", "COMPARE_ORIGIN_CURRENT": "Comparação com referência"}, value)
}
func presentAnalysisStatus(value string) string {
	return reportLabel(map[string]string{"PENDING": "Pendente", "COMPLETED": "Concluída", "INCONCLUSIVE": "Inconclusiva", "FAILED": "Falha técnica"}, value)
}
func presentNoRelevantChange(value *bool) string {
	if value == nil {
		return "Comparação inconclusiva"
	}
	if *value {
		return "Sem alteração relevante identificada"
	}
	return "Alteração relevante identificada"
}
func hasNoRelevantChange(value evidenceGroup) bool { return value.HasNoRelevantChange }
func presentCategory(value string) string {
	return reportLabel(map[string]string{"CONSERVATION": "Conservação", "INVENTORY": "Inventário", "CLEANLINESS": "Limpeza", "OBSTRUCTION": "Obstrução", "EVIDENCE_QUALITY": "Qualidade da evidência"}, value)
}
func presentAction(value string) string {
	if label := reportLabel(map[string]string{"REVIEW": "Revisar evidência", "RECOVER": "Solicitar complemento", "NO_ACTION": "Nenhuma ação adicional"}, value); label != "Situação não reconhecida" {
		return label
	}
	return value
}
func presentEvidenceRole(value string) string {
	return reportLabel(map[string]string{"REFERENCE": "Referência", "CURRENT": "Vistoria atual"}, value)
}
func noFindings(snapshot Snapshot) string {
	return "Nenhuma constatação foi registrada; isso não confirma a ausência de problemas."
}
func reportLabel(labels map[string]string, value string) string {
	if label, ok := labels[value]; ok {
		return label
	}
	return "Situação não reconhecida"
}
