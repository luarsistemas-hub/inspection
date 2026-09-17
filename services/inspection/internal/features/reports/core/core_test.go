package core

import (
	"strings"
	"testing"
)

func validSnapshot() Snapshot {
	return Snapshot{SchemaVersion: 1, ReportID: "report-1", InspectionID: "inspection-1", TemplateVersionID: "template-v1", ReferenceVersionID: "reference-v1", ProfileVersionID: "profile-v1", Mode: "HISTORICAL", Classification: "ATTENTION", Context: Context{Asset: AssetContext{ID: "asset-1", Name: "Apartamento 12", ExternalKey: "APT-12", Address: "Rua Exemplo, 12"}, Participant: ParticipantContext{ID: "participant-1", Name: "Pessoa responsável"}, Template: TemplateContext{ID: "template-1", Name: "Vistoria periódica", Version: 1}, Inspection: InspectionContext{GeneratedAt: "2026-09-16T00:00:00Z"}}, Advisory: "Esta triagem interna não atribui culpa."}
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
	snapshot.Requirements = []Requirement{{Key: "overview", Section: "imóvel", Label: "Visão geral"}}
	snapshot.Evidence = []Evidence{{ID: "evidence-1", RequirementKey: "overview", Role: "CURRENT", Description: "Sala"}, {ID: "evidence-2", RequirementKey: "overview", Role: "REFERENCE", Description: "Origem"}}
	html, err := HTMLForPDF(snapshot, map[string]bool{"evidence-1": true, "evidence-2": false}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `src="evidence-evidence-1.jpg"`) || !strings.Contains(string(html), "Imagem indisponível") {
		t.Fatalf("expected local image and missing marker: %s", html)
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
