package core

import (
	"os"
	"strings"
	"testing"
)

func validSnapshot() Snapshot {
	return Snapshot{SchemaVersion: 1, ReportID: "report-1", InspectionID: "inspection-1", TemplateVersionID: "template-v1", ReferenceVersionID: "reference-v1", PromptDigest: "prompt-digest-v1", Mode: "HISTORICAL", Classification: "ATTENTION", Context: Context{Asset: AssetContext{ID: "asset-1", Name: "Apartamento 12", ExternalKey: "APT-12", Address: "Rua Exemplo, 12"}, Participant: ParticipantContext{ID: "participant-1", Name: "Pessoa responsável"}, Template: TemplateContext{ID: "template-1", Name: "Vistoria periódica", Version: 1}, Inspection: InspectionContext{GeneratedAt: "2026-09-16T00:00:00Z"}}, Advisory: "Esta triagem interna não atribui culpa."}
}

func TestSnapshotRejectsMutableURLs(t *testing.T) {
	snapshot := validSnapshot()
	snapshot.Coverage = map[string]string{"evidence": "https://object-store/private"}
	if _, _, err := CanonicalJSON(snapshot); err == nil {
		t.Fatal("mutable URL accepted in immutable snapshot")
	}
}

func TestHTMLForPDFIncludesLocalImagesAndMissingMarker(t *testing.T) {
	snapshot := validSnapshot()
	unchanged := true
	snapshot.Requirements = []Requirement{{Key: "overview", Section: "imóvel", Label: "Visão geral", AnalysisMode: "COMPARE_ORIGIN_CURRENT", AnalysisStatus: "COMPLETED", NoRelevantChange: &unchanged}}
	snapshot.Evidence = []Evidence{{ID: "evidence-1", RequirementKey: "overview", Role: "CURRENT", Description: "Sala"}, {ID: "evidence-2", RequirementKey: "overview", Role: "REFERENCE", Description: "Origem"}}
	html, err := HTMLForPDF(snapshot, map[string]bool{"evidence-1": true, "evidence-2": false}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `src="evidence-evidence-1.jpg"`) || !strings.Contains(string(html), "Imagem indisponível") {
		t.Fatalf("expected local image and missing marker: %s", html)
	}
	if !strings.Contains(string(html), "Sem alteração relevante identificada") || !strings.Contains(string(html), "não confirma a ausência de problemas") {
		t.Fatalf("expected neutral analysis summary: %s", html)
	}
}

func TestCanonicalJSONIsStable(t *testing.T) {
	first, firstDigest, err := CanonicalJSON(validSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	second, secondDigest, err := CanonicalJSON(validSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || firstDigest != secondDigest {
		t.Fatal("canonical snapshot changed")
	}
}

func TestVideoFrameEvidenceRequiresExactImmutableLineage(t *testing.T) {
	snapshot := validSnapshot()
	timestamp := int64(70123)
	ptsMicros := int64(70123456)
	snapshot.Evidence = []Evidence{{ID: "frame-1", RequirementKey: "video:sink", Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: "frame-1", SourceMediaID: "source-1", PreparationID: "prep-1", PTSMicros: &ptsMicros, TimestampMS: &timestamp, FrameDigest: strings.Repeat("a", 64), SourceAvailability: "EXPIRED"}}
	if err := Validate(snapshot); err != nil {
		t.Fatalf("valid retained frame evidence rejected: %v", err)
	}
	data, _, err := CanonicalJSON(snapshot)
	if err != nil || !strings.Contains(string(data), `"ptsMicros":70123456`) || !strings.Contains(string(data), `"timestampMs":70123`) {
		t.Fatalf("canonical snapshot lost exact PTS or display timestamp: %s, %v", data, err)
	}
	snapshot.Evidence[0].TimestampMS = ptrInt64(70124)
	if err := Validate(snapshot); err == nil {
		t.Fatal("frame with inconsistent display timestamp accepted")
	}
	snapshot.Evidence[0].TimestampMS = nil
	if err := Validate(snapshot); err == nil {
		t.Fatal("frame without exact source timestamp accepted")
	}
}

func ptrInt64(value int64) *int64 { return &value }

func TestHTMLIsInternalAndEscaped(t *testing.T) {
	snapshot := validSnapshot()
	snapshot.Advisory = "<script>unsafe</script>"
	html, err := HTML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), "<script>") {
		t.Fatal("unescaped report text")
	}
	if !strings.Contains(string(html), "Laudo interno de vistoria") {
		t.Fatal("missing localized report marker")
	}
	if presentClassification("UNKNOWN") != "Situação não reconhecida" || presentSeverity("UNKNOWN") != "Situação não reconhecida" {
		t.Fatal("unknown report codes must use the safe fallback")
	}
}

func TestInventorySnapshotV2RendersProblemsBeforeEvidenceAndValidatesPairs(t *testing.T) {
	snapshot := validSnapshot()
	snapshot.SchemaVersion = 2
	snapshot.Evidence = []Evidence{{ID: "origin", RequirementKey: "env:kitchen", Role: "REFERENCE"}, {ID: "current", RequirementKey: "env:kitchen", Role: "CURRENT"}}
	snapshot.Inventory = &InventorySection{Reference: InventoryReference{VersionID: snapshot.ReferenceVersionID, VersionNumber: 3, Purpose: "MOVE_IN", ActivatedAt: "2026-01-01T00:00:00Z"}, Problems: []InventoryEntry{{ItemIdentityID: "item-1", Environment: "Cozinha", Type: "SINK", Name: "Pia", Status: "MISSING", Pairs: []InventoryEvidencePair{{Origin: "origin", Current: "current", Decision: "SAME_VIEW"}}}}}
	html, err := HTML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "Problemas do inventário") || !strings.Contains(string(html), "Pia") {
		t.Fatalf("inventory report section missing: %s", html)
	}
	if strings.Contains(string(html), "item-1") || strings.Contains(string(html), "SAME_VIEW") || strings.Contains(string(html), "MISSING") {
		t.Fatal("customer-facing inventory text must not expose internal IDs or raw matcher/status codes")
	}
	if !strings.Contains(string(html), "Item ausente") || !strings.Contains(string(html), "Referência") || !strings.Contains(string(html), "Vistoria atual") {
		t.Fatal("inventory pair must render localized labels and side-by-side evidence")
	}
	if strings.Index(string(html), "Problemas do inventário") > strings.Index(string(html), "Evidências") {
		t.Fatal("inventory exceptions must precede evidence")
	}
	snapshot.Inventory.Problems[0].Pairs[0].Current = "unknown"
	if err := Validate(snapshot); err == nil {
		t.Fatal("pair referencing unknown evidence was accepted")
	}
}

func TestLegacySnapshotHTMLRemainsDeterministic(t *testing.T) {
	first, err := HTML(validSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	second, err := HTML(validSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("v1 HTML changed between renders")
	}
}

func v2Snapshot() Snapshot {
	snapshot := validSnapshot()
	snapshot.SchemaVersion = 2
	snapshot.Classification = "NORMAL"
	snapshot.Inventory = &InventorySection{Reference: InventoryReference{VersionID: "version-1", VersionNumber: 2, Purpose: "MOVE_IN", ActivatedAt: "2026-01-01T00:00:00Z"}, Coverage: &InventoryCoverage{ExpectedItems: 4, AssessedItems: 4, EnvironmentsNotCaptured: []string{}, EnvironmentsAddedDuringCapture: []string{}}, Problems: []InventoryEntry{}, Informational: []InventoryEntry{}}
	return snapshot
}

func TestUT266NormalInventoryRendersSummaryOnly(t *testing.T) {
	html, err := HTML(v2Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `id="inventory-summary"`) || strings.Contains(string(html), `id="inventory-problems"`) || !strings.Contains(string(html), "4 de 4 itens avaliados") {
		t.Fatalf("NORMAL inventory report must render only the summary: %s", html)
	}
}

func TestUT267V1SnapshotRendersByteIdenticalToPreviousRenderer(t *testing.T) {
	golden, err := os.ReadFile("testdata/v1_report.golden.html")
	if err != nil {
		t.Fatal(err)
	}
	html, err := HTML(validSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if string(html) != string(golden) {
		t.Fatalf("v1 HTML differs from the pre-change renderer fixture:\n%s", html)
	}
}

func TestUT269ValidateRejectsIncompleteV2(t *testing.T) {
	if err := Validate(v2Snapshot()); err != nil {
		t.Fatalf("valid v2 rejected: %v", err)
	}
	missingReference := v2Snapshot()
	missingReference.Inventory.Reference.VersionID = ""
	unknownEvidence := v2Snapshot()
	unknownEvidence.Inventory.Problems = []InventoryEntry{{Environment: "Sala", Type: "SOFA", Name: "Sofá", Status: "MISSING", Pairs: []InventoryEvidencePair{{Origin: "nowhere", Current: "nobody", Decision: "SAME_VIEW"}}}}
	noSection := v2Snapshot()
	noSection.Inventory = nil
	comparativeWithoutPurpose := v2Snapshot()
	comparativeWithoutPurpose.Inventory.Reference.Purpose = ""
	for name, snapshot := range map[string]Snapshot{"missing reference": missingReference, "unknown evidence": unknownEvidence, "v2 without section": noSection, "comparative without purpose": comparativeWithoutPurpose} {
		if err := Validate(snapshot); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestCatalogingSectionValidatesWithoutCoverageOrPurpose(t *testing.T) {
	snapshot := v2Snapshot()
	snapshot.Inventory = &InventorySection{Reference: InventoryReference{VersionID: "draft-1", VersionNumber: 1}, Problems: []InventoryEntry{}, Informational: []InventoryEntry{}}
	if err := Validate(snapshot); err != nil {
		t.Fatalf("cataloging section rejected: %v", err)
	}
	html, err := HTML(snapshot)
	if err != nil || !strings.Contains(string(html), "Inventário catalogado nesta vistoria") {
		t.Fatalf("cataloging summary missing: %v %s", err, html)
	}
}
