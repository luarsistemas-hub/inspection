package core

import (
	"encoding/json"
	"strings"
	"testing"

	reportcore "inspection/services/inspection/internal/features/reports/core"
)

// Item 9d (round 8): conflict explanations name internal inputs (catalog,
// findings, voice and visual sources); the customer screen and PDF read only
// the neutral statement that the condition was not determined.
func TestR8CustomerChecklistReplacesInternalConflictTexts(t *testing.T) {
	snapshot := reportcore.Snapshot{SchemaVersion: 3, Checklist: &reportcore.ChecklistSection{Rooms: []reportcore.ChecklistRoom{{Key: "living", Name: "Sala", Components: []reportcore.ChecklistComponent{
		{Key: "sofa", Label: "Sofá", Status: "OBSERVED", Description: "Sofá de tecido", Condition: "GOOD", Conflict: true, Limitations: []string{reportcore.LimitationSourceConflict}},
		{Key: "rack", Label: "Rack", Status: "OBSERVED", Description: "Rack de madeira", Condition: "FAIR", Limitations: []string{reportcore.LimitationAmbiguousCatalog, "Vistas pendentes: detalhe"}},
		{Key: "wall", Label: "Parede", Status: "OBSERVED", Conflict: true, Limitations: []string{reportcore.LimitationCatalogFindingConflict}},
	}}}}, Processing: &reportcore.ProcessingState{Status: "COMPLETED"}, Handover: &reportcore.HandoverSection{}}
	checklist, _ := FilterDescriptive(snapshot)
	view := ProjectChecklist(snapshot)
	encoded, err := json.Marshal(struct {
		Checklist any
		View      any
	}{checklist, view})
	if err != nil {
		t.Fatal(err)
	}
	for _, internal := range []string{"fontes visual e por voz", "item de catálogo", "catálogo e uma constatação", "Sofá de tecido", "Rack de madeira"} {
		if strings.Contains(string(encoded), internal) {
			t.Fatalf("customer projection reveals %q: %s", internal, encoded)
		}
	}
	for _, component := range view.Rooms[0].Components {
		if len(component.Limitations) == 0 || component.Limitations[0] != reportcore.LimitationConditionUndetermined || component.Condition != "" {
			t.Fatalf("component %s: limitations=%v condition=%q", component.Label, component.Limitations, component.Condition)
		}
		seen := map[string]bool{}
		for _, limitation := range component.Limitations {
			if seen[limitation] {
				t.Fatalf("component %s repeats %q", component.Label, limitation)
			}
			seen[limitation] = true
		}
	}
	if got := view.Rooms[0].Components[1].Limitations; len(got) != 2 || got[1] != "Vistas pendentes: detalhe" {
		t.Fatalf("neutral limitations must be kept: %v", got)
	}
}
