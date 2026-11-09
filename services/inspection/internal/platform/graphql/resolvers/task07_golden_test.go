package resolvers

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"
)

func TestTask07InternalChecklistProjectionMatchesSharedGolden(t *testing.T) {
	snapshot := reportcore.Snapshot{
		SchemaVersion: 3,
		Checklist: &reportcore.ChecklistSection{
			Coverage: reportcore.ChecklistCoverage{Expected: 1, FullyAssessed: 1},
			Rooms: []reportcore.ChecklistRoom{{Key: "kitchen", TypeCode: "KITCHEN", Name: "Cozinha", Components: []reportcore.ChecklistComponent{{
				Key: "cabinet", TypeCode: "CABINET", Label: "Armários", Status: "OBSERVED", Description: "Armários íntegros nas vistas disponíveis.", Condition: "GOOD", ReviewStatus: "NOT_REVIEWED", AssessedScope: "Frentes e laterais", NotAssessed: []string{"Parte superior"}, EvidenceIDs: []string{"current-photo"}, Limitations: []string{"A parte superior não foi observada."}, AttributeSources: []reportcore.AttributeSource{{Attribute: "description", Source: "VISUAL", EvidenceIDs: []string{"current-photo"}}},
			}}}},
		},
		Evidence: []reportcore.Evidence{{ID: "current-photo", Role: "CURRENT", Availability: "AVAILABLE"}},
	}
	actual, err := json.MarshalIndent(mapInternalChecklist(snapshot), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	actual = append(actual, '\n')
	golden, err := os.ReadFile("testdata/internal_report_v3.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, golden) {
		t.Fatalf("internal checklist projection differs from shared golden\nactual:\n%s", actual)
	}
}

func TestTask07ReportEvidenceReferenceComesFromSnapshotEvidenceOrder(t *testing.T) {
	snapshot := reportcore.Snapshot{SchemaVersion: 3, Evidence: []reportcore.Evidence{{ID: "first-photo", RequirementKey: "room", Role: "CURRENT", Availability: "AVAILABLE"}, {ID: "second-photo", RequirementKey: "room", Role: "CURRENT", Availability: "AVAILABLE"}}}
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	projected := mapReport(database.ReportSnapshot{ID: identity.NewID(), InspectionID: identity.NewID(), VersionNumber: 1, CanonicalJSON: canonical, HTML: json.RawMessage(`""`)})
	if projected.Evidence.Nodes[0].Reference == nil || *projected.Evidence.Nodes[0].Reference != "E001" {
		t.Fatalf("first evidence reference = %+v", projected.Evidence.Nodes[0].Reference)
	}
	if projected.Evidence.Nodes[1].Reference == nil || *projected.Evidence.Nodes[1].Reference != "E002" {
		t.Fatalf("second evidence reference = %+v", projected.Evidence.Nodes[1].Reference)
	}
}
