package process_inventory_job

import (
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
)

// Item 6 (review round 3): in a strict comparison the model may only explain
// a gap by the current view (obstructed, framing, quality, no corresponding
// view). "Room not captured" and "analysis failed" are processing facts the
// system records itself: a model claiming them for a captured room of a
// finished job is stored as "no corresponding view". Legacy comparisons keep
// their historical reasons.
func TestTask05R3StrictModelCannotStateProcessingFacts(t *testing.T) {
	item := database.InventoryItem{ID: identity.NewID(), IdentityID: identity.NewID(), TypeCode: "SOFA", Name: "Sofá", Condition: "GOOD", Observation: "OBSERVED"}
	id := item.IdentityID.String()
	for _, reason := range []string{"ENVIRONMENT_NOT_CAPTURED", "ANALYSIS_FAILED", "NO_BASELINE_VIEW", "PAIR_REJECTED", "OBSTRUCTED", "FRAMING", "QUALITY", "NO_CORRESPONDING_VIEW"} {
		code := reason
		rows := []compareRow{{ItemIdentityID: &id, Status: "NOT_OBSERVED", ReasonCode: &code}}
		pairs := []database.InventoryEvidencePair{{ItemIdentityID: item.IdentityID, Confirmed: true}}
		strict := deriveResults(database.InventoryJob{ID: identity.NewID()}, []database.InventoryItem{item}, rows, pairs, time.Now(), compareRules{strict: true, roomCovered: true})
		want := reason
		if !strictModelReasons[reason] {
			want = "NO_CORRESPONDING_VIEW"
		}
		if len(strict) != 1 || strict[0].Status != "NOT_OBSERVED" || value(strict[0].ReasonCode) != want {
			t.Errorf("strict %s stored %+v; want NOT_OBSERVED/%s", reason, strict, want)
		}
		legacy := deriveResults(database.InventoryJob{ID: identity.NewID()}, []database.InventoryItem{item}, rows, pairs, time.Now(), compareRules{})
		legacyWant := reason
		if !notObservedReasons[reason] {
			legacyWant = "NO_CORRESPONDING_VIEW"
		}
		if len(legacy) != 1 || value(legacy[0].ReasonCode) != legacyWant {
			t.Errorf("legacy %s stored %+v; want the historical reason %s", reason, legacy, legacyWant)
		}
	}
}
