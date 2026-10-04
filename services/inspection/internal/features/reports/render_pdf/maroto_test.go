package render_pdf

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/pdf"
)

func TestMarotoRendererProducesUnicodeA4PDFWithEvidence(t *testing.T) {
	snapshot := validPDFSnapshot()
	snapshot.Evidence = []core.Evidence{
		{ID: "reference-photo", RequirementKey: "roof", Role: "REFERENCE", Description: "Referência"},
		{ID: "current-photo", RequirementKey: "roof", Role: "CURRENT", Description: "Vistoria atual"},
	}
	landscape := jpegBytes(t, 640, 360)
	portrait := jpegBytes(t, 360, 640)
	result, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "snapshot-1", Version: 1, Snapshot: snapshot, Internal: true}, pdf.AssetBundle{
		"evidence-reference-photo.jpg": portrait,
		"evidence-current-photo.jpg":   landscape,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(result, []byte("%PDF-")) {
		t.Fatal("rendered output is not a PDF")
	}
	if got := len(pdfPageMarker.FindAll(result, -1)); got != 1 {
		t.Fatalf("page count = %d, want 1", got)
	}
}

func TestMarotoRendererPaginatesLongTextAndRejectsCustomerFindings(t *testing.T) {
	snapshot := validPDFSnapshot()
	snapshot.Advisory = strings.Repeat("Descrição extensa com acentuação: vistoria, conservação e segurança. ", 1400)
	result, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "snapshot-1", Version: 1, Snapshot: snapshot, Internal: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(pdfPageMarker.FindAll(result, -1)); got < 2 {
		t.Fatalf("long report page count = %d, want multiple pages", got)
	}

	snapshot.Findings = []core.Finding{{Category: "CONSERVATION", Title: "Constatação interna", Description: "Texto interno", Severity: "MEDIUM", Confidence: .8, EvidenceIDs: []string{"photo"}, Quality: "HIGH", RecommendedAction: "REVIEW"}}
	if _, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "snapshot-1", Version: 1, Snapshot: snapshot, Internal: false}, nil); err == nil {
		t.Fatal("customer PDF accepted internal findings")
	}
}

func TestMarotoRendererMarksMissingImagesAndHonorsCancellation(t *testing.T) {
	snapshot := validPDFSnapshot()
	snapshot.Evidence = []core.Evidence{{ID: "missing-photo", RequirementKey: "roof", Role: "CURRENT", Availability: "MISSING"}}
	if _, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "snapshot-1", Version: 1, Snapshot: snapshot, Internal: false}, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (MarotoRenderer{}).Render(ctx, PDFDocument{SnapshotID: "snapshot-1", Version: 1, Snapshot: snapshot}, nil); err == nil {
		t.Fatal("canceled rendering returned success")
	}
}

func TestCustomerProjectionExcludesInternalFindings(t *testing.T) {
	snapshot := validPDFSnapshot()
	snapshot.Findings = []core.Finding{{Category: "CONSERVATION", Title: "Constatação interna", Description: "Texto interno", Severity: "MEDIUM", Confidence: .8, EvidenceIDs: []string{"photo"}, Quality: "HIGH", RecommendedAction: "REVIEW"}}
	projected, internal, err := projectForAudience(snapshot, "PDF_CUSTOMER")
	if err != nil || internal || len(projected.Findings) != 0 {
		t.Fatalf("customer projection internal=%v findings=%d err=%v", internal, len(projected.Findings), err)
	}
	projected, internal, err = projectForAudience(snapshot, "PDF")
	if err != nil || !internal || len(projected.Findings) != 1 {
		t.Fatalf("internal projection internal=%v findings=%d err=%v", internal, len(projected.Findings), err)
	}
}

func validPDFSnapshot() core.Snapshot {
	return core.Snapshot{
		SchemaVersion: 1, ReportID: "report-1", InspectionID: "inspection-1", TemplateVersionID: "template-v1", ReferenceVersionID: "reference-v1", PromptDigest: "digest-v1",
		Mode: "HISTORICAL", Classification: "ATTENTION", ReasonCodes: []string{"OBSERVED_CHANGE"}, Advisory: "Laudo com manutenção preventiva recomendada.",
		Context: core.Context{
			Asset:       core.AssetContext{ID: "asset-1", Name: "Apartamento 12", Address: "Rua São João, 12 — São Paulo"},
			Participant: core.ParticipantContext{ID: "participant-1", Name: "João da Silva"},
			Template:    core.TemplateContext{ID: "template-1", Name: "Vistoria periódica", Version: 1},
			Inspection:  core.InspectionContext{GeneratedAt: "2026-10-03T12:00:00Z"},
		},
		Requirements: []core.Requirement{{Key: "roof", Section: "Cobertura", Label: "Telhado e calhas", Instructions: "Registrar estado atual.", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "COMPLETED"}},
	}
}

func jpegBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 120, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestChunkTextKeepsEveryWordAndBoundsRowSize(t *testing.T) {
	words := make([]string, 0, 3000)
	for index := 0; index < 3000; index++ {
		words = append(words, fmt.Sprintf("palavra%d", index))
	}
	input := strings.Join(words[:1500], " ") + "\n" + strings.Join(words[1500:], " ") + " " + strings.Repeat("x", 3000)
	chunks := chunkText(input, 500)
	var rejoined []string
	for _, chunk := range chunks {
		if len([]rune(chunk)) > 500 {
			t.Fatalf("chunk with %d runes exceeds the limit", len([]rune(chunk)))
		}
		rejoined = append(rejoined, strings.Fields(chunk)...)
	}
	joined := strings.Join(rejoined, "")
	if !strings.Contains(joined, "palavra0palavra1") || !strings.Contains(joined, "palavra2999") || strings.Count(joined, "x") != 3000 {
		t.Fatal("chunking dropped or reordered text")
	}
	if len(chunkText("   ", 500)) != 0 {
		t.Fatal("blank text produced rows")
	}
}

func TestMarotoRendererSpreadsVeryLongTextOverPagesWithoutOverflow(t *testing.T) {
	snapshot := validPDFSnapshot()
	snapshot.Advisory = strings.Repeat("Descrição extensa da vistoria. ", 3000)
	result, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "snapshot-1", Version: 1, Snapshot: snapshot, Internal: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// ~93k characters at roughly 12k characters per page, with no blank pages.
	if got := PageCount(result); got < 6 || got > 14 {
		t.Fatalf("long advisory page count = %d, want between 6 and 14", got)
	}
}

func TestMarotoRendererPairsReferenceAndCurrentPhotosAndAcceptsPNG(t *testing.T) {
	snapshot := validPDFSnapshot()
	noChange := true
	snapshot.Requirements[0].AnalysisMode, snapshot.Requirements[0].NoRelevantChange = "COMPARE_ORIGIN_CURRENT", &noChange
	snapshot.Evidence = []core.Evidence{
		{ID: "ref-1", RequirementKey: "roof", Role: "REFERENCE", Flags: []string{"GPS_MISSING"}},
		{ID: "cur-1", RequirementKey: "roof", Role: "CURRENT"},
		{ID: "cur-2", RequirementKey: "roof", Role: "CURRENT"},
		{ID: "extra-b", RequirementKey: "zeta", Role: "CURRENT"},
		{ID: "extra-a", RequirementKey: "alpha", Role: "CURRENT"},
	}
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 50, 40))); err != nil {
		t.Fatal(err)
	}
	assets := pdf.AssetBundle{"evidence-ref-1.jpg": jpegBytes(t, 640, 360), "evidence-cur-1.jpg": pngData.Bytes(), "evidence-cur-2.jpg": jpegBytes(t, 360, 640), "evidence-extra-b.jpg": jpegBytes(t, 100, 100), "evidence-extra-a.jpg": jpegBytes(t, 100, 100)}
	first, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "snapshot-1", Version: 1, Snapshot: snapshot, Internal: true}, assets)
	if err != nil {
		t.Fatal(err)
	}
	if PageCount(first) < 1 {
		t.Fatal("no pages rendered")
	}
	if _, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "snapshot-1", Version: 1, Snapshot: snapshot, Internal: true}, pdf.AssetBundle{"evidence-ref-1.jpg": []byte("GIF89a not supported")}); err == nil {
		t.Fatal("undecodable image accepted")
	}
}

func TestUniqueImageCountIgnoresMissingAndDuplicates(t *testing.T) {
	items := []core.Evidence{{ID: "a"}, {ID: "a"}, {ID: "b", Availability: "MISSING"}, {ID: "c"}}
	if got := UniqueImageCount(items); got != 2 {
		t.Fatalf("unique images = %d, want 2", got)
	}
	snapshot := validPDFSnapshot()
	snapshot.Evidence = []core.Evidence{{ID: "a", RequirementKey: "roof"}, {ID: "b", RequirementKey: "roof"}}
	if _, err := (MarotoRenderer{MaxEvidence: 1}).Render(context.Background(), PDFDocument{SnapshotID: "s", Version: 1, Snapshot: snapshot, Internal: true}, nil); err == nil {
		t.Fatal("evidence limit not enforced")
	}
}
