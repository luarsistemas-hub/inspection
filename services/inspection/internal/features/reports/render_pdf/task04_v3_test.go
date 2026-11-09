package render_pdf

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/reports/core"
	customerreport "inspection/services/inspection/internal/features/reports/customer/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/pdf"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"
	"github.com/johnfercher/maroto/v2"
	marotoimage "github.com/johnfercher/maroto/v2/pkg/components/image"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/extension"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/pagesize"
	"github.com/johnfercher/maroto/v2/pkg/fontrepository"
	"github.com/johnfercher/maroto/v2/pkg/props"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	pdfcpu "github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// TestUT045UT054E2E010V3PDFFixtures checks readable pagination, captions, and QR association.
func TestUT045UT054E2E010V3PDFFixtures(t *testing.T) {
	renderer := MarotoRenderer{DashboardOrigin: "https://dashboard.example.test"}
	verificationURL, err := url.Parse(reportVerificationURL(renderer.DashboardOrigin, "random-code", 3))
	if err != nil || verificationURL.Path != "/verify-report" || verificationURL.Query().Get("code") != "random-code" || verificationURL.Query().Get("version") != "3" || len(verificationURL.Query()) != 2 {
		t.Fatalf("QR verification URL=%v err=%v; want only the stable public route, code and version", verificationURL, err)
	}
	short := task04PDFSnapshot(1)
	shortPDF, err := renderer.Render(context.Background(), PDFDocument{SnapshotID: "short", Version: 1, Snapshot: short, Internal: true}, pdf.AssetBundle{"evidence-photo-000.jpg": jpegBytes(t, 320, 240)})
	if err != nil {
		t.Fatal(err)
	}
	assertV3PDFTextAndPages(t, shortPDF, 1, []string{"Checklist descritivo", "Cozinha", "Piso", "Foto ilustrativa", "Sem alertas identificados", "Verifique a autenticidade", "123.4", "kWh", "2026-11-04", "Participante", "Evidência: E002", "E001 · Vistoria atual", "Observado", "Bom", "Não revisado", "Laudo RPT-T04"})

	long := task04PDFSnapshot(42)
	assets := pdf.AssetBundle{}
	for _, item := range long.Evidence {
		assets["evidence-"+item.ID+".jpg"] = jpegBytes(t, 160, 120)
	}
	longPDF, err := renderer.Render(context.Background(), PDFDocument{SnapshotID: "long", Version: 2, Snapshot: long, Internal: true}, assets)
	if err != nil {
		t.Fatal(err)
	}
	assertV3PDFTextAndPages(t, longPDF, 2, []string{"Componente observado", "Foto ilustrativa", "Verifique a autenticidade", "Anexo de evidências", "E001", "E042"})

	unavailable := task04PDFSnapshot(0)
	unavailable.Evidence = []core.Evidence{{ID: "gone", RequirementKey: "env:kitchen", Role: "CURRENT", Availability: "MISSING"}}
	unavailablePDF, err := renderer.Render(context.Background(), PDFDocument{SnapshotID: "unavailable", Version: 1, Snapshot: unavailable, Internal: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertV3PDFTextAndPages(t, unavailablePDF, 1, []string{"Nenhuma evidência registrada", "Imagem indisponível"})

	unavailableMedia := task04PDFSnapshot(1)
	unavailableMedia.Evidence[0].Availability = "UNAVAILABLE"
	unavailableMediaPDF, err := renderer.Render(context.Background(), PDFDocument{SnapshotID: "unavailable-media", Version: 1, Snapshot: unavailableMedia, Internal: true}, pdf.AssetBundle{"evidence-photo-000.jpg": jpegBytes(t, 320, 240)})
	if err != nil {
		t.Fatal(err)
	}
	assertV3PDFTextAndPages(t, unavailableMediaPDF, 1, []string{"Imagem indisponível"})

	failure := task04PDFSnapshot(0)
	failure.Processing = &core.ProcessingState{Status: "FAILED", Message: "A análise não foi concluída."}
	failure.Classification = "ATTENTION"
	failurePDF, err := renderer.Render(context.Background(), PDFDocument{SnapshotID: "failure", Version: 1, Snapshot: failure, Internal: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertV3PDFTextAndPages(t, failurePDF, 1, []string{"A análise não foi concluída", "Nenhuma evidência registrada"})
}

// TestTask04CustomerV3PDFExecutorPublishesAllowlistedPresentation renders a
// v3 PDF_CUSTOMER job through the real executor (queue → projection → renderer
// → artifact) and reads the published PDF text.
func TestTask04CustomerV3PDFExecutorPublishesAllowlistedPresentation(t *testing.T) {
	env := newTestEnv(t)
	if err := env.db.Exec(`CREATE TABLE media.screening_runs (id blob PRIMARY KEY, tenant_id blob, media_id blob, status text, model_digest text, regions blob, created_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	snapshot := task04PDFSnapshot(1)
	photoID, meterID := identity.NewID(), identity.NewID()
	evidenceID, meterEvidenceID := photoID.String(), meterID.String()
	// The component photo is screened CLEARED and has a display derivative, so
	// the customer PDF embeds it; the meter photo was never screened, so the
	// customer copy shows the unavailable placeholder with its caption.
	for _, id := range []identity.ID{photoID, meterID} {
		data := jpegBytes(t, 40, 30)
		key, hash := objectstore.DerivativeKey(env.tenantID, id, "DISPLAY", data)
		env.client.objects[key] = data
		if err := env.db.Create(&database.MediaDerivative{ID: identity.NewID(), TenantID: env.tenantID, MediaID: id, ObjectKey: key, Kind: "DISPLAY", SHA256: hash, ContentType: "image/jpeg", Width: 40, Height: 30, SizeBytes: int64(len(data))}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := env.db.Exec("INSERT INTO media.screening_runs (id,tenant_id,media_id,status,model_digest,regions,created_at) VALUES (?,?,?,?,?,?,?)", identity.NewID(), env.tenantID, photoID, "CLEARED", "digest", "[]", env.clock.Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	snapshot.Evidence[0].ID = evidenceID
	snapshot.Evidence[1].ID, snapshot.Evidence[1].Availability = meterEvidenceID, "AVAILABLE"
	snapshot.Checklist.Rooms[0].Key = "kitchen-key"
	snapshot.Checklist.Rooms[0].Components[0].Key = "floor-key"
	snapshot.Checklist.Rooms[0].Components[0].EvidenceIDs = []string{evidenceID}
	snapshot.Checklist.Rooms[0].Components[0].AttributeSources = []core.AttributeSource{{Attribute: "condition", Source: "VOICE", EvidenceIDs: []string{evidenceID}}}
	snapshot.Handover.Entries[0].Key = "meter-key"
	snapshot.Handover.Entries[0].EvidenceIDs = []string{meterEvidenceID}
	snapshot.Handover.Entries = append(snapshot.Handover.Entries,
		core.HandoverEntry{Key: "front-keys", Kind: "KEY", Status: "PENDING"},
		core.HandoverEntry{Key: "private-note", Kind: "NOTE", Status: "DECLARED", Payload: []byte(`{"note":"Senha do portão 4321"}`), Declarant: "Participante"},
	)
	canonical, digest, err := core.CanonicalJSON(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	env.snapshot.CanonicalJSON, env.snapshot.JSONDigest = canonical, digest
	if err := env.db.Model(&database.ReportSnapshot{}).Where("id=?", env.snapshot.ID).Updates(map[string]any{"canonical_json": canonical, "json_digest": digest}).Error; err != nil {
		t.Fatal(err)
	}
	job := env.addJob(t, "PDF_CUSTOMER", "QUEUED", 0)
	if worked, err := env.executor.RunDue(context.Background()); err != nil || !worked {
		t.Fatalf("customer PDF queue execution worked=%v err=%v", worked, err)
	}
	var completed database.ReportPDFJob
	if err := env.db.Table("reports.report_pdf_jobs").Where("id=?", job.ID).Take(&completed).Error; err != nil {
		t.Fatal(err)
	}
	if completed.Status != "READY" || completed.ArtifactID == nil {
		t.Fatalf("customer PDF job did not publish an artifact: %+v", completed)
	}
	var artifact database.ReportArtifact
	if err := env.db.Where("tenant_id=? AND id=?", env.tenantID, *completed.ArtifactID).Take(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	pdfBytes := env.client.objects[artifact.ObjectKey]
	if len(pdfBytes) == 0 {
		t.Fatalf("customer PDF artifact %q is missing from object store", artifact.ObjectKey)
	}
	if images := bytes.Count(pdfBytes, []byte("/Subtype /Image")); images < 2 {
		t.Fatalf("customer PDF embedded %d images, want the cleared photo and the placeholder", images)
	}
	text := assertV3PDFTextAndPages(t, pdfBytes, 1, []string{"LAUDO DE VISTORIA", "Piso · Observado · Componente observado · Bom · Não revisado", "Evidência: E001", "Medidor · Declarado · Energia · 123.4 · kWh", "Evidência: E002", "Chaves · Pendente", "E001 · Vistoria atual · Foto ilustrativa", "E002 · Vistoria atual · Foto do medidor", "Imagem não exibida nesta versão"})
	if strings.Count(text, "Imagem não exibida nesta versão") != 1 || strings.Contains(text, "Imagem indisponível") {
		t.Fatalf("only the unscreened meter photo should be withheld in the customer PDF: %s", text)
	}
	for _, forbidden := range []string{"Situação não reconhecida", "Evidência sem índice", "scope-v1", "prompt-v1", "Declaração por voz", "Senha do portão", "LAUDO INTERNO"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("customer PDF contains %q (double translation or non-allowlisted fact): %s", forbidden, text)
		}
	}
}

// TestTask04CustomerProjectionRendersLabelsOnce is the permanent form of the
// review reproduction: the customer projection of a valid v3 snapshot renders
// without images, applying labels and evidence numbers exactly once.
func TestTask04CustomerProjectionRendersLabelsOnce(t *testing.T) {
	snapshot := task04PDFSnapshot(1)
	if err := core.Validate(snapshot); err != nil {
		t.Fatalf("fixture is not a valid v3 snapshot: %v", err)
	}
	projected, internal, err := projectForAudience(snapshot, "PDF_CUSTOMER")
	if err != nil || internal {
		t.Fatalf("customer projection internal=%v err=%v", internal, err)
	}
	if projected.Scope != nil || projected.Provenance != nil || projected.Findings != nil {
		t.Fatalf("customer projection kept internal sections: scope=%v provenance=%v findings=%v", projected.Scope, projected.Provenance, projected.Findings)
	}
	if err := core.ValidateCustomerSnapshot(projected); err != nil {
		t.Fatalf("customer projection rejected by customer validation: %v", err)
	}
	component := projected.Checklist.Rooms[0].Components[0]
	if component.Status != "OBSERVED" || component.Condition != "GOOD" || component.ReviewStatus != "NOT_REVIEWED" || len(component.EvidenceIDs) != 1 || component.EvidenceIDs[0] != "photo-000" || projected.Handover.Entries[0].EvidenceIDs[0] != "evidence-meter" {
		t.Fatalf("customer projection translated codes or references: %+v / %+v", component, projected.Handover.Entries)
	}
	data, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "customer", Version: 1, Snapshot: projected, Internal: internal}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := assertV3PDFTextAndPages(t, data, 1, []string{"Observado", "Bom", "E001", "Evidência: E002", "Imagem indisponível"})
	if strings.Contains(text, "Situação não reconhecida") || strings.Contains(text, "Evidência sem índice") {
		t.Fatalf("customer PDF translated presentation values twice: %s", text)
	}
	// Customer validation still rejects broken codes and references: the
	// relaxed rule only covers the sections the allowlist removes.
	broken := projected
	broken.Checklist = &core.ChecklistSection{Rooms: []core.ChecklistRoom{{Key: "kitchen", Name: "Cozinha", Components: []core.ChecklistComponent{{Key: "floor", Label: "Piso", Status: "Observado", EvidenceIDs: []string{"E001"}}}}}}
	if core.ValidateCustomerSnapshot(broken) == nil {
		t.Fatal("customer validation accepted a translated status and a presentation evidence reference")
	}
}

func TestTask04AudienceProjectionPreservesRendererCodesAndReferences(t *testing.T) {
	snapshot := task04PDFSnapshot(1)
	evidenceID := identity.NewID().String()
	meterEvidenceID := identity.NewID().String()
	snapshot.Evidence[0].ID, snapshot.Evidence[1].ID = evidenceID, meterEvidenceID
	snapshot.Evidence[0].Availability, snapshot.Evidence[1].Availability = "MISSING", "MISSING"
	snapshot.Evidence[0].Description, snapshot.Evidence[1].Description = "", ""
	snapshot.Checklist.Rooms[0].Components[0].Key = "floor-key"
	snapshot.Checklist.Rooms[0].Components[0].EvidenceIDs = []string{evidenceID}
	snapshot.Handover.Entries[0].Key = "meter-key"
	snapshot.Handover.Entries[0].EvidenceIDs = []string{meterEvidenceID}
	for _, audience := range []string{"PDF", "PDF_CUSTOMER"} {
		t.Run(audience, func(t *testing.T) {
			projected, internal, err := projectForAudience(snapshot, audience)
			if err != nil {
				t.Fatal(err)
			}
			component := projected.Checklist.Rooms[0].Components[0]
			if projected.Checklist.Rooms[0].Key != snapshot.Checklist.Rooms[0].Key || component.Key != "floor-key" || component.Status != "OBSERVED" || component.Condition != "GOOD" || len(component.EvidenceIDs) != 1 || component.EvidenceIDs[0] != evidenceID {
				t.Fatalf("%s projection altered stable report codes or identifiers: %+v", audience, projected)
			}
			if len(projected.Handover.Entries) != 1 || projected.Handover.Entries[0].Key != "meter-key" || projected.Handover.Entries[0].EvidenceIDs[0] != meterEvidenceID {
				t.Fatalf("%s projection altered handover keys or evidence identifiers: %+v", audience, projected.Handover)
			}
			rendered, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "snapshot-v3", Version: 1, Snapshot: projected, Internal: internal}, nil)
			if err != nil {
				t.Fatal(err)
			}
			text := assertV3PDFTextAndPages(t, rendered, 1, []string{"Observado", "Bom", "E001", "Medidor", "Evidência: E002"})
			if strings.Contains(text, "Situação não reconhecida") || strings.Contains(text, "Evidência sem índice") {
				t.Fatalf("%s renderer translated presentation values twice: %s", audience, text)
			}
		})
	}
}

func task04PDFSnapshot(count int) core.Snapshot {
	snapshot := validPDFSnapshot()
	snapshot.SchemaVersion = 3
	snapshot.ReportID = "RPT-T04"
	snapshot.Classification = "NORMAL"
	snapshot.VerificationCode = "01234567890123456789012345678901"
	snapshot.Processing = &core.ProcessingState{Status: "COMPLETED"}
	snapshot.Scope = &core.ScopeSnapshot{Digest: "scope-v1", Version: 1, Profile: "PROPERTY_COMPONENTS"}
	handoverEvidence := []string{}
	if count == 1 {
		handoverEvidence = []string{"evidence-meter"}
	}
	snapshot.Handover = &core.HandoverSection{Entries: []core.HandoverEntry{{Key: "energy-meter", Kind: "METER", Status: "DECLARED", Payload: []byte(`{"meterType":"ENERGY","reading":123.4,"unit":"kWh","date":"2026-11-04"}`), Declarant: "Participante", EvidenceIDs: handoverEvidence}}}
	snapshot.Provenance = &core.Provenance{PromptRevision: "prompt-v1", ScopeDigest: "scope-v1"}
	snapshot.Checklist = &core.ChecklistSection{Coverage: core.ChecklistCoverage{Expected: 1, FullyAssessed: 1}, Rooms: []core.ChecklistRoom{{Key: "kitchen", TypeCode: "KITCHEN", Name: "Cozinha", Components: []core.ChecklistComponent{{Key: "floor", TypeCode: "FLOOR", Label: "Piso", Status: "OBSERVED", Description: "Componente observado", Condition: "GOOD", ReviewStatus: "NOT_REVIEWED"}}}}}
	snapshot.Evidence = make([]core.Evidence, 0, count)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("photo-%03d", i)
		snapshot.Evidence = append(snapshot.Evidence, core.Evidence{ID: id, RequirementKey: "roof", Role: "CURRENT", Description: "Foto ilustrativa", Availability: "AVAILABLE"})
	}
	if count > 0 {
		snapshot.Checklist.Rooms[0].Components[0].EvidenceIDs = []string{"photo-000"}
	}
	if count == 1 {
		snapshot.Evidence = append(snapshot.Evidence, core.Evidence{ID: "evidence-meter", RequirementKey: "handover:meter", Role: "CURRENT", Description: "Foto do medidor", Availability: "MISSING"})
	}
	return snapshot
}

func TestIT041NotAuthorizedNormalClassificationHasSameLimitedLabelAndNoPositiveClaimInPDF(t *testing.T) {
	snapshot := task04PDFSnapshot(0)
	snapshot.Classification = "NORMAL"
	snapshot.Processing = &core.ProcessingState{Status: "NOT_AUTHORIZED"}
	snapshot.Checklist.Coverage.UnknownScope = true
	snapshot.Checklist.Rooms[0].Components[0].Description = ""
	snapshot.Checklist.Rooms[0].Components[0].Condition = ""
	snapshot.Checklist.Rooms[0].Components[0].EvidenceIDs = nil
	customer := customerreport.ProjectChecklist(snapshot)
	if customer == nil || !customer.Limited || customer.ClassificationLabel != core.ClassificationLabelForSnapshot(snapshot) {
		t.Fatalf("screen projection label=%+v html label=%q", customer, core.ClassificationLabelForSnapshot(snapshot))
	}
	for _, audience := range []string{"PDF", "PDF_CUSTOMER"} {
		projected, internal, err := projectForAudience(snapshot, audience)
		if err != nil {
			t.Fatalf("%s projection: %v", audience, err)
		}
		rendered, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "snapshot-" + audience, Version: 1, Snapshot: projected, Internal: internal}, nil)
		if err != nil {
			t.Fatalf("%s render: %v", audience, err)
		}
		text := assertV3PDFTextAndPages(t, rendered, 1, []string{"Resultado com limitações", "Não autorizada"})
		if strings.Contains(text, "Sem alertas identificados") || strings.Contains(text, "Bom") {
			t.Fatalf("%s NOT_AUTHORIZED PDF contains a positive classification or condition claim: %s", audience, text)
		}
	}
}

func assertV3PDFTextAndPages(t *testing.T, data []byte, minPages int, want []string) string {
	t.Helper()
	if PageCount(data) < minPages {
		t.Fatalf("PDF pages=%d want >=%d", PageCount(data), minPages)
	}
	model.ConfigPath = "disable"
	conf := model.NewDefaultConfiguration()
	parsed, err := api.ReadAndValidate(bytes.NewReader(data), conf)
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for page := 1; page <= PageCount(data); page++ {
		stream, err := pdfcpu.ExtractPageContent(parsed, page)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(stream)
		if err != nil {
			t.Fatal(err)
		}
		texts := extractVVTask07PDFText(t, page, raw)
		if !hasTask04ReportBody(texts) {
			t.Fatalf("PDF page %d contains only repeating header/footer text or QR, without report body content", page)
		}
		var pageText strings.Builder
		for _, text := range texts {
			all.WriteString(text.text)
			all.WriteByte(' ')
			pageText.WriteString(text.text)
			pageText.WriteByte(' ')
		}
		if strings.Contains(pageText.String(), "Foto ilustrativa") && !strings.Contains(string(raw), " Do") {
			t.Fatalf("PDF page %d separates a readable photo caption from its image", page)
		}
	}
	for _, value := range want {
		if !strings.Contains(all.String(), value) {
			t.Errorf("PDF text does not contain %q", value)
		}
	}
	return all.String()
}

func TestUT054QROnlyPageBodyCheckRejectsHeaderAndFooterFixture(t *testing.T) {
	data := task04QROnlyPDF(t)
	if PageCount(data) != 1 {
		t.Fatalf("QR-only fixture page count=%d, want 1", PageCount(data))
	}
	model.ConfigPath = "disable"
	parsed, err := api.ReadAndValidate(bytes.NewReader(data), model.NewDefaultConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := pdfcpu.ExtractPageContent(parsed, 1)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	texts := extractVVTask07PDFText(t, 1, raw)
	if !strings.Contains(string(raw), " Do") {
		t.Fatal("QR-only fixture did not contain its QR image")
	}
	if hasTask04ReportBody(texts) {
		t.Fatalf("the generated QR-only page was accepted as report body: %+v", texts)
	}
	if !hasTask04ReportBody([]vvTask07PDFText{{text: "LAUDO INTERNO DE VISTORIA"}, {text: "Checklist descritivo · Piso observado"}, {text: "Laudo RPT-1 · versão 1"}}) {
		t.Fatal("report body content was rejected")
	}
}

func task04QROnlyPDF(t *testing.T) []byte {
	t.Helper()
	code, err := qr.Encode("https://dashboard.example.test/verify-report?code=fixture&version=1", qr.M, qr.Auto)
	if err != nil {
		t.Fatal(err)
	}
	code, err = barcode.Scale(code, 96, 96)
	if err != nil {
		t.Fatal(err)
	}
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, code); err != nil {
		t.Fatal(err)
	}
	fontBytes, err := reportFonts.ReadFile("fonts/Roboto-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	fonts, err := fontrepository.New().AddUTF8FontFromBytes("Roboto", fontstyle.Normal, fontBytes).Load()
	if err != nil {
		t.Fatal(err)
	}
	document := maroto.New(config.NewBuilder().WithPageSize(pagesize.A4).WithCustomFonts(fonts).WithDefaultFont(&props.Font{Family: "Roboto", Size: 10, Style: fontstyle.Normal}).Build())
	if err := document.RegisterHeader(text.NewAutoRow("LAUDO DE VISTORIA", props.Text{Family: "Roboto", Size: 10})); err != nil {
		t.Fatal(err)
	}
	if err := document.RegisterFooter(text.NewAutoRow("Laudo RPT-1 versão 1", props.Text{Family: "Roboto", Size: 8})); err != nil {
		t.Fatal(err)
	}
	document.AddRows(
		text.NewAutoRow("Verifique a autenticidade deste laudo", props.Text{Family: "Roboto", Size: 8}),
		row.New(2),
		marotoimage.NewFromBytesRow(18, pngBytes.Bytes(), extension.Png),
	)
	generated, err := document.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return generated.GetBytes()
}

func hasTask04ReportBody(texts []vvTask07PDFText) bool {
	for _, item := range texts {
		value := strings.TrimSpace(item.text)
		if value == "" || strings.HasPrefix(value, "LAUDO INTERNO DE VISTORIA") || strings.HasPrefix(value, "LAUDO DE VISTORIA") || strings.HasPrefix(value, "Classificação:") || strings.HasPrefix(value, "Laudo ") || strings.Contains(value, "Verifique a autenticidade deste laudo") {
			continue
		}
		return true
	}
	return false
}
