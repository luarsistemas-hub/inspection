package render_pdf

// PDF-KEEP: in a report with many evidences the heading of an evidence (title,
// instructions, limitation) must stay on the same page as its image and
// caption. The tests render real PDFs with enough evidences to force several
// page breaks at different offsets and read the pages back with pdfcpu
// (pdf_layout_helpers_test.go), so they verify the actual pagination.

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"

	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/pdf"
)

var (
	pdfInstructionLabel = regexp.MustCompile(`Instrução \d{3}`)
	pdfItemLabel        = regexp.MustCompile(`Item \d{3}`)
	pdfPairLabel        = regexp.MustCompile(`Evidência de referência ↔ vistoria atual`)
)

// keepRequirement builds requirement i with a variable-height heading, so the
// page breaks fall at different offsets inside the heading/image block.
func keepRequirement(i int) reportcore.Requirement {
	// One paragraph per line keeps every line short (the geometry helper
	// bounds text width by glyph count) while still varying the heading height.
	filler := strings.Repeat("\nlinha de apoio da instrução", (i*7)%9)
	return reportcore.Requirement{
		Key: fmt.Sprintf("video:keep-%03d", i), Section: "Vídeo", Label: fmt.Sprintf("Componente %03d", i),
		Instructions: fmt.Sprintf("Instrução %03d%s", i, filler),
		AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "INCONCLUSIVE", Coverage: fmt.Sprintf("Limitação %03d", i),
	}
}

func keepEvidence(requirement reportcore.Requirement, id, role, description string) reportcore.Evidence {
	ms, pts := int64(1000), int64(1_000_000)
	return reportcore.Evidence{
		ID: id, RequirementKey: requirement.Key, Role: role, EvidenceKind: "VIDEO_FRAME", FrameID: id, SourceMediaID: "source-" + id, PreparationID: "prep-" + id,
		Description: description, PTSMicros: &pts, TimestampMS: &ms, FrameDigest: strings.Repeat("a", 64), Availability: "AVAILABLE",
	}
}

// requireKeptHeading fails unless the title, instructions and limitation of
// requirement i are the nearest ones above the image, on the image's page.
func requireKeptHeading(t *testing.T, pages []vvTask07PDFPage, frameID string, i int) {
	t.Helper()
	pageIndex, image, found := findVVTask07CaptionedImage(pages, frameID)
	if !found {
		t.Errorf("no image with frame %s captioned directly below it", frameID)
		return
	}
	for pattern, want := range map[*regexp.Regexp]string{
		pdfTitleLabel:       fmt.Sprintf("Componente %03d", i),
		pdfInstructionLabel: fmt.Sprintf("Instrução %03d", i),
		pdfLimitationLabel:  fmt.Sprintf("Limitação %03d", i),
	} {
		if !labelAboveVVTask07Image(pages[pageIndex], image, pattern, want) {
			t.Errorf("page %d: image of frame %s is not under %q on the same page", pageIndex+1, frameID, want)
		}
	}
}

func TestPDFKeepRequirementHeadingStaysWithEvidenceImage(t *testing.T) {
	const count = 60
	snapshot := validPDFSnapshot()
	snapshot.Requirements = nil
	assets := make(pdf.AssetBundle, count)
	for i := 0; i < count; i++ {
		requirement := keepRequirement(i)
		id := fmt.Sprintf("frame-%03d", i)
		snapshot.Requirements = append(snapshot.Requirements, requirement)
		snapshot.Evidence = append(snapshot.Evidence, keepEvidence(requirement, id, "CURRENT", id))
		assets["evidence-"+id+".jpg"] = jpegBytes(t, 48, 36)
	}
	data := renderVVTask07(t, snapshot, assets)
	if pages := PageCount(data); pages < 8 {
		t.Fatalf("PDF has %d pages; the scenario must force several page breaks", pages)
	}
	pages := assertVVTask07PDFGeometry(t, data, count)
	for i := 0; i < count; i++ {
		requireKeptHeading(t, pages, fmt.Sprintf("frame-%03d", i), i)
	}
}

func TestPDFKeepReferenceAndCurrentPairKeepsColumnLabelsAndHeading(t *testing.T) {
	const count = 30
	snapshot := validPDFSnapshot()
	snapshot.Requirements = nil
	assets := make(pdf.AssetBundle, count*2)
	for i := 0; i < count; i++ {
		requirement := keepRequirement(i)
		reference, current := fmt.Sprintf("ref-%03d", i), fmt.Sprintf("cur-%03d", i)
		snapshot.Requirements = append(snapshot.Requirements, requirement)
		// Photos have short captions; a long video caption wrapped in a half-width
		// column would trip the geometry helper's coarse text-width bound.
		snapshot.Evidence = append(snapshot.Evidence,
			reportcore.Evidence{ID: reference, RequirementKey: requirement.Key, Role: "REFERENCE", Description: reference, Availability: "AVAILABLE"},
			reportcore.Evidence{ID: current, RequirementKey: requirement.Key, Role: "CURRENT", Description: current, Availability: "AVAILABLE"})
		assets["evidence-"+reference+".jpg"] = jpegBytes(t, 48, 36)
		assets["evidence-"+current+".jpg"] = jpegBytes(t, 48, 36)
	}
	data := renderVVTask07(t, snapshot, assets)
	if pages := PageCount(data); pages < 8 {
		t.Fatalf("PDF has %d pages; the scenario must force several page breaks", pages)
	}
	pages := assertVVTask07PDFGeometry(t, data, count*2)
	for i := 0; i < count; i++ {
		requireKeptHeading(t, pages, fmt.Sprintf("ref-%03d", i), i)
		requireKeptHeading(t, pages, fmt.Sprintf("cur-%03d", i), i)
		pageIndex, image, _ := findVVTask07CaptionedImage(pages, fmt.Sprintf("ref-%03d", i))
		if !textAboveVVTask07Image(pages[pageIndex], image, "Referência") {
			t.Errorf("page %d: reference column label is not above the image of ref-%03d", pageIndex+1, i)
		}
	}
}

func TestPDFKeepLongCaptionKeepsHeadingImageAndFirstCaptionLine(t *testing.T) {
	const count = 24
	snapshot := validPDFSnapshot()
	snapshot.Requirements = nil
	assets := make(pdf.AssetBundle, count)
	longDescription := strings.Repeat("descrição extensa da evidência ", 24) // 744 runes: multi-chunk caption layout
	for i := 0; i < count; i++ {
		requirement := keepRequirement(i)
		id := fmt.Sprintf("long-%03d", i)
		snapshot.Requirements = append(snapshot.Requirements, requirement)
		snapshot.Evidence = append(snapshot.Evidence, keepEvidence(requirement, id, "CURRENT", longDescription))
		assets["evidence-"+id+".jpg"] = jpegBytes(t, 48, 36)
	}
	data := renderVVTask07(t, snapshot, assets)
	if pages := PageCount(data); pages < 6 {
		t.Fatalf("PDF has %d pages; the scenario must force several page breaks", pages)
	}
	// assertVVTask07PDFGeometry also requires caption text right below every image.
	pages := assertVVTask07PDFGeometry(t, data, count)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("long-%03d", i)
		found := false
		for _, page := range pages {
			for _, image := range page.images {
				// Long captions are split in chunks: the frame ID is in the last one,
				// so the image is identified by the title above it instead.
				if labelAboveVVTask07Image(page, image, pdfTitleLabel, fmt.Sprintf("Componente %03d", i)) && labelAboveVVTask07Image(page, image, pdfLimitationLabel, fmt.Sprintf("Limitação %03d", i)) && labelAboveVVTask07Image(page, image, pdfInstructionLabel, fmt.Sprintf("Instrução %03d", i)) {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("no image of %s sits under its title, instructions and limitation on one page", id)
		}
	}
}

func TestPDFKeepInventoryPairKeepsProblemHeadingWithItsImages(t *testing.T) {
	const count = 24
	snapshot := validPDFSnapshot()
	snapshot.SchemaVersion = 2
	assets := make(pdf.AssetBundle, count*2)
	problems := make([]reportcore.InventoryEntry, 0, count)
	for i := 0; i < count; i++ {
		origin, current := fmt.Sprintf("origin-%03d", i), fmt.Sprintf("current-%03d", i)
		problems = append(problems, reportcore.InventoryEntry{
			Environment: "Sala", Type: "SOFA", Name: fmt.Sprintf("Item %03d", i), Status: "MISSING",
			Reason: strings.TrimPrefix(strings.Repeat("\nmotivo do problema", (i*5)%7), "\n"),
			Pairs:  []reportcore.InventoryEvidencePair{{Origin: origin, Current: current, Decision: "SAME_VIEW"}},
		})
		assets["evidence-"+origin+".jpg"] = jpegBytes(t, 48, 36)
		assets["evidence-"+current+".jpg"] = jpegBytes(t, 48, 36)
		// The snapshot must know the paired evidence (core.Validate); it is also
		// listed in the evidence section, after the inventory.
		snapshot.Evidence = append(snapshot.Evidence,
			reportcore.Evidence{ID: origin, RequirementKey: "env:sala", Role: "REFERENCE", Description: origin, Availability: "AVAILABLE"},
			reportcore.Evidence{ID: current, RequirementKey: "env:sala", Role: "CURRENT", Description: current, Availability: "AVAILABLE"})
	}
	snapshot.Inventory = &reportcore.InventorySection{Reference: reportcore.InventoryReference{VersionID: "version-1", VersionNumber: 1, Purpose: "MOVE_IN", ActivatedAt: "2026-01-01T00:00:00Z"}, Coverage: &reportcore.InventoryCoverage{ExpectedItems: count, AssessedItems: count}, Problems: problems}
	data := renderVVTask07(t, snapshot, assets)
	if pages := PageCount(data); pages < 5 {
		t.Fatalf("PDF has %d pages; the scenario must force several page breaks", pages)
	}
	// Inventory pairs (2 per problem) plus the same photos in the evidence section.
	pages := assertVVTask07PDFGeometry(t, data, count*4)
	var rows []struct {
		page  int
		image vvTask07PDFImage
	}
	for pageIndex, page := range pages {
		for _, image := range page.images {
			rows = append(rows, struct {
				page  int
				image vvTask07PDFImage
			}{pageIndex, image})
		}
	}
	for i := 0; i < count; i++ {
		first := rows[i*2] // left (reference) image of pair i, in rendering order
		page := pages[first.page]
		if !labelAboveVVTask07Image(page, first.image, pdfItemLabel, fmt.Sprintf("Item %03d", i)) || !labelAboveVVTask07Image(page, first.image, pdfPairLabel, pdfPairLabel.String()) {
			t.Errorf("page %d: images of pair %d are not under the problem heading and pair label of the same page", first.page+1, i)
		}
	}
}

// textAboveVVTask07Image reports whether a text equal to want sits directly
// above the image (the column labels of a reference/current row).
func textAboveVVTask07Image(page vvTask07PDFPage, image vvTask07PDFImage, want string) bool {
	for _, text := range page.texts {
		gap := text.y - (image.y + image.height)
		if strings.TrimSpace(text.text) == want && gap >= -1 && gap <= 20 && math.Abs(text.x-image.x) < image.width {
			return true
		}
	}
	return false
}
