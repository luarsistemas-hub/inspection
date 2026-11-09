package core

import (
	"encoding/json"
	"fmt"
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

func TestSanitizeURLsPreservesJSONEscapingInDecodedStringValues(t *testing.T) {
	notes := []string{
		`veja https://x.com/"abc"`,
		`fim https://x.com/foto`,
		`aspas https://x.com/"café" e unicode: vistoria 🏠`,
		`barras \\ antes de https://x.com/"item"`,
		"linha https://x.com/\"a\"\ncontinua",
	}
	for _, note := range notes {
		t.Run(note, func(t *testing.T) {
			snapshot := validV3Snapshot()
			payload, err := json.Marshal(map[string]string{"note": note})
			if err != nil {
				t.Fatal(err)
			}
			snapshot.Handover.Entries = []HandoverEntry{{Key: "note", Kind: "NOTE", Status: "DECLARED", Payload: payload}}
			snapshot.Checklist.Rooms[0].Components[0].Description = note
			cleaned := SanitizeURLs(snapshot)
			var decoded map[string]string
			if err := json.Unmarshal(cleaned.Handover.Entries[0].Payload, &decoded); err != nil {
				t.Fatalf("sanitized JSON is invalid: %v (%s)", err, cleaned.Handover.Entries[0].Payload)
			}
			if decoded["note"] != mutableURL.ReplaceAllString(note, "[endereço removido]") {
				t.Fatalf("note=%q, want URL-sanitized original text", decoded["note"])
			}
			if err := Validate(cleaned); err != nil {
				t.Fatalf("sanitized snapshot failed validation: %v", err)
			}
		})
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

func TestTask05R5HTMLForPDFLocalizesEvidenceAltOnlyInV3(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			var snapshot Snapshot
			requirementKey := "overview"
			switch version {
			case 1:
				snapshot = validSnapshot()
			case 2:
				snapshot = v2Snapshot()
			case 3:
				snapshot = validV3Snapshot()
				requirementKey = "env:kitchen"
			}
			snapshot.Requirements = []Requirement{{Key: requirementKey, Section: "imóvel", Label: "Visão geral", AnalysisMode: "COMPARE_ORIGIN_CURRENT", AnalysisStatus: "COMPLETED"}}
			if version < 3 {
				snapshot.Evidence = []Evidence{{ID: "evidence-1", RequirementKey: requirementKey, Role: "CURRENT", Description: "Sala"}}
			} else {
				snapshot.Evidence[0].Description = "Sala"
			}
			html, err := HTMLForPDF(snapshot, map[string]bool{"evidence-1": true}, true)
			if err != nil {
				t.Fatal(err)
			}
			want := `alt="CURRENT · Sala"`
			if version >= 3 {
				want = `alt="Vistoria atual · Sala"`
			}
			if !strings.Contains(string(html), want) {
				t.Fatalf("HTML v%d missing %q: %s", version, want, html)
			}
		})
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

func TestDescriptiveHTMLUsesSharedPortugueseVocabularyAndNumberedEvidence(t *testing.T) {
	snapshot := validSnapshot()
	snapshot.SchemaVersion = 3
	snapshot.Checklist = &ChecklistSection{Coverage: ChecklistCoverage{Expected: 1, FullyAssessed: 1}, Rooms: []ChecklistRoom{{Key: "kitchen", Name: "Cozinha", Components: []ChecklistComponent{{Label: "Piso", Status: "OBSERVED", Condition: "GOOD", ReviewStatus: "NOT_REVIEWED", EvidenceIDs: []string{"photo-id"}}}}}}
	snapshot.Processing = &ProcessingState{Status: "NOT_AUTHORIZED"}
	snapshot.Scope = &ScopeSnapshot{Digest: "scope", Version: 1, Profile: "PROPERTY_COMPONENTS"}
	snapshot.Handover = &HandoverSection{}
	snapshot.Provenance = &Provenance{PromptRevision: "prompt-v1", ScopeDigest: "scope"}
	snapshot.Evidence = []Evidence{{ID: "photo-id", RequirementKey: "room", Role: "CURRENT", Availability: "AVAILABLE"}}
	html, err := HTML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Observado", "Bom", "Não revisado", "Não autorizada", "Evidência: E001", "E001 · Vistoria atual"} {
		if !strings.Contains(string(html), want) {
			t.Errorf("HTML missing shared label/reference %q: %s", want, html)
		}
	}
	if strings.Contains(string(html), "Evidência: photo-id") || strings.Contains(string(html), " · GOOD") {
		t.Fatalf("HTML exposed raw evidence reference or condition code: %s", html)
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

func TestCaptureProvenanceFlagsHaveNeutralPortugueseLabels(t *testing.T) {
	for code, want := range map[string]string{
		"CAPTURE_TIME_CONFLICT":     "Horário da captura divergente",
		"CAPTURE_TIME_DEVICE_LOCAL": "Horário local do dispositivo",
		"GALLERY_SOURCE":            "Imagem da galeria",
	} {
		if got := FlagLabel(code); got != want {
			t.Errorf("FlagLabel(%q)=%q, want %q", code, got, want)
		}
	}
}

func validV3Snapshot() Snapshot {
	snapshot := validSnapshot()
	snapshot.SchemaVersion = 3
	snapshot.Evidence = []Evidence{{ID: "evidence-1", RequirementKey: "env:kitchen", Role: "CURRENT"}}
	snapshot.Checklist = &ChecklistSection{Rooms: []ChecklistRoom{{Key: "kitchen", TypeCode: "KITCHEN", Name: "Cozinha", Components: []ChecklistComponent{{Key: "floor", Label: "Piso", Status: "NOT_OBSERVED", ReviewStatus: "NOT_REVIEWED", EvidenceIDs: []string{}}}}}, Coverage: ChecklistCoverage{Expected: 1, NotObserved: 1}}
	snapshot.Processing = &ProcessingState{Status: "COMPLETED"}
	snapshot.Scope = &ScopeSnapshot{Digest: "scope-digest", Version: 1, Profile: "PROPERTY_COMPONENTS"}
	snapshot.Handover = &HandoverSection{Entries: []HandoverEntry{{Key: "meter", Kind: "METER", Status: "PENDING"}}}
	snapshot.Provenance = &Provenance{PromptRevision: "prompt-v1", ModelRevision: "unknown", ScopeDigest: "scope-digest"}
	snapshot.VerificationCode = "random-verification-code-at-least-128-bits"
	return snapshot
}

// TestUT043UT044UT049UT110UT113UT115BuildChecklistContracts validates descriptive facts, explicit limits and conflicts.
func TestUT043UT044UT049UT110UT113UT115BuildChecklistContracts(t *testing.T) {
	snapshot := validV3Snapshot()
	if err := Validate(snapshot); err != nil {
		t.Fatalf("v3 snapshot rejected: %v", err)
	}
	html, err := HTML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Checklist descritivo", "1 componentes esperados", "Piso", "Não observado", "Chaves, controles e medidores", "Pendente", "Falha na análise"} {
		if strings.Contains(string(html), want) == (want == "Falha na análise") {
			t.Errorf("HTML presence for %q incorrect", want)
		}
	}
	unknown := validV3Snapshot()
	unknown.Checklist.Coverage.UnknownScope = true
	unknown.Checklist.Coverage.Expected = 0
	unknown.Checklist.Coverage.NotObserved = 0
	html, err = HTML(unknown)
	if err != nil || !strings.Contains(string(html), "Escopo não definido") {
		t.Fatalf("unknown scope must avoid a percentage: %v", err)
	}
	unsafe := validV3Snapshot()
	unsafe.Checklist.Rooms[0].Components[0].EvidenceIDs = []string{"foreign"}
	if err := Validate(unsafe); err == nil {
		t.Fatal("foreign checklist evidence accepted")
	}
}

func TestV1AndV2SnapshotsRemainDecodableAlongsideV3(t *testing.T) {
	for _, snapshot := range []Snapshot{validSnapshot(), v2Snapshot(), validV3Snapshot()} {
		data, _, err := CanonicalJSON(snapshot)
		if err != nil {
			t.Fatalf("schema %d encode: %v", snapshot.SchemaVersion, err)
		}
		var decoded Snapshot
		if err := json.Unmarshal(data, &decoded); err != nil || decoded.SchemaVersion != snapshot.SchemaVersion {
			t.Fatalf("schema %d decode=%d err=%v", snapshot.SchemaVersion, decoded.SchemaVersion, err)
		}
	}
}

func TestUT110HTMLRendersTypedHandoverFieldsAndPendingDoesNotInventAReading(t *testing.T) {
	snapshot := validV3Snapshot()
	snapshot.Handover = &HandoverSection{Entries: []HandoverEntry{
		{Key: "private-meter-key", Kind: "METER", Status: "DECLARED", Payload: json.RawMessage(`{"meterType":"ENERGY","identifier":"A-1","reading":123.4,"unit":"kWh","date":"2026-11-04"}`), Declarant: "Participante", EvidenceIDs: []string{"evidence-1"}},
		{Key: "required-meter", Kind: "METER", Status: "PENDING"},
	}}
	html, err := HTML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Medidor", "Energia", "123.4", "kWh", "2026-11-04", "Declarante: Participante", "Evidência: E001", "Pendente"} {
		if !strings.Contains(string(html), expected) {
			t.Errorf("handover HTML missing %q", expected)
		}
	}
	if strings.Contains(string(html), "private-meter-key") || strings.Contains(string(html), "required-meter") || strings.Contains(string(html), "Pendente · ·") {
		t.Fatalf("handover HTML leaked internal keys or invented a pending reading: %s", html)
	}
}

func TestHandoverNoteRendersOnlyForInternalAudience(t *testing.T) {
	snapshot := validSnapshot()
	snapshot.SchemaVersion = 3
	snapshot.Checklist = &ChecklistSection{Rooms: []ChecklistRoom{}, Coverage: ChecklistCoverage{UnknownScope: true}}
	snapshot.Processing = &ProcessingState{Status: "COMPLETED"}
	snapshot.Scope = &ScopeSnapshot{Digest: "scope", Version: 1, Profile: "PROPERTY_COMPONENTS"}
	snapshot.Handover = &HandoverSection{Entries: []HandoverEntry{{Key: "observation", Kind: "NOTE", Status: "DECLARED", Payload: []byte(`{"note":"Pintura retocada na parede."}`)}}}
	snapshot.Provenance = &Provenance{PromptRevision: "prompt-v1", ScopeDigest: "scope"}
	internal, err := HTMLForPDF(snapshot, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	customer, err := HTMLForPDF(snapshot, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(internal), "Observação: Pintura retocada na parede.") || strings.Contains(string(customer), "Pintura retocada na parede") {
		t.Fatalf("NOTE audience allowlist mismatch: internal=%s customer=%s", internal, customer)
	}
}

// TestSanitizeURLsDecodesRawPayloadsBeforeReplacing feeds raw JSON with the
// escapes that broke the text-level replacement (escaped quotes right after a
// URL, backslashes, \n, \u escapes) and checks that only decoded string values
// change, numbers keep their literal form and URL-free payloads stay intact.
func TestSanitizeURLsDecodesRawPayloadsBeforeReplacing(t *testing.T) {
	cases := map[string]struct {
		raw, want string
	}{
		"escaped quotes after url": {`{"note":"veja https://x.com/\"abc\""}`, "veja [endereço removido]\"abc\""},
		"url at end":               {`{"note":"fim https://x.com/foto"}`, "fim [endereço removido]"},
		"backslashes":              {`{"note":"barra \\ e https://x.com/a\\b fim"}`, `barra \ e [endereço removido] fim`},
		"newline and unicode":      {`{"note":"linha https://x.com/a\nproxima café 🏠"}`, "linha [endereço removido]\nproxima café 🏠"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			snapshot := validV3Snapshot()
			snapshot.Handover.Entries = []HandoverEntry{{Key: "note", Kind: "NOTE", Status: "DECLARED", Payload: json.RawMessage(tc.raw)}}
			cleaned := SanitizeURLs(snapshot)
			var decoded map[string]string
			if err := json.Unmarshal(cleaned.Handover.Entries[0].Payload, &decoded); err != nil {
				t.Fatalf("sanitized payload is invalid JSON: %v (%s)", err, cleaned.Handover.Entries[0].Payload)
			}
			if decoded["note"] != tc.want {
				t.Fatalf("note=%q want %q", decoded["note"], tc.want)
			}
			if err := Validate(cleaned); err != nil {
				t.Fatalf("sanitized snapshot failed validation: %v", err)
			}
			if _, _, err := CanonicalJSON(cleaned); err != nil {
				t.Fatalf("sanitized snapshot cannot be serialized: %v", err)
			}
		})
	}
	snapshot := validV3Snapshot()
	untouched := json.RawMessage(`{"keyType":"FRONT_DOOR","quantity":2,"reading":123.40,"big":12345678901234567890}`)
	withURL := json.RawMessage(`{"meterType":"ENERGY","reading":123.40,"note":"https://x.com/"}`)
	snapshot.Handover.Entries = []HandoverEntry{{Key: "keys", Kind: "KEY", Status: "DECLARED", Payload: untouched}, {Key: "meter", Kind: "METER", Status: "DECLARED", Payload: withURL}}
	cleaned := SanitizeURLs(snapshot)
	if string(cleaned.Handover.Entries[0].Payload) != string(untouched) {
		t.Fatalf("URL-free payload was re-encoded: %s", cleaned.Handover.Entries[0].Payload)
	}
	if got := string(cleaned.Handover.Entries[1].Payload); !strings.Contains(got, `"reading":123.40`) || strings.Contains(got, "https://") {
		t.Fatalf("payload numbers or URL handling changed: %s", got)
	}
}

func TestTask07ChecklistHTMLShowsAssessmentExtentAndOutOfScopeOnlyInternally(t *testing.T) {
	snapshot := validV3Snapshot()
	component := &snapshot.Checklist.Rooms[0].Components[0]
	component.AssessedScope = "Frentes visíveis"
	component.NotAssessed = []string{"Parte superior"}
	component.OutsideScope = true
	internalHTML, err := HTMLForPDF(snapshot, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Extensão avaliada: Frentes visíveis", "Partes não avaliadas: Parte superior", "Observado fora do escopo"} {
		if !strings.Contains(string(internalHTML), want) {
			t.Errorf("internal HTML missing %q", want)
		}
	}
	customerHTML, err := HTMLForPDF(snapshot, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(customerHTML), "Observado fora do escopo") {
		t.Fatal("customer HTML exposed an internal outside-scope observation")
	}
}
