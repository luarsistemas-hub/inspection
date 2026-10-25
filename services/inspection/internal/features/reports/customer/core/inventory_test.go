package core

import (
	"encoding/json"
	"strings"
	"testing"

	reportcore "inspection/services/inspection/internal/features/reports/core"
)

func TestUT268MapInventoryV2UsesCustomerAllowlist(t *testing.T) {
	snapshot := reportcore.Snapshot{SchemaVersion: 2, Inventory: &reportcore.InventorySection{
		Reference: reportcore.InventoryReference{VersionID: "private-version", VersionNumber: 3, Purpose: "INVENTORY_UPDATE"},
		Coverage:  &reportcore.InventoryCoverage{ExpectedItems: 2, AssessedItems: 1},
		Problems:  []reportcore.InventoryEntry{{ItemIdentityID: "private-item", Environment: "Sala", Type: "SOFA", Name: "Sofá", Status: "MISSING", Pairs: []reportcore.InventoryEvidencePair{{Origin: "origin-evidence", Current: "current-evidence", Decision: "SAME_VIEW"}}}},
	}, Evidence: []reportcore.Evidence{{ID: "origin-evidence", SourceAvailability: "EXPIRED", Availability: "AVAILABLE"}, {ID: "current-evidence", Availability: "AVAILABLE"}}}
	view := MapInventory(snapshot)
	if view == nil || view.ReferenceVersionNumber != 3 || len(view.Problems) != 1 || len(view.Problems[0].Pairs) != 1 {
		t.Fatalf("unexpected customer view: %#v", view)
	}
	if view.Problems[0].Pairs[0].OriginSourceAvailability != "EXPIRED" || view.Problems[0].Pairs[0].CurrentSourceAvailability != "AVAILABLE" {
		t.Fatalf("source availability was not preserved: %#v", view.Problems[0].Pairs[0])
	}
	payload, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, forbidden := range []string{"private-version", "private-item", "decision", "confidenceBps", "itemIdentityId"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("customer projection leaked %q: %s", forbidden, text)
		}
	}
}

func TestMapInventoryV1AndMissingSectionAreAbsent(t *testing.T) {
	if MapInventory(reportcore.Snapshot{SchemaVersion: 1, Inventory: &reportcore.InventorySection{}}) != nil {
		t.Fatal("v1 must preserve the legacy customer shape")
	}
	if MapInventory(reportcore.Snapshot{SchemaVersion: 2}) != nil {
		t.Fatal("missing inventory section must remain absent")
	}
}

func TestUT270ExpiredCurrentSourceKeepsFrameReference(t *testing.T) {
	timestamp := int64(4200)
	snapshot := reportcore.Snapshot{SchemaVersion: 2, Inventory: &reportcore.InventorySection{
		Reference: reportcore.InventoryReference{VersionID: "version", VersionNumber: 1, Purpose: "MOVE_IN"},
		Problems:  []reportcore.InventoryEntry{{Environment: "Sala", Type: "TABLE", Name: "Mesa", Status: "CONDITION_CHANGED", Pairs: []reportcore.InventoryEvidencePair{{Origin: "origin-photo", Current: "frame-1", Decision: "SAME_VIEW", TimestampMS: &timestamp}}}},
	}, Evidence: []reportcore.Evidence{{ID: "origin-photo", Availability: "AVAILABLE"}, {ID: "frame-1", EvidenceKind: "VIDEO_FRAME", FrameID: "frame-1", Availability: "AVAILABLE", SourceAvailability: "EXPIRED"}}}
	pair := MapInventory(snapshot).Problems[0].Pairs[0]
	if pair.CurrentEvidenceID != "frame-1" || pair.CurrentSourceAvailability != "EXPIRED" || pair.TimestampMS == nil || *pair.TimestampMS != 4200 {
		t.Fatalf("pair = %+v, want the frame kept with an expired source", pair)
	}
}
