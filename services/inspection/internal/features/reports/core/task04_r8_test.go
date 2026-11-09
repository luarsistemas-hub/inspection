package core

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func r8V3Snapshot() Snapshot {
	return Snapshot{
		SchemaVersion: 3, ReportID: "r", InspectionID: "i", TemplateVersionID: "t", ReferenceVersionID: "ref", PromptDigest: "p", Mode: "HISTORICAL", Classification: "NORMAL", Advisory: "adv", ReasonCodes: []string{}, Coverage: map[string]string{},
		Context:    Context{Asset: AssetContext{ID: "a", Name: "A", Address: "Rua"}, Participant: ParticipantContext{ID: "p", Name: "P"}, Template: TemplateContext{ID: "t", Name: "T", Version: 1}, Inspection: InspectionContext{GeneratedAt: "2026"}},
		Checklist:  &ChecklistSection{Rooms: []ChecklistRoom{}, Coverage: ChecklistCoverage{UnknownScope: true}},
		Processing: &ProcessingState{Status: "COMPLETED"},
		Scope:      &ScopeSnapshot{Unknown: true},
		Handover:   &HandoverSection{Entries: []HandoverEntry{}},
		Provenance: &Provenance{},
	}
}

// Item 2 (round 8): a scheme without a host ("https://", "HTTP:// ") is
// rejected by the validation, so the sanitizer must remove it too; otherwise a
// participant note blocks the report forever.
func TestR8BareURLSchemesAreSanitizedWhereValidationRejectsThem(t *testing.T) {
	notes := []string{"veja https:// depois", "link: http://", `aspas "https://"`, "HTTPS://", "Http:// ", "termina em https://", "https:\\/\\/escapado"}
	for _, note := range notes {
		payload, err := json.Marshal(map[string]any{"note": note, "reading": json.Number("1.50")})
		if err != nil {
			t.Fatal(err)
		}
		scope, err := json.Marshal(map[string]any{"rooms": []any{}, "note": note})
		if err != nil {
			t.Fatal(err)
		}
		snapshot := r8V3Snapshot()
		snapshot.Handover = &HandoverSection{Entries: []HandoverEntry{{Key: "n", Kind: "NOTE", Status: "DECLARED", Payload: payload, Declarant: note}}}
		snapshot.Scope.Document = scope
		snapshot.Coverage = map[string]string{"roof": note}
		snapshot.Evidence = []Evidence{{ID: "e", RequirementKey: "roof", Role: "CURRENT", Flags: []string{note}, Description: note}}
		if Validate(snapshot) == nil && containsMutableURL(note) {
			t.Fatalf("fixture %q should be rejected before sanitization", note)
		}
		sanitized := SanitizeURLs(snapshot)
		if err := Validate(sanitized); err != nil {
			t.Errorf("note %q still blocks issuance after sanitization: %v (payload=%s scope=%s)", note, err, sanitized.Handover.Entries[0].Payload, sanitized.Scope.Document)
		}
		if _, _, err := CanonicalJSON(sanitized); err != nil {
			t.Errorf("note %q: canonical JSON failed: %v", note, err)
		}
		if !strings.Contains(string(sanitized.Handover.Entries[0].Payload), `"reading":1.50`) {
			t.Errorf("note %q: numbers changed: %s", note, sanitized.Handover.Entries[0].Payload)
		}
	}
}

// Item 9b (round 8): SanitizeURLs takes the snapshot by value and must not
// change the caller's maps, slices or sections.
func TestR8SanitizeURLsDoesNotMutateCallerSnapshot(t *testing.T) {
	snapshot := r8V3Snapshot()
	snapshot.Coverage = map[string]string{"roof": "https://cdn.example/a.jpg"}
	snapshot.Scope.Document = json.RawMessage(`{"note":"https://x"}`)
	snapshot.Handover.Entries = []HandoverEntry{{Key: "n", Kind: "NOTE", Status: "DECLARED", Payload: json.RawMessage(`{"note":"http://y"}`), Declarant: "https://z"}}
	snapshot.Evidence = []Evidence{{ID: "e", RequirementKey: "roof", Description: "https://d", Flags: []string{"https://f"}}}
	snapshot.Findings = []Finding{{Title: "https://t", Description: "https://d", RecommendedAction: "https://a"}}
	snapshot.Checklist.Rooms = []ChecklistRoom{{Key: "k", Name: "K", Components: []ChecklistComponent{{Label: "L", Status: "OBSERVED", Description: "https://c", Limitations: []string{"https://l"}}}}}
	before, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	original := snapshot
	sanitized := SanitizeURLs(snapshot)
	after, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("SanitizeURLs changed the caller's snapshot:\nbefore %s\nafter  %s", before, after)
	}
	if reflect.DeepEqual(original, sanitized) || containsMutableURL(sanitized.Coverage["roof"]) || containsMutableURL(sanitized.Evidence[0].Flags[0]) || containsMutableURL(sanitized.Checklist.Rooms[0].Components[0].Limitations[0]) {
		t.Fatalf("sanitized copy still carries URLs: %+v", sanitized)
	}
}

// Item 9c (round 8): the customer validation enforces the allowlist itself.
func TestR8ValidateCustomerSnapshotRejectsFieldsOutsideAllowlist(t *testing.T) {
	customer := func() Snapshot {
		s := r8V3Snapshot()
		s.Scope, s.Provenance = nil, nil
		s.Evidence = []Evidence{{ID: "e", RequirementKey: "roof", Role: "CURRENT", Availability: EvidenceWithheld}}
		s.Checklist = &ChecklistSection{Rooms: []ChecklistRoom{{Key: "k", Name: "Cozinha", Components: []ChecklistComponent{{Key: "c", Label: "Piso", Status: "OBSERVED", ReviewStatus: "NOT_REVIEWED", EvidenceIDs: []string{"e"}, Limitations: []string{LimitationConditionUndetermined}}}}}, Coverage: ChecklistCoverage{Expected: 1, FullyAssessed: 1}}
		s.Handover = &HandoverSection{Entries: []HandoverEntry{{Key: "m", Kind: "METER", Status: "DECLARED", Payload: json.RawMessage(`{"meterType":"WATER","reading":1}`), Declarant: "Participante"}, {Key: "n", Kind: "NOTE", Status: "DECLARED"}}}
		s.Processing = &ProcessingState{Status: "FAILED", Message: CustomerProcessingMessage("FAILED")}
		s.Inventory = &InventorySection{Reference: InventoryReference{VersionID: "customer-reference", VersionNumber: 1}, Problems: []InventoryEntry{{Environment: "Sala", Type: "SOFA", Name: "Sofá", Status: "MISSING"}}, Informational: []InventoryEntry{}}
		return s
	}
	if err := ValidateCustomerSnapshot(customer()); err != nil {
		t.Fatalf("allowlisted customer projection rejected: %v", err)
	}
	if err := Validate(customer()); err == nil {
		t.Fatal("an audience-only WITHHELD availability must never validate as a stored snapshot")
	}
	leaks := map[string]func(*Snapshot){
		"attribute sources": func(s *Snapshot) {
			s.Checklist.Rooms[0].Components[0].AttributeSources = []AttributeSource{{Attribute: "condition", Source: "VOICE"}}
		},
		"component type code": func(s *Snapshot) { s.Checklist.Rooms[0].Components[0].TypeCode = "FLOOR" },
		"room type code":      func(s *Snapshot) { s.Checklist.Rooms[0].TypeCode = "KITCHEN" },
		"assessed scope":      func(s *Snapshot) { s.Checklist.Rooms[0].Components[0].AssessedScope = "Parte visível" },
		"not assessed":        func(s *Snapshot) { s.Checklist.Rooms[0].Components[0].NotAssessed = []string{"Fundo"} },
		"conflict flag":       func(s *Snapshot) { s.Checklist.Rooms[0].Components[0].Conflict = true },
		"outside scope":       func(s *Snapshot) { s.Checklist.Rooms[0].Components[0].OutsideScope = true },
		"review decision":     func(s *Snapshot) { s.Checklist.Rooms[0].Components[0].ReviewStatus = "EDITED" },
		"internal limitation": func(s *Snapshot) { s.Checklist.Rooms[0].Components[0].Limitations = []string{LimitationSourceConflict} },
		"note payload":        func(s *Snapshot) { s.Handover.Entries[1].Payload = json.RawMessage(`{"note":"senha"}`) },
		"note declarant":      func(s *Snapshot) { s.Handover.Entries[1].Declarant = "Participante" },
		"payload field": func(s *Snapshot) {
			s.Handover.Entries[0].Payload = json.RawMessage(`{"meterType":"WATER","serial":"X"}`)
		},
		"processing message":    func(s *Snapshot) { s.Processing.Message = "A catalogação dos componentes falhou." },
		"inventory identity":    func(s *Snapshot) { s.Inventory.Problems[0].ItemIdentityID = "item-1" },
		"catalog diagnostics":   func(s *Snapshot) { s.Inventory.CatalogDiagnostics = &InventoryCatalogDiagnostics{ItemsTruncated: 1} },
		"internal scope":        func(s *Snapshot) { s.Scope = &ScopeSnapshot{Unknown: true} },
		"internal findings":     func(s *Snapshot) { s.Findings = []Finding{{Category: "CONSERVATION"}} },
		"internal provenance":   func(s *Snapshot) { s.Provenance = &Provenance{} },
		"payload not an object": func(s *Snapshot) { s.Handover.Entries[0].Payload = json.RawMessage(`[1]`) },
	}
	for name, leak := range leaks {
		snapshot := customer()
		leak(&snapshot)
		if err := ValidateCustomerSnapshot(snapshot); err == nil {
			t.Errorf("%s: customer validation accepted a field outside the allowlist", name)
		}
	}
}

// Item 7 (round 8): v1/v2 HTML keeps its historical caption (no E### prefix)
// and flag/status presentation; v3 adds the references and the shared vocabulary.
func TestR8LegacyHTMLKeepsHistoricalCaptionsAndV3UsesSharedVocabulary(t *testing.T) {
	legacy := validSnapshot()
	legacy.Evidence = []Evidence{{ID: "m1", RequirementKey: "roof", Role: "CURRENT", Description: "foto", Flags: []string{"GALLERY_SOURCE"}}}
	legacy.Requirements = []Requirement{{Key: "roof", Section: "Cobertura", Label: "Telhado", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "FAILED"}}
	html, err := HTML(legacy)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<strong>Vistoria atual</strong>", "Falha técnica", "Situação não reconhecida"} {
		if !strings.Contains(string(html), want) {
			t.Errorf("v1 HTML lost its historical presentation %q", want)
		}
	}
	if strings.Contains(string(html), "E001") {
		t.Fatal("v1 HTML carries a v3 evidence reference")
	}
	v3 := r8V3Snapshot()
	v3.Evidence = legacy.Evidence
	v3.Requirements = legacy.Requirements
	html, err = HTML(v3)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<strong>E001 · Vistoria atual</strong>", "Imagem da galeria", AnalysisStatusLabel("FAILED")} {
		if !strings.Contains(string(html), want) {
			t.Errorf("v3 HTML lacks %q", want)
		}
	}
	if !strings.Contains(string(html), `alt="Vistoria atual · foto"`) || strings.Contains(string(html), `alt="CURRENT · foto"`) {
		t.Fatalf("v3 HTML evidence alt text did not use the Portuguese role: %s", html)
	}
}

// Item 6 (round 8): the shared vocabulary labels every code family.
func TestR8SharedVocabularyLabelsAnalysisStatusesConsistently(t *testing.T) {
	for _, status := range []string{"PENDING", "COMPLETED", "INCONCLUSIVE", "FAILED", "NOT_AUTHORIZED"} {
		if AnalysisStatusLabel(status) != PresentationLabel(status) || CodeLabel(status) != PresentationLabel(status) || AnalysisStatusLabel(status) == unrecognizedLabel {
			t.Errorf("status %s has divergent labels: %q %q %q", status, AnalysisStatusLabel(status), CodeLabel(status), PresentationLabel(status))
		}
	}
	for _, flag := range []string{"QUALITY_WARNING:NEW_CODE", "IMPOSSIBILITY:NEW_REASON", "sensitive"} {
		if FlagLabel(flag) == unrecognizedLabel {
			t.Errorf("parameterized or lower-case flag %q is unlabeled", flag)
		}
	}
	if FlagLabel("UNKNOWN_FLAG") != unrecognizedLabel {
		t.Error("an unknown flag must stay unrecognized instead of leaking its code")
	}
}
