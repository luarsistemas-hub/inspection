package render_pdf

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	stdimage "image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"

	"inspection/services/inspection/internal/features/reports/core"
	customerreport "inspection/services/inspection/internal/features/reports/customer/core"
	"inspection/services/inspection/internal/platform/pdf"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"
	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	marotoimage "github.com/johnfercher/maroto/v2/pkg/components/image"
	"github.com/johnfercher/maroto/v2/pkg/components/page"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/extension"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/pagesize"
	marotocore "github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/fontrepository"
	"github.com/johnfercher/maroto/v2/pkg/props"
)

//go:embed fonts/Roboto-Regular.ttf
var reportFonts embed.FS

const maxPDFBytes = 64 << 20
const maxPDFPages = 200

var pdfPageMarker = regexp.MustCompile(`/Type\s*/Page\b`)

// PDFDocument is a public-specific projection of the immutable report data.
type PDFDocument struct {
	SnapshotID string
	Version    int
	Snapshot   core.Snapshot
	Internal   bool
}

// MarotoRenderer composes a report from canonical snapshot data without HTML.
type MarotoRenderer struct {
	DashboardOrigin string
	MaxEvidence     int
	MaxPages        int
	MaxImageBytes   int64
	MaxPDFBytes     int64
}

func (r MarotoRenderer) Render(ctx context.Context, document PDFDocument, assets pdf.AssetBundle) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	validSnapshot := core.Validate(document.Snapshot)
	if !document.Internal {
		validSnapshot = core.ValidateCustomerSnapshot(document.Snapshot)
	}
	if strings.TrimSpace(document.SnapshotID) == "" || document.Version <= 0 || validSnapshot != nil {
		return nil, fmt.Errorf("report PDF: invalid document")
	}
	if !document.Internal && len(document.Snapshot.Findings) > 0 {
		return nil, fmt.Errorf("report PDF: customer document contains internal findings")
	}
	document.Snapshot = core.LocalizeVideoPresentation(document.Snapshot)
	// v1/v2 documents keep their historical presentation exactly; report v3
	// adds evidence references, the evidence index and the shared vocabulary.
	presentation := presentationFor(document.Snapshot)
	label := presentation.label
	maxEvidence, maxPages := r.MaxEvidence, r.MaxPages
	maxImageBytes, maxPDFBytesLimit := r.MaxImageBytes, r.MaxPDFBytes
	if maxEvidence <= 0 {
		maxEvidence = 200
	}
	if maxPages <= 0 {
		maxPages = maxPDFPages
	}
	if maxImageBytes <= 0 {
		maxImageBytes = 128 << 20
	}
	if maxPDFBytesLimit <= 0 {
		maxPDFBytesLimit = maxPDFBytes
	}
	if UniqueImageCount(document.Snapshot.Evidence) > maxEvidence {
		return nil, fmt.Errorf("report PDF: evidence limit exceeded")
	}
	var totalAssetBytes int64
	for _, data := range assets {
		totalAssetBytes += int64(len(data))
	}
	if totalAssetBytes > maxImageBytes {
		return nil, fmt.Errorf("report PDF: image byte limit exceeded")
	}

	fontBytes, err := reportFonts.ReadFile("fonts/Roboto-Regular.ttf")
	if err != nil {
		return nil, fmt.Errorf("report PDF: load font: %w", err)
	}
	customFonts, err := fontrepository.New().AddUTF8FontFromBytes("Roboto", fontstyle.Normal, fontBytes).Load()
	if err != nil {
		return nil, fmt.Errorf("report PDF: load unicode font: %w", err)
	}
	pageNumber := props.PageNumber{Pattern: "{current} / {total}", Place: props.Bottom, Family: "Roboto", Size: 8}
	options := config.NewBuilder().
		WithPageSize(pagesize.A4).
		WithLeftMargin(15).
		WithRightMargin(15).
		WithTopMargin(15).
		WithBottomMargin(18).
		WithSequentialMode().
		WithDefaultFont(&props.Font{Family: "Roboto", Size: 10, Style: fontstyle.Normal, Color: &props.BlackColor}).
		WithCustomFonts(customFonts).
		WithPageNumber(pageNumber).
		Build()
	documentMaker := maroto.New(options)

	title := "LAUDO DE VISTORIA"
	if document.Internal {
		title = "LAUDO INTERNO DE VISTORIA"
	}
	asset := document.Snapshot.Context.Asset
	if err := documentMaker.RegisterHeader(
		text.NewAutoRow(title, props.Text{Size: 10, Family: "Roboto", Bottom: 2}),
		text.NewAutoRow(ellipsis(strings.TrimSpace(asset.Name+" · "+asset.Address), maxHeaderRunes), props.Text{Size: 9, Family: "Roboto"}),
		text.NewAutoRow("Classificação: "+core.ClassificationLabelForSnapshot(document.Snapshot), props.Text{Size: 9, Family: "Roboto", Bottom: 2}),
		row.New(3),
	); err != nil {
		return nil, fmt.Errorf("report PDF: register header: %w", err)
	}
	reportNumber := document.SnapshotID
	if presentation.v3 && document.Snapshot.ReportID != "" {
		reportNumber = document.Snapshot.ReportID
	}
	if err := documentMaker.RegisterFooter(text.NewAutoRow(ellipsis(fmt.Sprintf("Laudo %s · versão %d", reportNumber, document.Version), maxHeaderRunes), props.Text{Size: 8, Family: "Roboto"})); err != nil {
		return nil, fmt.Errorf("report PDF: register footer: %w", err)
	}

	addText := func(value string, prop props.Text) {
		prop.Family = "Roboto"
		documentMaker.AddRows(textRows(value, prop, fullWidthChunk)...)
	}
	addText("Classificação: "+core.ClassificationLabelForSnapshot(document.Snapshot)+" · "+label(document.Snapshot.Mode), props.Text{Size: 13, Top: 4, Bottom: 2})
	addText("Imóvel: "+asset.Name+" · "+asset.Address, props.Text{Size: 9, Bottom: 2})
	addText(fmt.Sprintf("Responsável: %s · Modelo: %s v%d", document.Snapshot.Context.Participant.Name, document.Snapshot.Context.Template.Name, document.Snapshot.Context.Template.Version), props.Text{Size: 9, Bottom: 2})
	addText(document.Snapshot.Advisory, props.Text{Size: 9, Bottom: 4})
	if document.Snapshot.VerificationCode != "" && r.DashboardOrigin != "" {
		code, err := qr.Encode(reportVerificationURL(r.DashboardOrigin, document.Snapshot.VerificationCode, document.Version), qr.M, qr.Auto)
		if err != nil {
			return nil, fmt.Errorf("report PDF: encode verification code: %w", err)
		}
		code, err = barcode.Scale(code, 96, 96)
		if err != nil {
			return nil, fmt.Errorf("report PDF: scale verification code: %w", err)
		}
		var pngBytes bytes.Buffer
		if err := png.Encode(&pngBytes, code); err != nil {
			return nil, fmt.Errorf("report PDF: render verification code: %w", err)
		}
		addText("Verifique a autenticidade deste laudo", props.Text{Size: 8, Top: 2, Bottom: 1})
		documentMaker.AddRows(marotoimage.NewFromBytesRow(18, pngBytes.Bytes(), extension.Png))
	}
	if document.Snapshot.Processing != nil {
		addText("Estado da análise: "+label(document.Snapshot.Processing.Status), props.Text{Size: 9, Bottom: 2})
		if document.Snapshot.Processing.Message != "" {
			addText(document.Snapshot.Processing.Message, props.Text{Size: 9, Bottom: 2})
		}
	}
	if notice := core.EvidenceNotice(document.Snapshot); notice != "" {
		addText(notice, props.Text{Size: 9, Bottom: 2})
	}
	if checklist := document.Snapshot.Checklist; checklist != nil {
		addText("Checklist descritivo", props.Text{Size: 11, Top: 4})
		if checklist.Coverage.UnknownScope {
			addText("Escopo não definido; não há percentual de cobertura.", props.Text{Size: 9})
		} else {
			addText(fmt.Sprintf("Cobertura: %d componentes esperados; %d avaliados; %d parcialmente avaliados; %d não observados; %d não aplicáveis; %d impossíveis de capturar.", checklist.Coverage.Expected, checklist.Coverage.FullyAssessed, checklist.Coverage.PartiallyAssessed, checklist.Coverage.NotObserved, checklist.Coverage.NotApplicable, checklist.Coverage.Impossible), props.Text{Size: 9})
		}
		for _, room := range checklist.Rooms {
			addText(room.Name, props.Text{Size: 10, Top: 4})
			if len(room.Components) == 0 {
				addText("Nenhum componente observado.", props.Text{Size: 9})
				continue
			}
			for _, component := range room.Components {
				line := component.Label + " · " + core.PresentationLabel(component.Status)
				if component.Description != "" {
					line += " · " + component.Description
				}
				if component.Condition != "" {
					line += " · " + core.PresentationLabel(component.Condition)
				}
				if component.ReviewStatus != "" {
					line += " · " + core.PresentationLabel(component.ReviewStatus)
				}
				addText(line, props.Text{Size: 9})
				if component.AssessedScope != "" {
					addText("Extensão avaliada: "+component.AssessedScope, props.Text{Size: 8})
				}
				if len(component.NotAssessed) > 0 {
					addText("Partes não avaliadas: "+strings.Join(component.NotAssessed, ", "), props.Text{Size: 8})
				}
				if component.OutsideScope && document.Internal {
					addText("Observado fora do escopo; não compõe a cobertura nem o inventário de referência.", props.Text{Size: 8})
				}
				if len(component.EvidenceIDs) > 0 {
					refs := core.EvidenceReferences(document.Snapshot, component.EvidenceIDs)
					addText("Evidência: "+strings.Join(refs, ", "), props.Text{Size: 8})
				}
				for _, source := range component.AttributeSources {
					addText(core.FormatAttributeSourceForSnapshot(document.Snapshot, source), props.Text{Size: 8})
				}
				for _, limitation := range component.Limitations {
					addText("Limitação: "+limitation, props.Text{Size: 8})
				}
			}
		}
		if len(checklist.Rooms) == 0 && len(document.Snapshot.Evidence) == 0 {
			addText("Nenhuma evidência registrada; isso não significa ausência de problemas.", props.Text{Size: 9})
		}
	}
	if handover := document.Snapshot.Handover; handover != nil && len(handover.Entries) > 0 {
		addText("Chaves, controles e medidores", props.Text{Size: 11, Top: 5})
		for _, entry := range handover.Entries {
			addText(core.FormatHandoverForSnapshot(document.Snapshot, entry, document.Internal), props.Text{Size: 9})
		}
	}
	if len(document.Snapshot.ScopeStatements) > 0 {
		addText("Escopo da análise", props.Text{Size: 11, Top: 2})
		for _, statement := range scopeStatementLines(document.Snapshot.ScopeStatements) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			addText(statement, props.Text{Size: 9})
		}
	}
	addText("Motivos", props.Text{Size: 11, Top: 2})
	if len(document.Snapshot.ReasonCodes) == 0 {
		addText("Nenhum motivo adicional registrado.", props.Text{Size: 9})
	} else {
		for _, reason := range document.Snapshot.ReasonCodes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			addText("• "+core.ReasonLabel(reason), props.Text{Size: 9})
		}
	}

	evidenceIndex := make(map[string]string, len(document.Snapshot.Evidence))
	if presentation.v3 {
		for index, evidence := range document.Snapshot.Evidence {
			evidenceIndex[evidence.ID] = fmt.Sprintf("E%03d", index+1)
		}
	}
	if presentation.v3 && len(document.Snapshot.Evidence) > 10 {
		addText("Anexo de evidências · índice", props.Text{Size: 11, Top: 6})
		for index, evidence := range document.Snapshot.Evidence {
			reference := fmt.Sprintf("E%03d", index+1)
			// A capture requirement key is internal: an evidence without
			// description is named by its role and room.
			addText(reference+" · "+ellipsis(core.EvidenceIndexLabel(document.Snapshot, evidence), 80), props.Text{Size: 8})
		}
	}
	if comparison := document.Snapshot.Comparison; comparison != nil {
		// Report v3 presents the complete comparison instead of the
		// exception-first inventory section; both derive from the same issue.
		addText("Comparação com a referência", props.Text{Size: 11, Top: 6})
		addText(core.ComparisonBaselineLine(comparison.Baseline), props.Text{Size: 9})
		addText("Estado: "+core.ComparisonStatusLabel(comparison.Status), props.Text{Size: 9})
		addText(core.ComparisonCountsLine(comparison.Counts), props.Text{Size: 9, Bottom: 2})
		for _, group := range core.GroupComparison(*comparison) {
			if group.Count == 0 {
				continue
			}
			addText(fmt.Sprintf("%s · %d de %d itens", group.Label, group.Count, group.Total), props.Text{Size: 10, Top: 4})
			for _, item := range group.Items {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				addText(core.ComparisonItemLine(item), props.Text{Size: 9, Top: 1})
				if document.Internal && item.Description != "" {
					addText(item.Description, props.Text{Size: 8})
				}
				for _, pair := range item.Pairs {
					line := core.ComparisonPairLine(document.Snapshot, pair)
					// The customer copy labels a paired image it cannot show
					// with the same rule as the customer GraphQL view.
					if note := customerreport.PairAvailabilityNote(document.Snapshot, pair); !document.Internal && note != "" {
						line += " · " + note
					}
					addText(line, props.Text{Size: 8})
				}
				for _, limitation := range item.Limitations {
					addText("Limitação: "+limitation, props.Text{Size: 8})
				}
			}
		}
		if len(comparison.OutsideBaseline) > 0 {
			addText("Observado fora da referência (informativo)", props.Text{Size: 10, Top: 4})
			for _, observation := range comparison.OutsideBaseline {
				addText(observation.Room+" · "+observation.Name, props.Text{Size: 9})
			}
		}
		addText(core.ComparisonDisclaimer, props.Text{Size: 8, Top: 2})
	} else if inventory := document.Snapshot.Inventory; inventory != nil {
		for i, line := range presentation.inventorySummaryLines(inventory) {
			prop := props.Text{Size: 9}
			if i == 0 {
				prop.Top = 6
			}
			addText(line, prop)
		}
		if len(inventory.Problems) > 0 {
			addText("Problemas do inventário", props.Text{Size: 11, Top: 6})
		}
		for _, item := range inventory.Problems {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			lead := &keptHeading{}
			lead.add(presentation.inventoryProblemHeading(item), props.Text{Size: 10, Top: 4})
			if item.ConditionBefore != "" || item.ConditionAfter != "" {
				lead.add(label(item.ConditionBefore)+" → "+label(item.ConditionAfter), props.Text{Size: 8})
			}
			if item.Reason != "" {
				lead.add(item.Reason, props.Text{Size: 8})
			}
			if len(item.Pairs) == 0 {
				lead.flush(documentMaker, 0)
			}
			for _, pair := range item.Pairs {
				lead.add("Evidência de referência ↔ vistoria atual", props.Text{Size: 8})
				paired := make([]core.Evidence, 0, 2)
				for _, side := range []struct{ id, role string }{{pair.Origin, "REFERENCE"}, {pair.Current, "CURRENT"}} {
					if side.id == "" {
						continue
					}
					availability := "MISSING"
					if assets["evidence-"+side.id+".jpg"] != nil {
						availability = "AVAILABLE"
					}
					paired = append(paired, core.Evidence{ID: side.id, Role: side.role, Availability: availability})
				}
				if len(paired) == 0 {
					lead.flush(documentMaker, 0)
					continue
				}
				if err := presentation.addEvidenceRows(ctx, documentMaker, lead, paired, assets, evidenceIndex, item.Name, document.SnapshotID, r.DashboardOrigin); err != nil {
					return nil, err
				}
				lead = &keptHeading{}
			}
		}
	}
	// Each requirement heading (title, instructions, summary) is kept on the
	// same page as the first image row of its evidence (PDF-KEEP); the
	// "Evidências" section title travels with the first heading.
	lead := &keptHeading{}
	lead.add("Evidências", props.Text{Size: 11, Top: 6})
	evidenceByRequirement := make(map[string][]core.Evidence, len(document.Snapshot.Requirements))
	for _, evidence := range document.Snapshot.Evidence {
		evidenceByRequirement[evidence.RequirementKey] = append(evidenceByRequirement[evidence.RequirementKey], evidence)
	}
	seenRequirements := make(map[string]bool, len(evidenceByRequirement))
	for _, requirement := range document.Snapshot.Requirements {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		seenRequirements[requirement.Key] = true
		lead.add(requirement.Section+" · "+requirement.Label, props.Text{Size: 10, Top: 4})
		lead.add(requirement.Instructions, props.Text{Size: 8})
		lead.add(presentation.analysisSummary(requirement), props.Text{Size: 8, Bottom: 3})
		items := evidenceByRequirement[requirement.Key]
		if len(items) == 0 {
			lead.add("Nenhuma evidência registrada para este requisito.", props.Text{Size: 8})
			lead.flush(documentMaker, 0)
			continue
		}
		if err := presentation.addEvidenceRows(ctx, documentMaker, lead, items, assets, evidenceIndex, requirement.Label, document.SnapshotID, r.DashboardOrigin); err != nil {
			return nil, err
		}
		lead = &keptHeading{}
	}
	extraKeys := make([]string, 0, len(evidenceByRequirement))
	for key := range evidenceByRequirement {
		if !seenRequirements[key] {
			extraKeys = append(extraKeys, key)
		}
	}
	sort.Strings(extraKeys)
	for _, key := range extraKeys {
		lead.add("Evidências adicionais", props.Text{Size: 10, Top: 4})
		if err := presentation.addEvidenceRows(ctx, documentMaker, lead, evidenceByRequirement[key], assets, evidenceIndex, "", document.SnapshotID, r.DashboardOrigin); err != nil {
			return nil, err
		}
		lead = &keptHeading{}
	}
	lead.flush(documentMaker, 0)
	if document.Internal {
		addText("Constatações", props.Text{Size: 11, Top: 6})
		if len(document.Snapshot.Findings) == 0 {
			addText("Nenhuma constatação registrada; isso não confirma ausência de problemas.", props.Text{Size: 9})
		}
		for _, finding := range document.Snapshot.Findings {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			addText(label(finding.Category)+" · "+finding.Title, props.Text{Size: 10, Top: 4})
			addText(finding.Description, props.Text{Size: 9})
			addText(fmt.Sprintf("Severidade: %s · Confiança: %.0f%% · Ação: %s", label(finding.Severity), finding.Confidence*100, label(finding.RecommendedAction)), props.Text{Size: 8})
		}
	}

	generated, err := documentMaker.Generate()
	if err != nil {
		return nil, fmt.Errorf("report PDF: generate: %w", err)
	}
	data := generated.GetBytes()
	if len(data) == 0 || int64(len(data)) > maxPDFBytesLimit || !bytes.HasPrefix(data, []byte("%PDF-")) {
		return nil, fmt.Errorf("report PDF: invalid or oversized PDF")
	}
	if pages := PageCount(data); pages == 0 || pages > maxPages {
		return nil, fmt.Errorf("report PDF: invalid page count")
	}
	return data, nil
}

func reportVerificationURL(origin, code string, version int) string {
	return strings.TrimRight(origin, "/") + "/verify-report?code=" + url.QueryEscape(code) + "&version=" + fmt.Sprint(version)
}

// UniqueImageCount counts distinct images that must be embedded; evidence
// marked missing never carries an image.
func UniqueImageCount(items []core.Evidence) int {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if core.EvidenceAvailable(item.Availability) {
			seen[item.ID] = struct{}{}
		}
	}
	return len(seen)
}

// PageCount returns the number of page objects in a generated PDF.
func PageCount(data []byte) int { return len(pdfPageMarker.FindAll(data, -1)) }

const (
	maxHeaderRunes = 160
	fullWidthChunk = 1200
	// captionChunk is the v3 caption split; v1/v2 keep legacyCaptionChunk.
	captionChunk       = 45
	legacyCaptionChunk = 500
)

func ellipsis(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}

// chunkText splits text on paragraph and word boundaries so no single row can
// exceed a page, because Maroto cannot split a row across pages.
func chunkText(value string, limit int) []string {
	var chunks []string
	for _, paragraph := range strings.Split(strings.TrimSpace(value), "\n") {
		var current []rune
		flush := func() {
			if len(current) > 0 {
				chunks = append(chunks, string(current))
				current = nil
			}
		}
		for _, word := range strings.Fields(paragraph) {
			runes := []rune(word)
			for len(runes) > limit {
				flush()
				chunks = append(chunks, string(runes[:limit]))
				runes = runes[limit:]
			}
			if len(current) > 0 && len(current)+1+len(runes) > limit {
				flush()
			}
			if len(current) > 0 {
				current = append(current, ' ')
			}
			current = append(current, runes...)
		}
		flush()
	}
	return chunks
}

// textRows emits one auto-height row per chunk, applying the top spacing to the
// first chunk and the bottom spacing to the last.
func textRows(value string, prop props.Text, limit int) []marotocore.Row {
	chunks := chunkText(value, limit)
	rows := make([]marotocore.Row, 0, len(chunks))
	for index, chunk := range chunks {
		chunkProp := prop
		if index > 0 {
			chunkProp.Top = 0
		}
		if index < len(chunks)-1 {
			chunkProp.Bottom = 0
		}
		rows = append(rows, text.NewAutoRow(chunk, chunkProp))
	}
	return rows
}

func scopeStatementLines(statements []string) []string {
	lines := make([]string, len(statements))
	for index, statement := range statements {
		lines[index] = "• " + statement
	}
	return lines
}

// pdfPresentation selects the presentation rules of one document: report v3
// uses the shared core vocabulary, E### references, the evidence index, the
// image placeholder and the 45-rune caption split; v1/v2 render as before.
type pdfPresentation struct{ v3 bool }

func presentationFor(snapshot core.Snapshot) pdfPresentation {
	return pdfPresentation{v3: snapshot.SchemaVersion >= 3}
}

// legacyPDF is the historical presentation used by v1/v2 documents.
var legacyPDF = pdfPresentation{}

func (p pdfPresentation) label(value string) string {
	if p.v3 {
		return core.CodeLabel(value)
	}
	return label(value)
}

func (p pdfPresentation) captionChunk() int {
	if p.v3 {
		return captionChunk
	}
	return legacyCaptionChunk
}

func analysisSummary(requirement core.Requirement) string {
	return legacyPDF.analysisSummary(requirement)
}

func (p pdfPresentation) analysisSummary(requirement core.Requirement) string {
	label := p.label
	summary := label(requirement.AnalysisMode) + " · " + label(requirement.AnalysisStatus)
	if requirement.AnalysisMode == "COMPARE_ORIGIN_CURRENT" && requirement.NoRelevantChange != nil {
		if *requirement.NoRelevantChange {
			summary += " · Sem alteração relevante identificada"
		} else {
			summary += " · Alteração relevante identificada"
		}
	}
	if requirement.Coverage != "" {
		summary += " · " + requirement.Coverage
	}
	return summary
}

// keptHeading holds the text rows that introduce an evidence block together
// with an estimate of their height, so they can be placed on a page only when
// the first image row that follows fits there too (PDF-KEEP). Maroto cannot
// split a row but breaks pages between rows; without this the title and the
// limitation of an evidence could end one page while its image starts the next.
type keptHeading struct {
	rows []marotocore.Row
	mm   float64
}

// add appends a text block (chunked like every other PDF text) to the heading.
func (h *keptHeading) add(value string, prop props.Text) {
	prop.Family = "Roboto"
	h.rows = append(h.rows, textRows(value, prop, fullWidthChunk)...)
	h.mm += estimateTextMM(value, prop)
}

// flush emits the heading, moving it to a new page first when it plus the
// following followMM millimetres of content do not fit on the current one.
func (h *keptHeading) flush(documentMaker marotocore.Maroto, followMM float64) {
	if len(h.rows) == 0 {
		return
	}
	keepTogether(documentMaker, h.mm+followMM)
	documentMaker.AddRows(h.rows...)
	h.rows, h.mm = nil, 0
}

// keepTogether starts a new page when a block of needMM millimetres does not
// fit in the space left on the current one. It never emits a blank page: at the
// top of a page the call is a no-op, so a block taller than a page still flows.
// Maroto's own check also counts the page header twice, so the decision errs on
// the side of an early break, never of a split block.
func keepTogether(documentMaker marotocore.Maroto, needMM float64) {
	if !documentMaker.FitlnCurrentPage(needMM) {
		documentMaker.AddPages(page.New())
	}
}

const (
	pdfTextWidthMM = 180.0
	ptToMM         = 25.4 / 72
	// averageGlyphEm overestimates the Roboto average advance so that wrapped
	// lines are never undercounted.
	averageGlyphEm = 0.6
)

// estimateTextMM bounds, from above, the height Maroto gives the rows built by
// textRows for value: one font-size line per wrapped line plus the spacing.
func estimateTextMM(value string, prop props.Text) float64 {
	size := prop.Size
	if size <= 0 {
		size = 10
	}
	perLine := max(1, int(pdfTextWidthMM/(size*ptToMM*averageGlyphEm)))
	lines := 0
	for _, chunk := range chunkText(value, fullWidthChunk) {
		lines += (len([]rune(chunk)) + perLine - 1) / perLine
	}
	if lines == 0 {
		return 0
	}
	return float64(lines)*size*ptToMM + prop.Top + prop.Bottom
}

// addEvidenceRows lays out reference photos in the left column and current
// photos in the right one; without references, photos fill two columns. The
// optional lead heading is emitted on the same page as the first image row, and
// in the multi-chunk caption layout every image stays with the start of its
// caption.
func (p pdfPresentation) addEvidenceRows(ctx context.Context, documentMaker marotocore.Maroto, lead *keptHeading, items []core.Evidence, assets pdf.AssetBundle, evidenceIndex map[string]string, componentLabel, snapshotID, dashboardOrigin string) error {
	captionChunk := p.captionChunk()
	if lead == nil {
		lead = &keptHeading{}
	}
	var references, others []core.Evidence
	for _, item := range items {
		if item.Role == "REFERENCE" {
			references = append(references, item)
		} else {
			others = append(others, item)
		}
	}
	var left, right []core.Evidence
	if len(references) > 0 {
		left, right = references, others
		lead.rows = append(lead.rows, row.New().Add(
			col.New(6).Add(text.New("Referência", props.Text{Size: 8, Family: "Roboto", Bottom: 1})),
			col.New(6).Add(text.New("Vistoria atual", props.Text{Size: 8, Family: "Roboto", Bottom: 1})),
		))
		lead.mm += 8*ptToMM + 1
	} else {
		for index, item := range others {
			if index%2 == 0 {
				left = append(left, item)
			} else {
				right = append(right, item)
			}
		}
	}
	for index := 0; index < len(left) || index < len(right); index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		leftImage, leftCaption, leftLink := col.New(6), "", ""
		rightImage, rightCaption, rightLink := col.New(6), "", ""
		var err error
		if index < len(left) {
			if leftImage, leftCaption, leftLink, err = p.evidenceComponents(left[index], assets, componentLabel, snapshotID, dashboardOrigin); err != nil {
				return err
			}
			if reference := evidenceIndex[left[index].ID]; reference != "" {
				leftCaption = reference + " · " + leftCaption
			}
		}
		if index < len(right) {
			if rightImage, rightCaption, rightLink, err = p.evidenceComponents(right[index], assets, componentLabel, snapshotID, dashboardOrigin); err != nil {
				return err
			}
			if reference := evidenceIndex[right[index].ID]; reference != "" {
				rightCaption = reference + " · " + rightCaption
			}
		}
		if len([]rune(leftCaption)) <= captionChunk && len([]rune(rightCaption)) <= captionChunk {
			// Image and caption share one row so a page break cannot separate them.
			lines := max(captionLines(leftCaption)+linkLineCount(leftLink), captionLines(rightCaption)+linkLineCount(rightLink))
			rowMM := imageHeightMM + 2 + float64(lines)*captionLineMM
			lead.flush(documentMaker, rowMM)
			if leftCaption != "" {
				leftImage.Add(text.New(leftCaption, props.Text{Size: 8, Family: "Roboto", Top: imageHeightMM + 1}))
			}
			if leftLink != "" {
				addLinkText(leftImage, leftLink, imageHeightMM+1+float64(captionLines(leftCaption))*captionLineMM)
			}
			if rightCaption != "" {
				rightImage.Add(text.New(rightCaption, props.Text{Size: 8, Family: "Roboto", Top: imageHeightMM + 1}))
			}
			if rightLink != "" {
				addLinkText(rightImage, rightLink, imageHeightMM+1+float64(captionLines(rightCaption))*captionLineMM)
			}
			documentMaker.AddRows(row.New(rowMM).Add(leftImage, rightImage))
		} else {
			// The image keeps company with the first caption chunk.
			firstChunkMM := float64(max(captionLines(firstChunk(leftCaption, captionChunk)), captionLines(firstChunk(rightCaption, captionChunk)))) * captionLineMM
			if len(lead.rows) > 0 {
				lead.flush(documentMaker, imageHeightMM+1+firstChunkMM)
			} else {
				keepTogether(documentMaker, imageHeightMM+1+firstChunkMM)
			}
			documentMaker.AddRows(row.New(imageHeightMM).Add(leftImage, rightImage))
			leftChunks, rightChunks := chunkText(leftCaption, captionChunk), chunkText(rightCaption, captionChunk)
			for line := 0; line < len(leftChunks) || line < len(rightChunks); line++ {
				leftColumn, rightColumn := col.New(6), col.New(6)
				if line < len(leftChunks) {
					leftColumn.Add(text.New(leftChunks[line], props.Text{Size: 8, Family: "Roboto", Top: 1}))
				}
				if line < len(rightChunks) {
					rightColumn.Add(text.New(rightChunks[line], props.Text{Size: 8, Family: "Roboto", Top: 1}))
				}
				documentMaker.AddRows(row.New().Add(leftColumn, rightColumn))
			}
			if leftLink != "" || rightLink != "" {
				linkLeft, linkRight := col.New(6), col.New(6)
				if leftLink != "" {
					addLinkText(linkLeft, leftLink, 0)
				}
				if rightLink != "" {
					addLinkText(linkRight, rightLink, 0)
				}
				documentMaker.AddRows(row.New(float64(max(linkLineCount(leftLink), linkLineCount(rightLink)))*captionLineMM).Add(linkLeft, linkRight))
			}
		}
		documentMaker.AddRows(row.New(3))
	}
	return nil
}

const (
	imageHeightMM = 56.0
	captionLineMM = 3.8
	// captionCharsPerLine is deliberately low so the reserved height is never short.
	captionCharsPerLine = 45
)

// firstChunk returns the first piece of a caption split by chunkText.
func firstChunk(caption string, limit int) string {
	if chunks := chunkText(caption, limit); len(chunks) > 0 {
		return chunks[0]
	}
	return ""
}

func captionLines(caption string) int {
	if caption == "" {
		return 0
	}
	return (len([]rune(caption)) + captionCharsPerLine - 1) / captionCharsPerLine
}

func linkLineCount(link string) int {
	if link == "" {
		return 0
	}
	return len(linkTextLines(link))
}

func addLinkText(column marotocore.Col, link string, top float64) {
	for index, chunk := range linkTextLines(link) {
		target := link
		column.Add(text.New(chunk, props.Text{Size: 7, Family: "Roboto", Top: top + float64(index)*captionLineMM, Hyperlink: &target}))
	}
}

func linkTextLines(link string) []string {
	path, query, hasQuery := strings.Cut(link, "?")
	segments := strings.Split(path, "/")
	var result []string
	current := ""
	flush := func() {
		if current != "" {
			result = append(result, current)
			current = ""
		}
	}
	for index, segment := range segments {
		part := segment
		if index < len(segments)-1 {
			part += "/"
		}
		if current != "" && len([]rune(current+part)) > captionCharsPerLine {
			flush()
		}
		if len([]rune(part)) > captionCharsPerLine {
			flush()
			result = append(result, chunkText(part, captionCharsPerLine)...)
			continue
		}
		current += part
	}
	flush()
	if hasQuery {
		result = append(result, chunkText("?"+query, captionCharsPerLine)...)
	}
	return result
}

// evidenceComponents builds the image column and caption of one evidence with
// the historical (v1/v2) presentation.
func evidenceComponents(item core.Evidence, assets pdf.AssetBundle, componentLabel, snapshotID, dashboardOrigin string) (marotocore.Col, string, string, error) {
	return legacyPDF.evidenceComponents(item, assets, componentLabel, snapshotID, dashboardOrigin)
}

func (p pdfPresentation) evidenceComponents(item core.Evidence, assets pdf.AssetBundle, componentLabel, snapshotID, dashboardOrigin string) (marotocore.Col, string, string, error) {
	label := p.label
	imageColumn := col.New(6)
	data := assets["evidence-"+item.ID+".jpg"]
	if !p.v3 && (len(data) == 0 || !core.EvidenceAvailable(item.Availability)) {
		imageColumn.Add(text.New("Imagem indisponível", props.Text{Size: 8, Family: "Roboto", Top: 2}))
	} else if len(data) == 0 || !core.EvidenceAvailable(item.Availability) {
		notice := "Imagem indisponível"
		if item.Availability == core.EvidenceWithheld {
			notice = "Imagem não exibida nesta versão"
		}
		// A neutral frame takes the place of the photo so the caption below is
		// still attached to a visual block, and the notice is drawn inside it.
		const placeholderWidthMM, columnWidthMM = imageHeightMM * 4 / 3, 90.0
		imageColumn.Add(
			marotoimage.NewFromBytes(unavailableImagePlaceholder(), extension.Png, props.Rect{Percent: placeholderWidthMM / columnWidthMM * 100, JustReferenceWidth: true}),
			text.New(notice, props.Text{Size: 8, Family: "Roboto", Top: imageHeightMM/2 - 2, Left: 4}),
		)
	} else {
		dimensions, format, err := stdimage.DecodeConfig(bytes.NewReader(data))
		if err != nil || dimensions.Width <= 0 || dimensions.Height <= 0 {
			return nil, "", "", fmt.Errorf("report PDF: invalid evidence image")
		}
		imageExtension := extension.Jpg
		switch format {
		case "jpeg":
		case "png":
			imageExtension = extension.Png
		default:
			return nil, "", "", fmt.Errorf("report PDF: unsupported evidence image format %q", format)
		}
		const maxWidthMM, maxHeightMM, columnWidthMM = 84.0, imageHeightMM, 90.0
		aspect := float64(dimensions.Height) / float64(dimensions.Width)
		widthMM := min(maxWidthMM, maxHeightMM/aspect)
		percent := widthMM / columnWidthMM * 100
		imageColumn.Add(marotoimage.NewFromBytes(data, imageExtension, props.Rect{Percent: percent, JustReferenceWidth: true}))
	}
	caption := label(item.Role)
	if item.Description != "" {
		caption += " · " + item.Description
	}
	if item.CapturedAt != "" {
		caption += " · " + item.CapturedAt
	}
	if item.EvidenceKind == "VIDEO_FRAME" && (item.SourceAvailability == "EXPIRED" || item.SourceAvailability == "MISSING") {
		caption += " · Vídeo original indisponível; frame preservado"
	}
	link := ""
	if item.EvidenceKind == "VIDEO_FRAME" {
		caption = p.videoFrameCaption(item, componentLabel, caption)
		if dashboardOrigin != "" && snapshotID != "" {
			link = dashboardOrigin + "/media/video-evidence/" + url.PathEscape(item.FrameID) + "?snapshotId=" + url.QueryEscape(snapshotID)
		}
	}
	for _, flag := range item.Flags {
		caption += " · " + label(flag)
	}
	return imageColumn, caption, link, nil
}

var (
	placeholderOnce  sync.Once
	placeholderBytes []byte
)

// unavailableImagePlaceholder returns a neutral 4:3 PNG frame that carries no
// evidence data; it stands in for any photo whose pixels cannot be shown.
func unavailableImagePlaceholder() []byte {
	placeholderOnce.Do(func() {
		const width, height, border = 320, 240, 3
		canvas := stdimage.NewRGBA(stdimage.Rect(0, 0, width, height))
		fill, edge := color.RGBA{R: 0xF1, G: 0xF1, B: 0xF1, A: 0xFF}, color.RGBA{R: 0xB5, G: 0xB5, B: 0xB5, A: 0xFF}
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				if x < border || y < border || x >= width-border || y >= height-border {
					canvas.Set(x, y, edge)
				} else {
					canvas.Set(x, y, fill)
				}
			}
		}
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, canvas); err == nil {
			placeholderBytes = encoded.Bytes()
		}
	})
	return placeholderBytes
}

func (p pdfPresentation) videoFrameCaption(item core.Evidence, componentLabel, caption string) string {
	if p.v3 {
		caption = "Frame " + item.FrameID + " · " + caption
	}
	if strings.TrimSpace(componentLabel) != "" {
		caption = strings.TrimSpace(componentLabel) + " · " + caption
	}
	if item.TimestampMS != nil {
		seconds := *item.TimestampMS / 1000
		caption += fmt.Sprintf(" · %02d:%02d", seconds/60, seconds%60)
	}
	if !p.v3 {
		caption += " · Frame " + item.FrameID
	}
	caption += " · Fonte " + item.SourceMediaID
	return caption
}

func abbreviatedID(value string) string {
	if len(value) > 8 {
		return value[:8]
	}
	return value
}

func classificationLabel(value string) string {
	return map[string]string{"NORMAL": "Sem alertas identificados", "ATTENTION": "Requer atenção", "CRITICAL": "Crítica"}[value]
}

// label presents enum values of v1/v2 PDFs exactly as they were issued;
// report v3 uses core.CodeLabel. Classification reason codes are not here:
// core.ReasonLabel is their single vocabulary, shared with the HTML report
// (INCONCLUSIVE below is the analysis status, not a reason).
func label(value string) string {
	labels := map[string]string{
		"INCONCLUSIVE": "Inconclusiva", "REFERENCE": "Referência", "CURRENT": "Vistoria atual", "CONSERVATION": "Conservação", "INVENTORY": "Inventário", "CLEANLINESS": "Limpeza", "OBSTRUCTION": "Obstrução", "EVIDENCE_QUALITY": "Qualidade da evidência",
		"CURRENT_ONLY": "Análise atual", "COMPARE_ORIGIN_CURRENT": "Comparação com referência", "PENDING": "Pendente", "COMPLETED": "Concluída", "FAILED": "Falha técnica", "GPS_MISSING": "Localização ausente", "LOW_QUALITY": "Qualidade insuficiente", "SENSITIVE_DETECTION": "Detecção sensível", "CONSOLIDATED": "Consolidado", "HISTORICAL": "Histórico",
		"NONE": "Insuficiente", "LOW": "Baixa", "MEDIUM": "Média", "HIGH": "Alta", "CRITICAL": "Crítica", "REVIEW": "Revisar evidência", "RECOVER": "Solicitar complemento", "NO_ACTION": "Nenhuma ação adicional",
		"MISSING": "Item ausente", "CONDITION_CHANGED": "Condição alterada", "PRESENT_UNCHANGED": "Presente sem alteração relevante", "NOT_OBSERVED": "Não observado", "OUTSIDE_INVENTORY": "Fora do inventário", "IMPROVED": "Condição melhorada", "ENVIRONMENT_NOT_CAPTURED": "Ambiente não capturado", "NO_BASIS": "Sem base para comparar",
		"NEW": "Novo", "GOOD": "Bom", "FAIR": "Regular", "POOR": "Ruim", "NOT_ASSESSABLE": "Não avaliável",
		"MOVE_IN": "Entrada no imóvel", "CONSTRUCTION_HANDOVER": "Entrega da obra", "POST_RENOVATION": "Pós-reforma", "INVENTORY_UPDATE": "Atualização do inventário", "CONTESTATION_ADJUSTMENT": "Ajuste de contestação",
	}
	if translated, ok := labels[value]; ok {
		return translated
	}
	return strings.ReplaceAll(strings.ToLower(value), "_", " ")
}

// inventorySummaryLines opens the inventory section: the reference, the
// coverage of a comparative report and, without problems, the all-clear line.
func inventorySummaryLines(inventory *core.InventorySection) []string {
	return legacyPDF.inventorySummaryLines(inventory)
}

func (p pdfPresentation) inventorySummaryLines(inventory *core.InventorySection) []string {
	lines := []string{"Inventário catalogado nesta vistoria"}
	if inventory.Reference.Purpose != "" {
		lines[0] = "Referência: " + p.label(inventory.Reference.Purpose)
	}
	if inventory.Coverage != nil {
		lines = append(lines, fmt.Sprintf("%d de %d itens avaliados · %d não observados", inventory.Coverage.AssessedItems, inventory.Coverage.ExpectedItems, inventory.Coverage.NotObserved))
		if len(inventory.Problems) == 0 {
			lines = append(lines, "Inventário sem alertas.")
		}
	}
	return lines
}

// inventoryProblemHeading names the section of one problem; missing items are
// grouped under "Itens ausentes".
func inventoryProblemHeading(item core.InventoryEntry) string {
	return legacyPDF.inventoryProblemHeading(item)
}

func (p pdfPresentation) inventoryProblemHeading(item core.InventoryEntry) string {
	if p.v3 {
		// Report v3 names every item outcome with the comparison vocabulary.
		return core.InventoryStatusLabel(item.Status) + " · " + item.Environment + " · " + item.Name
	}
	section := p.label(item.Status)
	if item.Status == "MISSING" {
		section = "Itens ausentes"
	}
	return section + " · " + item.Environment + " · " + item.Name
}
