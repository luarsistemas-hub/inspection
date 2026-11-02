package process_inventory_job

import (
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
)

func TestIT170_visualConflictRequiresSpokenAttributeAndIsBounded(t *testing.T) {
	item := catalogItem{Name: "Pratos", Quantity: intPtr(5), Condition: "GOOD"}
	voice := []VoiceCitation{{Attributes: []string{"quantity"}}}
	got := acceptedConflicts([]catalogConflict{
		{Attribute: "quantity", VisualValue: "2"},
		{Attribute: "name", VisualValue: "Taças"},
		{Attribute: "quantity", VisualValue: "2"},
		{Attribute: "quantity", VisualValue: string(make([]byte, 201))},
	}, voice, item)
	if len(got) != 1 || got[0]["attribute"] != "quantity" || got[0]["visualValue"] != "2" || got[0]["voiceValue"] != "5" {
		t.Fatalf("accepted conflicts = %#v, want one bounded quantity conflict", got)
	}
}

func TestIT170_catalogRowsPersistsAValidatedVoiceImageConflict(t *testing.T) {
	noteID, revisionID := identity.NewID(), identity.NewID()
	voice := CatalogNote{NoteID: noteID, RevisionID: revisionID}
	candidate := catalogItem{TypeCode: "OTHER", Name: "Pratos", Quantity: intPtr(5), Condition: "GOOD", Observation: "OBSERVED", ConfidenceBPS: 9000,
		Voice:     []voiceCitation{{NoteID: noteID.String(), RevisionID: revisionID.String(), Attributes: []string{"name", "quantity"}}},
		Conflicts: []catalogConflict{{Attribute: "quantity", VisualValue: "2"}, {Attribute: "color", VisualValue: "azul"}, {Attribute: "quantity", VisualValue: string(make([]byte, 201))}},
	}
	items, _, _ := catalogRowsWithVoice(database.InventoryJob{TenantID: identity.NewID(), OriginVersionID: identity.NewID()}, database.InventoryEnvironment{ID: identity.NewID()}, []catalogItem{candidate}, map[string]evidenceInput{}, map[string]CatalogNote{noteID.String() + ":" + revisionID.String(): voice}, time.Now().UTC())
	if len(items) != 1 {
		t.Fatalf("items=%d, want one valid voice-declared item", len(items))
	}
	var suggested struct {
		Conflicts []struct {
			Attribute   string `json:"attribute"`
			VisualValue string `json:"visualValue"`
			VoiceValue  string `json:"voiceValue"`
		} `json:"conflicts"`
	}
	if err := json.Unmarshal(items[0].Suggested, &suggested); err != nil {
		t.Fatal(err)
	}
	if len(suggested.Conflicts) != 1 || suggested.Conflicts[0].Attribute != "quantity" || suggested.Conflicts[0].VisualValue != "2" || suggested.Conflicts[0].VoiceValue != "5" {
		t.Fatalf("suggested conflicts=%+v; suggestion=%s", suggested.Conflicts, items[0].Suggested)
	}
}

func intPtr(value int) *int { return &value }
