package core

import (
	"encoding/json"
	"strings"
	"testing"

	reportcore "inspection/services/inspection/internal/features/reports/core"
)

// TestUT050UT052CustomerChecklistUsesOnlyTheApprovedAllowlist covers the descriptive customer contract.
func TestUT050UT052CustomerChecklistUsesOnlyTheApprovedAllowlist(t *testing.T) {
	snapshot := reportcore.Snapshot{SchemaVersion: 3, Checklist: &reportcore.ChecklistSection{Rooms: []reportcore.ChecklistRoom{{Name: "Cozinha", Components: []reportcore.ChecklistComponent{{Label: "Piso", Status: "OBSERVED", Description: "Piso cerâmico", Condition: "GOOD", ReviewStatus: "REJECTED", Conflict: true, Limitations: []string{"Área sob o armário não observada"}, EvidenceIDs: []string{"evidence-1"}}}}}, Coverage: reportcore.ChecklistCoverage{Expected: 1, FullyAssessed: 1}}, Processing: &reportcore.ProcessingState{Status: "FAILED", Message: "segredo técnico"}, Scope: &reportcore.ScopeSnapshot{Document: []byte(`{"private":"secret"}`)}, Handover: &reportcore.HandoverSection{Entries: []reportcore.HandoverEntry{{Key: "observação", Kind: "NOTE", Status: "DECLARED", Declarant: "Nome privado", Payload: []byte(`{"note":"texto privado"}`)}}}}
	view := ProjectChecklist(snapshot)
	if view == nil || len(view.Rooms) != 1 || len(view.Rooms[0].Components) != 1 {
		t.Fatalf("projection=%+v", view)
	}
	component := view.Rooms[0].Components[0]
	if component.Description != "" || component.Condition != "" || component.ReviewLabel != "" || len(component.Limitations) != 2 || component.Limitations[0] != "A condição não foi determinada." {
		t.Fatalf("customer component=%+v", component)
	}
	if view.Processing != "Falha na análise" || len(view.Handover) != 1 {
		t.Fatalf("customer statuses=%+v", view)
	}
	encodedBytes, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(encodedBytes)
	for _, private := range []string{"REJECTED", "secret", "Nome privado", "texto privado", "segredo técnico", "reviewStatus", "conflict"} {
		if strings.Contains(encoded, private) {
			t.Fatalf("private field leaked: %s", private)
		}
	}
}

func TestCustomerProjectionUsesSharedLabelsClassificationAndEvidenceNumbers(t *testing.T) {
	snapshot := reportcore.Snapshot{SchemaVersion: 3, Classification: "NORMAL", Evidence: []reportcore.Evidence{{ID: "media-uuid", Availability: "AVAILABLE"}}, Processing: &reportcore.ProcessingState{Status: "COMPLETED"}, Scope: &reportcore.ScopeSnapshot{Profile: "PROPERTY_COMPONENTS"}, Provenance: &reportcore.Provenance{ScopeDigest: "scope"}, Checklist: &reportcore.ChecklistSection{Coverage: reportcore.ChecklistCoverage{Expected: 1, FullyAssessed: 1}, Rooms: []reportcore.ChecklistRoom{{Name: "Sala", Components: []reportcore.ChecklistComponent{{Label: "Parede", Status: "OBSERVED", Condition: "GOOD", ReviewStatus: "NOT_REVIEWED", EvidenceIDs: []string{"media-uuid"}}}}}}, Handover: &reportcore.HandoverSection{}}
	view := ProjectChecklist(snapshot)
	component := view.Rooms[0].Components[0]
	if component.Status != "Observado" || component.Condition != "Bom" || component.ReviewLabel != "Não revisado" || len(component.EvidenceIDs) != 1 || component.EvidenceIDs[0] != "E001" {
		t.Fatalf("customer descriptive vocabulary/reference=%+v", component)
	}
	if view.Limited || view.ClassificationLabel != "Sem alertas identificados" {
		t.Fatalf("customer classification projection label=%q limited=%v", view.ClassificationLabel, view.Limited)
	}
}

func TestUT050CustomerProjectionShowsOnlyTheUnreviewedLabelAndHandoverFacts(t *testing.T) {
	snapshot := reportcore.Snapshot{SchemaVersion: 3, Checklist: &reportcore.ChecklistSection{Rooms: []reportcore.ChecklistRoom{{Name: "Cozinha", Components: []reportcore.ChecklistComponent{{Label: "Piso", Status: "OBSERVED", Description: "Superfície clara", ReviewStatus: "NOT_REVIEWED", EvidenceIDs: []string{"e-1"}}}}}, Coverage: reportcore.ChecklistCoverage{Expected: 1, FullyAssessed: 1}}, Handover: &reportcore.HandoverSection{Entries: []reportcore.HandoverEntry{{Key: "private-internal-key", Kind: "METER", Status: "DECLARED", Payload: []byte(`{"meterType":"ENERGY","identifier":"internal-id","reading":123.4,"unit":"kWh","date":"2026-11-03","note":"private note"}`), Declarant: "Participante", EvidenceIDs: []string{"e-meter"}}}}}
	view := ProjectChecklist(snapshot)
	component := view.Rooms[0].Components[0]
	if component.ReviewLabel != "Não revisado" || component.Description != "Superfície clara" {
		t.Fatalf("customer review label/projection=%+v", component)
	}
	handover := view.Handover[0]
	if handover.Label != "Medidor" || handover.Type != "Energia" || handover.Value != "123.4" || handover.Unit != "kWh" || handover.Date != "2026-11-03" || handover.Declarant != "Participante" || len(handover.EvidenceIDs) != 1 {
		t.Fatalf("customer handover facts=%+v", handover)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private-internal-key", "internal-id", "private note", "NOT_REVIEWED"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("customer projection leaked %q: %s", forbidden, encoded)
		}
	}
}

// TestFilterDescriptiveKeepsCodesKeysAndEvidenceIDs guards the PDF path: the
// allowlist removes internal fields but never presents values, so labels and
// E### numbers are applied exactly once downstream.
func TestFilterDescriptiveKeepsCodesKeysAndEvidenceIDs(t *testing.T) {
	snapshot := reportcore.Snapshot{SchemaVersion: 3, Evidence: []reportcore.Evidence{{ID: "photo-1"}, {ID: "meter-1"}}, Checklist: &reportcore.ChecklistSection{Rooms: []reportcore.ChecklistRoom{{Key: "kitchen", TypeCode: "KITCHEN", Name: "Cozinha", Components: []reportcore.ChecklistComponent{
		{Key: "floor", TypeCode: "FLOOR", Label: "Piso", Status: "OBSERVED", Description: "Piso cerâmico", Condition: "GOOD", ReviewStatus: "PENDING", AssessedScope: "interno", EvidenceIDs: []string{"photo-1"}, AttributeSources: []reportcore.AttributeSource{{Attribute: "condition", Source: "VOICE"}}},
		{Key: "outside", Label: "Fora", Status: "OBSERVED", OutsideScope: true},
	}}}}, Handover: &reportcore.HandoverSection{Entries: []reportcore.HandoverEntry{
		{Key: "energy", Kind: "METER", Status: "DECLARED", Payload: []byte(`{"meterType":"ENERGY","reading":123.4,"unit":"kWh","date":"2026-11-04","serial":"interno"}`), Declarant: "Participante", EvidenceIDs: []string{"meter-1"}},
		{Key: "note", Kind: "NOTE", Status: "DECLARED", Payload: []byte(`{"note":"texto privado"}`), Declarant: "Nome privado"},
	}}}
	checklist, handover := FilterDescriptive(snapshot)
	if checklist == nil || handover == nil || len(checklist.Rooms) != 1 || len(checklist.Rooms[0].Components) != 1 {
		t.Fatalf("allowlist result = %+v %+v", checklist, handover)
	}
	room, component := checklist.Rooms[0], checklist.Rooms[0].Components[0]
	if room.Key != "kitchen" || room.TypeCode != "" || component.Key != "floor" || component.Status != "OBSERVED" || component.Condition != "GOOD" || component.ReviewStatus != "NOT_REVIEWED" || len(component.EvidenceIDs) != 1 || component.EvidenceIDs[0] != "photo-1" {
		t.Fatalf("allowlist changed codes or references: %+v %+v", room, component)
	}
	if component.TypeCode != "" || component.AssessedScope != "" || component.AttributeSources != nil {
		t.Fatalf("allowlist leaked internal component fields: %+v", component)
	}
	meter, note := handover.Entries[0], handover.Entries[1]
	var payload map[string]any
	if err := json.Unmarshal(meter.Payload, &payload); err != nil || meter.Key != "energy" || meter.Status != "DECLARED" || payload["meterType"] != "ENERGY" || payload["reading"] != 123.4 || payload["serial"] != nil || meter.EvidenceIDs[0] != "meter-1" {
		t.Fatalf("meter allowlist = %+v (%s)", meter, meter.Payload)
	}
	if note.Payload != nil || note.Declarant != "" || note.Key != "note" {
		t.Fatalf("note leaked private text: %+v", note)
	}
	view := ProjectChecklist(snapshot)
	if got := view.Rooms[0].Components[0]; got.Status != "Observado" || got.Condition != "Bom" || got.ReviewLabel != "Não revisado" || got.EvidenceIDs[0] != "E001" || view.Handover[0].Type != "Energia" || view.Handover[0].EvidenceIDs[0] != "E002" {
		t.Fatalf("screen projection presented values incorrectly: %+v %+v", got, view.Handover)
	}
}
