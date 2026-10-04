package render_pdf

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	stdimage "image"
	_ "image/jpeg"
	_ "image/png"
	"regexp"
	"sort"
	"strings"

	"inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/pdf"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	marotoimage "github.com/johnfercher/maroto/v2/pkg/components/image"
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
	MaxEvidence   int
	MaxPages      int
	MaxImageBytes int64
	MaxPDFBytes   int64
}

func (r MarotoRenderer) Render(ctx context.Context, document PDFDocument, assets pdf.AssetBundle) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(document.SnapshotID) == "" || document.Version <= 0 || core.Validate(document.Snapshot) != nil {
		return nil, fmt.Errorf("report PDF: invalid document")
	}
	if !document.Internal && len(document.Snapshot.Findings) > 0 {
		return nil, fmt.Errorf("report PDF: customer document contains internal findings")
	}
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
		text.NewAutoRow("Classificação: "+classificationLabel(document.Snapshot.Classification), props.Text{Size: 9, Family: "Roboto", Bottom: 2}),
		row.New(3),
	); err != nil {
		return nil, fmt.Errorf("report PDF: register header: %w", err)
	}
	if err := documentMaker.RegisterFooter(text.NewAutoRow(ellipsis(fmt.Sprintf("Laudo %s · versão %d", document.SnapshotID, document.Version), maxHeaderRunes), props.Text{Size: 8, Family: "Roboto"})); err != nil {
		return nil, fmt.Errorf("report PDF: register footer: %w", err)
	}

	addText := func(value string, prop props.Text) {
		prop.Family = "Roboto"
		documentMaker.AddRows(textRows(value, prop, fullWidthChunk)...)
	}
	addText("Classificação: "+classificationLabel(document.Snapshot.Classification)+" · "+label(document.Snapshot.Mode), props.Text{Size: 13, Top: 4, Bottom: 2})
	addText("Imóvel: "+asset.Name+" · "+asset.Address, props.Text{Size: 9, Bottom: 2})
	addText(fmt.Sprintf("Responsável: %s · Modelo: %s v%d", document.Snapshot.Context.Participant.Name, document.Snapshot.Context.Template.Name, document.Snapshot.Context.Template.Version), props.Text{Size: 9, Bottom: 2})
	addText(document.Snapshot.Advisory, props.Text{Size: 9, Bottom: 4})
	addText("Motivos", props.Text{Size: 11, Top: 2})
	if len(document.Snapshot.ReasonCodes) == 0 {
		addText("Nenhum motivo adicional registrado.", props.Text{Size: 9})
	} else {
		for _, reason := range document.Snapshot.ReasonCodes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			addText("• "+label(reason), props.Text{Size: 9})
		}
	}

	addText("Evidências", props.Text{Size: 11, Top: 6})
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
		addText(requirement.Section+" · "+requirement.Label, props.Text{Size: 10, Top: 4})
		addText(requirement.Instructions, props.Text{Size: 8})
		addText(analysisSummary(requirement), props.Text{Size: 8, Bottom: 3})
		items := evidenceByRequirement[requirement.Key]
		if len(items) == 0 {
			addText("Nenhuma evidência registrada para este requisito.", props.Text{Size: 8})
			continue
		}
		if err := addEvidenceRows(ctx, documentMaker, items, assets); err != nil {
			return nil, err
		}
	}
	extraKeys := make([]string, 0, len(evidenceByRequirement))
	for key := range evidenceByRequirement {
		if !seenRequirements[key] {
			extraKeys = append(extraKeys, key)
		}
	}
	sort.Strings(extraKeys)
	for _, key := range extraKeys {
		addText("Evidências adicionais", props.Text{Size: 10, Top: 4})
		if err := addEvidenceRows(ctx, documentMaker, evidenceByRequirement[key], assets); err != nil {
			return nil, err
		}
	}
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

// UniqueImageCount counts distinct images that must be embedded; evidence
// marked missing never carries an image.
func UniqueImageCount(items []core.Evidence) int {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.Availability != "MISSING" {
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
	captionChunk   = 500
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

func analysisSummary(requirement core.Requirement) string {
	summary := label(requirement.AnalysisMode) + " · " + label(requirement.AnalysisStatus)
	if requirement.AnalysisMode == "COMPARE_ORIGIN_CURRENT" && requirement.NoRelevantChange != nil {
		if *requirement.NoRelevantChange {
			summary += " · Sem alteração relevante identificada"
		} else {
			summary += " · Alteração relevante identificada"
		}
	}
	return summary
}

// addEvidenceRows lays out reference photos in the left column and current
// photos in the right one; without references, photos fill two columns.
func addEvidenceRows(ctx context.Context, documentMaker marotocore.Maroto, items []core.Evidence, assets pdf.AssetBundle) error {
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
		documentMaker.AddRows(row.New().Add(
			col.New(6).Add(text.New("Referência", props.Text{Size: 8, Family: "Roboto", Bottom: 1})),
			col.New(6).Add(text.New("Vistoria atual", props.Text{Size: 8, Family: "Roboto", Bottom: 1})),
		))
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
		leftImage, leftCaption := col.New(6), ""
		rightImage, rightCaption := col.New(6), ""
		var err error
		if index < len(left) {
			if leftImage, leftCaption, err = evidenceComponents(left[index], assets); err != nil {
				return err
			}
		}
		if index < len(right) {
			if rightImage, rightCaption, err = evidenceComponents(right[index], assets); err != nil {
				return err
			}
		}
		if len([]rune(leftCaption)) <= captionChunk && len([]rune(rightCaption)) <= captionChunk {
			// Image and caption share one row so a page break cannot separate them.
			lines := max(captionLines(leftCaption), captionLines(rightCaption))
			if leftCaption != "" {
				leftImage.Add(text.New(leftCaption, props.Text{Size: 8, Family: "Roboto", Top: imageHeightMM + 1}))
			}
			if rightCaption != "" {
				rightImage.Add(text.New(rightCaption, props.Text{Size: 8, Family: "Roboto", Top: imageHeightMM + 1}))
			}
			documentMaker.AddRows(row.New(imageHeightMM+2+float64(lines)*captionLineMM).Add(leftImage, rightImage))
		} else {
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

func captionLines(caption string) int {
	if caption == "" {
		return 0
	}
	return (len([]rune(caption)) + captionCharsPerLine - 1) / captionCharsPerLine
}

func evidenceComponents(item core.Evidence, assets pdf.AssetBundle) (marotocore.Col, string, error) {
	imageColumn := col.New(6)
	data := assets["evidence-"+item.ID+".jpg"]
	if len(data) == 0 || item.Availability == "MISSING" {
		imageColumn.Add(text.New("Imagem indisponível", props.Text{Size: 8, Family: "Roboto", Top: 2}))
	} else {
		dimensions, format, err := stdimage.DecodeConfig(bytes.NewReader(data))
		if err != nil || dimensions.Width <= 0 || dimensions.Height <= 0 {
			return nil, "", fmt.Errorf("report PDF: invalid evidence image")
		}
		imageExtension := extension.Jpg
		switch format {
		case "jpeg":
		case "png":
			imageExtension = extension.Png
		default:
			return nil, "", fmt.Errorf("report PDF: unsupported evidence image format %q", format)
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
	for _, flag := range item.Flags {
		caption += " · " + label(flag)
	}
	return imageColumn, caption, nil
}

func classificationLabel(value string) string {
	return map[string]string{"NORMAL": "Sem alertas identificados", "ATTENTION": "Requer atenção", "CRITICAL": "Crítica"}[value]
}

func label(value string) string {
	labels := map[string]string{
		"MISSING_EVIDENCE": "Evidência ausente", "QUALITY_LOW": "Qualidade insuficiente", "INCONCLUSIVE": "Inconclusiva", "ANALYSIS_FAILED": "Falha técnica na análise", "OBSERVED_CHANGE": "Mudança observada", "CRITICAL_FINDING": "Constatação crítica",
		"REFERENCE": "Referência", "CURRENT": "Vistoria atual", "CONSERVATION": "Conservação", "INVENTORY": "Inventário", "CLEANLINESS": "Limpeza", "OBSTRUCTION": "Obstrução", "EVIDENCE_QUALITY": "Qualidade da evidência",
		"CURRENT_ONLY": "Análise atual", "COMPARE_ORIGIN_CURRENT": "Comparação com referência", "PENDING": "Pendente", "COMPLETED": "Concluída", "FAILED": "Falha técnica", "GPS_MISSING": "Localização ausente", "LOW_QUALITY": "Qualidade insuficiente", "SENSITIVE_DETECTION": "Detecção sensível", "CONSOLIDATED": "Consolidado", "HISTORICAL": "Histórico", "CLASSIFICATION_CRITICAL": "Classificação crítica",
		"NONE": "Insuficiente", "LOW": "Baixa", "MEDIUM": "Média", "HIGH": "Alta", "CRITICAL": "Crítica", "REVIEW": "Revisar evidência", "RECOVER": "Solicitar complemento", "NO_ACTION": "Nenhuma ação adicional",
	}
	if translated, ok := labels[value]; ok {
		return translated
	}
	return strings.ReplaceAll(strings.ToLower(value), "_", " ")
}
