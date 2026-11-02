package render_pdf

// Test helpers shared by the unit and integration PDF layout tests: they parse
// the real PDF content streams with pdfcpu (page by page) so pagination can be
// verified on the generated document, not on the layout model.

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/pdf"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	pdfcpu "github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

var pdfImageMatrix = regexp.MustCompile(`(?m)([-+0-9.]+)\s+0\s+0\s+([-+0-9.]+)\s+([-+0-9.]+)\s+([-+0-9.]+)\s+cm\s*/I[^\s]+\s+Do`)
var pdfTextBlock = regexp.MustCompile(`(?s)BT(.*?)ET`)
var pdfTextFont = regexp.MustCompile(`/[^\s]+\s+([-+0-9.]+)\s+Tf`)
var pdfTextMatrix = regexp.MustCompile(`([-+0-9.]+)\s+([-+0-9.]+)\s+([-+0-9.]+)\s+([-+0-9.]+)\s+([-+0-9.]+)\s+([-+0-9.]+)\s+Tm`)
var pdfTextDelta = regexp.MustCompile(`([-+0-9.]+)\s+([-+0-9.]+)\s+Td`)
var pdfArrayString = regexp.MustCompile(`\((?:\\.|[^\\)])*\)|<[0-9A-Fa-f\s]+>`)
var pdfTextShow = regexp.MustCompile(`(?s)(\((?:\\.|[^\\)])*\)|<[0-9A-Fa-f\s]+>|\[[^\]]*\])\s*T(?:j|J)`)

type vvTask07PDFImage struct{ x, y, width, height float64 }
type vvTask07PDFText struct {
	x, y, fontSize, estimatedWidth float64
	encoded                        string
	// text is the decoded content shown by the operator (UTF-16BE for the
	// embedded UTF-8 fonts used by the renderer).
	text string
}
type vvTask07PDFPage struct {
	images []vvTask07PDFImage
	texts  []vvTask07PDFText
}

// PDF margins are 15 mm on three sides and 18 mm at the bottom in maroto.go.
const (
	pdfLeftRightMarginPt   = 15 * 72 / 25.4
	pdfTopMarginPt         = 15 * 72 / 25.4
	pdfBottomMarginPt      = 18 * 72 / 25.4
	minPDFEvidenceImagePt  = 56 * 72 / 25.4 // renderer evidence height is 56 mm
	minPDFReadableFontPt   = 7.0
	captionBelowImageMaxPt = 18.0 // 8 pt caption plus row spacing and line spacing
	// captionBlockBelowImagePt bounds the wrapped caption under an image: two
	// 8 pt lines, the second carrying the frame ID.
	captionBlockBelowImagePt = 26.0
)

func assertVVTask07PDFGeometry(t *testing.T, data []byte, wantImages int) []vvTask07PDFPage {
	t.Helper()
	model.ConfigPath = "disable"
	conf := model.NewDefaultConfiguration()
	ctx, err := api.ReadAndValidate(bytes.NewReader(data), conf)
	if err != nil {
		t.Fatalf("read rendered PDF with pdfcpu: %v", err)
	}
	dimensions, err := api.PageDims(bytes.NewReader(data), conf)
	if err != nil {
		t.Fatalf("extract PDF page dimensions: %v", err)
	}
	if len(dimensions) != PageCount(data) {
		t.Fatalf("pdfcpu returned %d page dimensions for %d pages", len(dimensions), PageCount(data))
	}
	images := 0
	textRuns := 0
	pages := make([]vvTask07PDFPage, len(dimensions))
	for pageIndex, page := range dimensions {
		content, err := pdfcpu.ExtractPageContent(ctx, pageIndex+1)
		if err != nil {
			t.Fatalf("extract PDF page %d content: %v", pageIndex+1, err)
		}
		stream, err := io.ReadAll(content)
		if err != nil {
			t.Fatalf("read PDF page %d content: %v", pageIndex+1, err)
		}
		matches := pdfImageMatrix.FindAllSubmatch(stream, -1)
		pageImages := 0
		var pageImageGeometry []vvTask07PDFImage
		for _, match := range matches {
			values := make([]float64, 4)
			for index, raw := range match[1:] {
				values[index], err = strconv.ParseFloat(string(raw), 64)
				if err != nil {
					t.Fatalf("parse image transform %q: %v", raw, err)
				}
			}
			width, height, x, y := values[0], values[1], values[2], values[3]
			if height < minPDFEvidenceImagePt-0.5 || x < pdfLeftRightMarginPt-0.5 || y < pdfBottomMarginPt-0.5 || x+width > page.Width-pdfLeftRightMarginPt+0.5 || y+height > page.Height-pdfTopMarginPt+0.5 {
				t.Errorf("page %d image bounds x=%.2f y=%.2f w=%.2f h=%.2f exceed page/margins %.2fx%.2f", pageIndex+1, x, y, width, height, page.Width, page.Height)
			}
			pageImageGeometry = append(pageImageGeometry, vvTask07PDFImage{x: x, y: y, width: width, height: height})
			pageImages++
		}
		pageTexts := extractVVTask07PDFText(t, pageIndex+1, stream)
		textRuns += len(pageTexts)
		for _, text := range pageTexts {
			if text.fontSize < minPDFReadableFontPt {
				t.Errorf("page %d text uses %.2f pt font, below %.1f pt minimum: %q", pageIndex+1, text.fontSize, minPDFReadableFontPt, text.text)
			}
			if text.x < pdfLeftRightMarginPt-0.5 || text.x+text.estimatedWidth > page.Width-pdfLeftRightMarginPt+0.5 || text.y+text.fontSize < pdfBottomMarginPt-0.5 || text.y+text.fontSize > page.Height-pdfTopMarginPt+0.5 {
				t.Errorf("page %d text block x=%.2f..%.2f y=%.2f exceeds margins", pageIndex+1, text.x, text.x+text.estimatedWidth, text.y)
			}
		}
		pages[pageIndex] = vvTask07PDFPage{images: pageImageGeometry, texts: pageTexts}
		images += pageImages
		if pageImages > 0 {
			if len(pageTexts) == 0 {
				t.Errorf("page %d contains image evidence without a text caption block; page break may have split the row", pageIndex+1)
			}
			for _, image := range pageImageGeometry {
				captionBelow := false
				for _, text := range pageTexts {
					gap := image.y - text.y
					if gap >= 0 && gap <= captionBelowImageMaxPt && text.x >= image.x-1 && text.x <= image.x+image.width+1 {
						captionBelow = true
						break
					}
				}
				if !captionBelow {
					t.Errorf("page %d image at x=%.2f y=%.2f has no caption text operator immediately below it within %.0f pt", pageIndex+1, image.x, image.y, captionBelowImageMaxPt)
				}
			}
		}
	}
	if images != wantImages {
		t.Errorf("pdfcpu extracted %d positioned images, want %d", images, wantImages)
	}
	if textRuns < wantImages*2 {
		t.Errorf("pdfcpu extracted %d positioned text operators for %d images; expected captions and limitations", textRuns, wantImages)
	}
	return pages
}

// assertTask07PDFCaptionsAndLimitations groups decoded text by page and, for
// every evidence, requires an image whose caption block directly below it
// contains the evidence FrameID, while the requirement title ("Componente
// NNN") and summary ("Limitação NNN"), emitted by maroto.go before the image,
// sit above that image on the SAME page (PDF-KEEP): a page break must never
// leave the heading of an evidence on the previous page.
func assertTask07PDFCaptionsAndLimitations(t *testing.T, pages []vvTask07PDFPage, snapshot reportcore.Snapshot) {
	t.Helper()
	for index, evidence := range snapshot.Evidence {
		limitation := fmt.Sprintf("Limitação %03d", index)
		title := fmt.Sprintf("Componente %03d", index)
		pageIndex, image, found := findVVTask07CaptionedImage(pages, evidence.FrameID)
		if !found {
			t.Errorf("PDF has no image with frame %s in the caption directly below it", evidence.FrameID)
			continue
		}
		if !labelAboveVVTask07Image(pages[pageIndex], image, pdfLimitationLabel, limitation) {
			t.Errorf("page %d image for frame %s has no %q above it on the same page", pageIndex+1, evidence.FrameID, limitation)
		}
		if !labelAboveVVTask07Image(pages[pageIndex], image, pdfTitleLabel, title) {
			t.Errorf("page %d image for frame %s has no %q above it on the same page", pageIndex+1, evidence.FrameID, title)
		}
	}
}

func findVVTask07CaptionedImage(pages []vvTask07PDFPage, frameID string) (int, vvTask07PDFImage, bool) {
	for pageIndex, page := range pages {
		for _, image := range page.images {
			if strings.Contains(captionBelowVVTask07Image(page, image), frameID) {
				return pageIndex, image, true
			}
		}
	}
	return 0, vvTask07PDFImage{}, false
}

var pdfLimitationLabel = regexp.MustCompile(`Limitação \d{3}`)
var pdfTitleLabel = regexp.MustCompile(`Componente \d{3}`)

// labelAboveVVTask07Image requires the nearest text matching pattern above the
// image, on the same page, to be the expected one, so a heading belonging to
// another requirement (or one left on the previous page) cannot satisfy it.
func labelAboveVVTask07Image(page vvTask07PDFPage, image vvTask07PDFImage, pattern *regexp.Regexp, want string) bool {
	nearest, nearestY := "", math.MaxFloat64
	for _, text := range page.texts {
		if found := pattern.FindString(text.text); found != "" && text.y >= image.y+image.height && text.y < nearestY {
			nearest, nearestY = found, text.y
		}
	}
	return nearest == want
}

// captionBelowVVTask07Image joins, top to bottom and without spaces, the text
// lines that start inside the image column within the caption block below it.
func captionBelowVVTask07Image(page vvTask07PDFPage, image vvTask07PDFImage) string {
	lines := make([]vvTask07PDFText, 0, 2)
	for _, text := range page.texts {
		gap := image.y - text.y
		if gap > 0 && gap <= captionBlockBelowImagePt && text.x >= image.x-1 && text.x <= image.x+image.width+1 {
			lines = append(lines, text)
		}
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].y > lines[j].y })
	var caption strings.Builder
	for _, line := range lines {
		caption.WriteString(strings.ReplaceAll(line.text, " ", ""))
	}
	return caption.String()
}

// decodeVVTask07PDFString decodes one shown string operand: a literal, a hex
// string or a TJ array of them. Even-length payloads are read as UTF-16BE.
func decodeVVTask07PDFString(operand string) string {
	if strings.HasPrefix(operand, "[") {
		var joined strings.Builder
		for _, part := range pdfArrayString.FindAllString(operand, -1) {
			joined.WriteString(decodeVVTask07PDFString(part))
		}
		return joined.String()
	}
	var raw []byte
	switch {
	case strings.HasPrefix(operand, "<"):
		decoded, err := hex.DecodeString(strings.Join(strings.Fields(operand[1:len(operand)-1]), ""))
		if err != nil {
			return ""
		}
		raw = decoded
	case strings.HasPrefix(operand, "("):
		raw = unescapeVVTask07PDFLiteral(operand[1 : len(operand)-1])
	default:
		return operand
	}
	if len(raw)%2 != 0 {
		return string(raw)
	}
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		units = append(units, uint16(raw[i])<<8|uint16(raw[i+1]))
	}
	return string(utf16.Decode(units))
}

// unescapeVVTask07PDFLiteral resolves PDF literal string escapes.
func unescapeVVTask07PDFLiteral(literal string) []byte {
	out := make([]byte, 0, len(literal))
	for i := 0; i < len(literal); i++ {
		if literal[i] != '\\' || i+1 == len(literal) {
			out = append(out, literal[i])
			continue
		}
		i++
		switch c := literal[i]; c {
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case '\r', '\n':
			// Line continuation.
		default:
			if c >= '0' && c <= '7' {
				value, digits := 0, 0
				for ; digits < 3 && i < len(literal) && literal[i] >= '0' && literal[i] <= '7'; digits++ {
					value = value*8 + int(literal[i]-'0')
					i++
				}
				i--
				out = append(out, byte(value))
				continue
			}
			out = append(out, c)
		}
	}
	return out
}

func extractVVTask07PDFText(t *testing.T, page int, stream []byte) []vvTask07PDFText {
	t.Helper()
	var runs []vvTask07PDFText
	currentFontSize := 0.0
	for _, blockMatch := range pdfTextBlock.FindAllSubmatch(stream, -1) {
		block := blockMatch[1]
		fontMatch := pdfTextFont.FindSubmatch(block)
		if len(fontMatch) == 2 {
			fontSize, err := strconv.ParseFloat(string(fontMatch[1]), 64)
			if err != nil {
				t.Fatalf("page %d parse text font size %q: %v", page, fontMatch[1], err)
			}
			currentFontSize = fontSize
		}
		if !pdfTextShow.Match(block) {
			continue
		}
		var x, y float64
		var err error
		if matrix := pdfTextMatrix.FindAllSubmatch(block, -1); len(matrix) > 0 {
			x, err = strconv.ParseFloat(string(matrix[len(matrix)-1][5]), 64)
			if err == nil {
				y, err = strconv.ParseFloat(string(matrix[len(matrix)-1][6]), 64)
			}
		} else if delta := pdfTextDelta.FindSubmatch(block); len(delta) == 3 {
			x, err = strconv.ParseFloat(string(delta[1]), 64)
			if err == nil {
				y, err = strconv.ParseFloat(string(delta[2]), 64)
			}
		}
		if err != nil {
			t.Fatalf("page %d parse text position: %v", page, err)
		}
		for _, shown := range pdfTextShow.FindAllSubmatch(block, -1) {
			encoded := string(shown[1])
			glyphs := (len(encoded) - 2) / 2
			if strings.HasPrefix(encoded, "<") {
				glyphs = len(strings.Map(func(r rune) rune {
					if r == ' ' || r == '\n' || r == '\t' {
						return -1
					}
					return r
				}, encoded[1:len(encoded)-1])) / 4
			} else if strings.HasPrefix(encoded, "[") {
				glyphs = len(encoded) / 4
			}
			if glyphs < 0 {
				glyphs = 0
			}
			runs = append(runs, vvTask07PDFText{x: x, y: y, fontSize: currentFontSize, estimatedWidth: float64(glyphs) * currentFontSize * 0.55, encoded: encoded, text: decodeVVTask07PDFString(encoded)})
		}
	}
	return runs
}

func task07LargeVideoSnapshot(count int, limitations bool) reportcore.Snapshot {
	snapshot := validPDFSnapshot()
	snapshot.Requirements = make([]reportcore.Requirement, 0, count)
	snapshot.Findings = make([]reportcore.Finding, 0, count)
	snapshot.Evidence = make([]reportcore.Evidence, 0, count)
	for index := 0; index < count; index++ {
		key := fmt.Sprintf("video:finding-%03d", index)
		evidenceID := identity.NewID().String()
		frameID, sourceID := fmt.Sprintf("frame-%03d", index), fmt.Sprintf("source-%03d", index)
		ms, pts := int64(index*1000), int64(index*1_000_000)
		snapshot.Requirements = append(snapshot.Requirements, reportcore.Requirement{Key: key, Section: "Vídeo", Label: fmt.Sprintf("Componente %03d", index), AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "INCONCLUSIVE", Coverage: func() string {
			if limitations {
				return fmt.Sprintf("Limitação %03d", index)
			}
			return ""
		}()})
		snapshot.Findings = append(snapshot.Findings, reportcore.Finding{ID: fmt.Sprintf("Finding %03d", index), Category: "CONSERVATION", Title: fmt.Sprintf("Finding %03d", index), Description: "Desgaste observado.", Severity: "LOW", Confidence: .8, EvidenceIDs: []string{evidenceID}, Quality: "ADEQUATE", RecommendedAction: "MONITOR"})
		snapshot.Evidence = append(snapshot.Evidence, reportcore.Evidence{ID: evidenceID, RequirementKey: key, Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: evidenceID, SourceMediaID: sourceID, PreparationID: "prep-" + frameID, Description: frameID, PTSMicros: &pts, TimestampMS: &ms, FrameDigest: strings.Repeat("a", 64), Availability: "AVAILABLE"})
	}
	return snapshot
}

func renderVVTask07(t *testing.T, snapshot reportcore.Snapshot, assets pdf.AssetBundle) []byte {
	t.Helper()
	data, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "snapshot-vv-task07", Version: 1, Snapshot: snapshot, Internal: true}, assets)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func extractVVTask07Images(t *testing.T, data []byte) int {
	t.Helper()
	model.ConfigPath = "disable"
	count := 0
	err := api.ExtractImages(bytes.NewReader(data), nil, func(_ model.Image, _ bool, _ int) error { count++; return nil }, model.NewDefaultConfiguration())
	if err != nil {
		t.Fatalf("extract PDF images: %v", err)
	}
	return count
}

func ptrTask07Int64(value int64) *int64 { return &value }
