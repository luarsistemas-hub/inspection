package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	analysis "inspection/services/inspection/internal/features/analysis/core"
)

// TestAIP_UT_036CanonicalizationYieldsStableDigestAndPinnedVersions covers
// UT-036 of autonomous-inspection-platform.
func TestAIP_UT_036CanonicalizationYieldsStableDigestAndPinnedVersions(t *testing.T) {
	snapshot := validSnapshot()
	snapshot.Coverage = map[string]string{"zeta": "2", "alpha": "1", "mid": "3"}
	snapshot.ReasonCodes = []string{"EVIDENCE_FLAG", "OBSERVED_CHANGE"}
	data, digest, err := CanonicalJSON(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest %s is not the SHA-256 of the canonical bytes", digest)
	}
	for i := 0; i < 20; i++ {
		again, againDigest, err := CanonicalJSON(snapshot)
		if err != nil || againDigest != digest || string(again) != string(data) {
			t.Fatalf("canonical output changed on repetition %d: %v", i, err)
		}
	}
	var decoded struct {
		TemplateVersionID  string `json:"templateVersionId"`
		ReferenceVersionID string `json:"referenceVersionId"`
		PromptDigest       string `json:"promptDigest"`
		SchemaVersion      int    `json:"schemaVersion"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.TemplateVersionID != "template-v1" || decoded.ReferenceVersionID != "reference-v1" || decoded.PromptDigest != "prompt-digest-v1" || decoded.SchemaVersion != 1 {
		t.Fatalf("pinned versions were not recorded in the canonical snapshot: %+v", decoded)
	}
	// Any pinned version change yields a different immutable identity.
	for name, change := range map[string]func(*Snapshot){
		"template":  func(s *Snapshot) { s.TemplateVersionID = "template-v2" },
		"reference": func(s *Snapshot) { s.ReferenceVersionID = "reference-v2" },
		"prompt":    func(s *Snapshot) { s.PromptDigest = "prompt-digest-v2" },
	} {
		changed := snapshot
		change(&changed)
		if _, other, err := CanonicalJSON(changed); err != nil || other == digest {
			t.Fatalf("changing the pinned %s version kept digest %s (err=%v)", name, digest, err)
		}
	}
	if html, err := HTML(snapshot); err != nil || strings.Contains(strings.ToLower(string(html)), "http://") {
		t.Fatalf("HTML derived from the snapshot must stay self-contained: %v", err)
	}
}

// TestAIP_UT_037ReportRejectsMutableURLAndMissingVersionOrDigest covers the
// validation half of UT-037. The published-mutation half runs against
// PostgreSQL in features/reports/generate_snapshot.
func TestAIP_UT_037ReportRejectsMutableURLAndMissingVersionOrDigest(t *testing.T) {
	for name, change := range map[string]func(*Snapshot){
		"mutable URL in coverage": func(s *Snapshot) { s.Coverage = map[string]string{"evidence": "HTTPS://object-store/private"} },
		"mutable URL in evidence flag": func(s *Snapshot) {
			s.Evidence = []Evidence{{ID: "e1", RequirementKey: "k", Flags: []string{"http://x/y"}}}
		},
		"missing template version":    func(s *Snapshot) { s.TemplateVersionID = " " },
		"missing reference version":   func(s *Snapshot) { s.ReferenceVersionID = "" },
		"missing prompt digest":       func(s *Snapshot) { s.PromptDigest = "" },
		"missing schema version":      func(s *Snapshot) { s.SchemaVersion = 0 },
		"missing report identity":     func(s *Snapshot) { s.ReportID = "" },
		"missing inspection identity": func(s *Snapshot) { s.InspectionID = "" },
		"unknown classification":      func(s *Snapshot) { s.Classification = "UNKNOWN" },
		"missing advisory statement":  func(s *Snapshot) { s.Advisory = "" },
		"blank coverage key":          func(s *Snapshot) { s.Coverage = map[string]string{" ": "x"} },
		"finding without evidence reference": func(s *Snapshot) {
			s.Findings = []Finding{{Category: "CONSERVATION", Title: "t", Description: "d", Quality: "ADEQUATE", RecommendedAction: "a", Confidence: .9}}
		},
		"video frame without exact lineage": func(s *Snapshot) {
			s.Evidence = []Evidence{{ID: "f", RequirementKey: "k", EvidenceKind: "VIDEO_FRAME", FrameID: "f"}}
		},
		"requirement with unknown mode": func(s *Snapshot) {
			s.Requirements = []Requirement{{Key: "k", Section: "s", Label: "l", AnalysisMode: "X", AnalysisStatus: "COMPLETED"}}
		},
		"requirement with unknown status": func(s *Snapshot) {
			s.Requirements = []Requirement{{Key: "k", Section: "s", Label: "l", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "X"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := validSnapshot()
			change(&snapshot)
			if data, digest, err := CanonicalJSON(snapshot); err == nil || data != nil || digest != "" {
				t.Fatalf("invalid snapshot produced canonical output: digest=%q err=%v", digest, err)
			}
		})
	}
	if _, digest, err := CanonicalJSON(validSnapshot()); err != nil || digest == "" {
		t.Fatalf("control snapshot rejected: %v", err)
	}
}

// aipClassifierReasonCodes collects every reason code the deterministic
// classifier can emit, by exercising each condition once.
func aipClassifierReasonCodes() []string {
	var all []analysis.ComparisonFacts
	all = append(all, analysis.ComparisonFacts{}, analysis.ComparisonFacts{Terminal: true, Inconclusive: true, TechnicalFailure: true, Missing: true, Skipped: true, Flagged: true, Uncorrected: true, Findings: []analysis.Finding{{Severity: "LOW"}, {Severity: "CRITICAL"}}})
	decisions := []analysis.Decision{analysis.Classify(nil), analysis.Classify(all)}
	decisions = append(decisions, analysis.ClassifyInventory(analysis.Decision{Classification: analysis.ClassificationNormal}, analysis.InventoryFacts{Missing: 1, Worsened: 1, NotObserved: 1, EnvironmentsNotCaptured: 1}))
	seen := map[string]bool{}
	var codes []string
	for _, decision := range decisions {
		for _, code := range decision.ReasonCodes {
			if !seen[code] {
				seen[code] = true
				codes = append(codes, code)
			}
		}
	}
	return codes
}

// TestAIP_IT_262EveryClassifierReasonCodeIsExplainedInPortugueseByTheReport
// covers the explanation half of IT-262: a report without findings must still
// say why its classification is NORMAL or ATTENTION, so no reason code may
// fall back to the generic unrecognized label.
func TestAIP_IT_262EveryClassifierReasonCodeIsExplainedInPortugueseByTheReport(t *testing.T) {
	codes := aipClassifierReasonCodes()
	if len(codes) < 10 {
		t.Fatalf("expected the classifier to emit its full vocabulary, got %v", codes)
	}
	for _, code := range codes {
		if label := presentReason(code); label == "Situação não reconhecida" || label == code {
			t.Errorf("reason %s has no Portuguese report label (%q)", code, label)
		}
	}
	snapshot := validSnapshot()
	snapshot.ReasonCodes = codes
	html, err := HTML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), "Situação não reconhecida") {
		t.Fatal("the report HTML shows an unrecognized reason")
	}
	if !strings.Contains(string(html), "Nenhuma constatação foi registrada") {
		t.Fatal("a report without findings must state that no finding was recorded")
	}
}

// TestReasonLabelIsTheSingleCanonicalVocabulary pins the texts shared by the
// HTML report, the PDF and the dashboard (triage-journey.tsx) for the codes
// that used to diverge, and refuses to print an unknown code.
func TestReasonLabelIsTheSingleCanonicalVocabulary(t *testing.T) {
	for code, want := range map[string]string{
		"INCONCLUSIVE":     "Análise inconclusiva",
		"OBSERVED_CHANGE":  "Alteração observada",
		"CRITICAL_FINDING": "Achado crítico",
	} {
		if got := ReasonLabel(code); got != want {
			t.Errorf("ReasonLabel(%s) = %q, want %q", code, got, want)
		}
		if got := presentReason(code); got != want {
			t.Errorf("HTML label for %s = %q, want %q", code, got, want)
		}
	}
	if got := ReasonLabel("SOMETHING_NEW"); got != "Situação não reconhecida" {
		t.Errorf("unknown code must not leak, got %q", got)
	}
}

// TestDashboardTriageReasonLabelsMatchTheReportVocabulary keeps the dashboard
// (apps/dashboard triage-journey.tsx) on the same text as ReasonLabel for every
// code the classifier emits.
func TestDashboardTriageReasonLabelsMatchTheReportVocabulary(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "apps", "dashboard", "src", "features", "dashboard", "triage-journey.tsx"))
	if err != nil {
		t.Skipf("dashboard source not available: %v", err)
	}
	block := regexp.MustCompile(`export const reasonLabels[^=]*=\s*\{([^}]*)\}`).FindSubmatch(source)
	if block == nil {
		t.Fatal("reasonLabels map not found in triage-journey.tsx")
	}
	dashboard := map[string]string{}
	for _, pair := range regexp.MustCompile(`([A-Z_]+):"([^"]+)"`).FindAllSubmatch(block[1], -1) {
		dashboard[string(pair[1])] = string(pair[2])
	}
	for _, code := range aipClassifierReasonCodes() {
		if got, want := dashboard[code], ReasonLabel(code); got != want {
			t.Errorf("dashboard label for %s = %q, report says %q", code, got, want)
		}
	}
}
