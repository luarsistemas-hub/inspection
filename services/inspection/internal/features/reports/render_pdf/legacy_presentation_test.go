package render_pdf

// Report v1/v2 presentation must stay byte-for-byte what it was before report
// v3 (task_04): the golden files under testdata/legacy_presentation were
// produced by this same test file run against the pre-v3 code (commit
// fba7de44, REPORT_LEGACY_GOLDEN_DIR set), and record the HTML
// bytes plus, per PDF page, the content-stream digest and its decoded text.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/pdf"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	pdfcpu "github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

func legacyPresentationEvidenceID(index int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", index)
}

// legacyPresentationFixtures are v1 and v2 snapshots with evidence that
// exercise every presentation rule task_04 changed for v3: E### references,
// the evidence index (more than ten items), caption splitting, frame captions,
// flags, analysis statuses, unavailable images, inventory conditions and the
// footer report number.
func legacyPresentationFixtures() map[string]reportcore.Snapshot {
	unchanged := true
	ms, pts := int64(65_000), int64(65_000_000)
	base := reportcore.Snapshot{
		SchemaVersion: 1, ReportID: "legacy-report-previous-id", InspectionID: "legacy-inspection", TemplateVersionID: "template-v1", ReferenceVersionID: "reference-v1", PromptDigest: "prompt-v1",
		Mode: "HISTORICAL", Classification: "ATTENTION", ReasonCodes: []string{"OBSERVED_CHANGE", "EVIDENCE_FLAG", "INCONCLUSIVE"}, Coverage: map[string]string{},
		Context: reportcore.Context{Asset: reportcore.AssetContext{ID: "asset-1", Name: "Casa 7", Address: "Rua das Flores, 7"}, Participant: reportcore.ParticipantContext{ID: "participant-1", Name: "Maria Souza"}, Template: reportcore.TemplateContext{ID: "template-1", Name: "Vistoria periódica", Version: 2}, Inspection: reportcore.InspectionContext{GeneratedAt: "2026-10-01T12:00:00Z"}},
		Requirements: []reportcore.Requirement{
			{Key: "roof", Section: "Cobertura", Label: "Telhado e calhas", Instructions: "Registre o telhado.", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "FAILED"},
			{Key: "kitchen", Section: "Cozinha", Label: "Pia e bancada", Instructions: "Registre a pia.", Coverage: "Cobertura parcial da bancada", AnalysisMode: "COMPARE_ORIGIN_CURRENT", AnalysisStatus: "INCONCLUSIVE", NoRelevantChange: &unchanged},
			{Key: "video:living", Section: "Vídeo", Label: "Sala de estar", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "COMPLETED"},
		},
		Findings: []reportcore.Finding{{ID: "finding-1", Category: "CONSERVATION", Title: "Mancha no forro", Description: "Mancha de umidade próxima à calha.", Severity: "MEDIUM", Confidence: 0.8, EvidenceIDs: []string{legacyPresentationEvidenceID(1)}, Quality: "ADEQUATE", RecommendedAction: "REVIEW"}},
		Advisory: "Esta triagem interna descreve alterações observadas e não atribui culpa, custo, responsabilidade ou consequência automática.",
	}
	flags := [][]string{{"GALLERY_SOURCE"}, {"CAPTURE_TIME_CONFLICT", "LOW_QUALITY"}, {"CAPTURE_TIME_DEVICE_LOCAL"}, {"SENSITIVE_DETECTION"}, nil}
	for index := 1; index <= 11; index++ {
		item := reportcore.Evidence{ID: legacyPresentationEvidenceID(index), RequirementKey: "roof", Role: "CURRENT", Description: fmt.Sprintf("Foto %02d do telhado", index), CapturedAt: "2026-10-01T10:00:00Z", Flags: flags[index%len(flags)]}
		if index%2 == 0 {
			item.RequirementKey, item.Role = "kitchen", "REFERENCE"
		}
		if index == 3 {
			item.Description = "Descrição longa da fotografia da calha, com detalhes suficientes para quebrar a legenda em mais de uma linha no PDF."
		}
		if index == 5 {
			item.Availability = "MISSING"
		}
		base.Evidence = append(base.Evidence, item)
	}
	frameID := legacyPresentationEvidenceID(12)
	base.Evidence = append(base.Evidence, reportcore.Evidence{ID: frameID, RequirementKey: "video:living", Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: frameID, SourceMediaID: "source-media-1", PreparationID: "preparation-1", PTSMicros: &pts, TimestampMS: &ms, FrameDigest: strings.Repeat("a", 64), SourceAvailability: "EXPIRED", Description: "Quadro do vídeo em 65000 ms", CaptureSource: "VIDEO", Availability: "AVAILABLE"})

	v2 := base
	v2.SchemaVersion = 2
	v2.Evidence = append([]reportcore.Evidence(nil), base.Evidence...)
	v2.Inventory = &reportcore.InventorySection{
		Reference: reportcore.InventoryReference{VersionID: "baseline-1", VersionNumber: 1, Purpose: "MOVE_IN", ActivatedAt: "2026-09-01T00:00:00Z"},
		Coverage:  &reportcore.InventoryCoverage{ExpectedItems: 3, AssessedItems: 2, NotObserved: 1, EnvironmentsNotCaptured: []string{}, EnvironmentsAddedDuringCapture: []string{}},
		Problems: []reportcore.InventoryEntry{
			{ItemIdentityID: "item-1", Environment: "Sala", Type: "SOFA", Name: "Sofá", Status: "CONDITION_CHANGED", ConditionBefore: "GOOD", ConditionAfter: "", Pairs: []reportcore.InventoryEvidencePair{{Origin: legacyPresentationEvidenceID(2), Current: legacyPresentationEvidenceID(1), Decision: "SAME_VIEW"}}},
			{ItemIdentityID: "item-2", Environment: "Sala", Type: "TABLE", Name: "Mesa", Status: "MISSING", Reason: "Item não encontrado"},
		},
		Informational: []reportcore.InventoryEntry{},
	}
	return map[string]reportcore.Snapshot{"v1": base, "v2": v2}
}

func legacyPresentationAssets(t *testing.T, snapshot reportcore.Snapshot) pdf.AssetBundle {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 8; x++ {
			canvas.Set(x, y, color.RGBA{R: uint8(30 * x), G: uint8(40 * y), B: 120, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	assets := pdf.AssetBundle{}
	for index, item := range snapshot.Evidence {
		if index%3 == 2 || item.Availability == "MISSING" {
			continue
		}
		assets["evidence-"+item.ID+".jpg"] = encoded.Bytes()
	}
	return assets
}

// legacyPDFDump describes a PDF page by page: the content-stream digest and
// the decoded text, which cover layout, pagination and wording.
func legacyPDFDump(t *testing.T, data []byte) string {
	t.Helper()
	model.ConfigPath = "disable"
	parsed, err := api.ReadAndValidate(bytes.NewReader(data), model.NewDefaultConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	var dump strings.Builder
	for page := 1; page <= PageCount(data); page++ {
		stream, err := pdfcpu.ExtractPageContent(parsed, page)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(stream)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		fmt.Fprintf(&dump, "page %d stream %s\n", page, hex.EncodeToString(sum[:]))
		for _, text := range extractVVTask07PDFText(t, page, raw) {
			dump.WriteString(text.text)
			dump.WriteByte('\n')
		}
	}
	return dump.String()
}

func renderLegacyPresentation(t *testing.T, snapshot reportcore.Snapshot) map[string]string {
	t.Helper()
	html, err := reportcore.HTML(snapshot)
	if err != nil {
		t.Fatalf("v%d HTML: %v", snapshot.SchemaVersion, err)
	}
	outputs := map[string]string{"html": string(html)}
	for _, audience := range []string{"PDF", "PDF_CUSTOMER"} {
		projected, internal, err := projectForAudience(snapshot, audience)
		if err != nil {
			t.Fatalf("v%d %s projection: %v", snapshot.SchemaVersion, audience, err)
		}
		data, err := (MarotoRenderer{DashboardOrigin: "https://dashboard.example"}).Render(context.Background(), PDFDocument{SnapshotID: "legacy-snapshot-id", Version: 3, Snapshot: projected, Internal: internal}, legacyPresentationAssets(t, projected))
		if err != nil {
			t.Fatalf("v%d %s render: %v", snapshot.SchemaVersion, audience, err)
		}
		outputs[strings.ToLower(audience)] = legacyPDFDump(t, data)
	}
	return outputs
}

func TestLegacyV1V2HTMLAndPDFMatchPreV3Presentation(t *testing.T) {
	fixtures := legacyPresentationFixtures()
	if dir := os.Getenv("REPORT_LEGACY_GOLDEN_DIR"); dir != "" {
		for name, snapshot := range fixtures {
			for kind, output := range renderLegacyPresentation(t, snapshot) {
				if err := os.WriteFile(filepath.Join(dir, name+"_"+kind+".golden"), []byte(output), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		t.Skip("golden files written")
	}
	for name, snapshot := range fixtures {
		for kind, output := range renderLegacyPresentation(t, snapshot) {
			want, err := os.ReadFile(filepath.Join("testdata", "legacy_presentation", name+"_"+kind+".golden"))
			if err != nil {
				t.Fatal(err)
			}
			if output != string(want) {
				gotLines, wantLines := strings.Split(output, "\n"), strings.Split(string(want), "\n")
				for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
					got, expected := "", ""
					if i < len(gotLines) {
						got = gotLines[i]
					}
					if i < len(wantLines) {
						expected = wantLines[i]
					}
					if got != expected {
						offset := 0
						for offset < len(got) && offset < len(expected) && got[offset] == expected[offset] {
							offset++
						}
						start := max(0, offset-80)
						t.Errorf("%s %s differs from the pre-v3 presentation at line %d, byte %d:\n got: %.240q\nwant: %.240q", name, kind, i+1, offset, got[start:], expected[start:])
						break
					}
				}
			}
		}
	}
}
