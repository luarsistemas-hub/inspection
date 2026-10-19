package core

import (
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
