package seed_qa

import (
	"context"
	"encoding/json"
	"testing"

	"inspection/libs/identity"
	analysisprompt "inspection/services/inspection/internal/features/analysis/prompt"
	"inspection/services/inspection/internal/features/templates/catalog"
)

type qaTemplateReferences struct{ segment string }

func (r qaTemplateReferences) SegmentExists(value string) bool { return value == r.segment }
func (r qaTemplateReferences) AnalysisTypeExists(value string) bool {
	return value == analysisprompt.RealEstate
}

func TestQATemplateIsUsableByCreationFlows(t *testing.T) {
	segmentID := identity.NewID()
	encoded, err := qaTemplateDefinition(segmentID)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := catalog.Compile(encoded, qaTemplateReferences{segment: segmentID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if !compiled.Document.MultiStage || len(compiled.Document.Stages) != 1 || compiled.Document.ComparisonMode != catalog.ChecklistOnly {
		t.Fatalf("QA template cannot create a project: %+v", compiled.Document)
	}
	var array []json.RawMessage
	if err := json.Unmarshal(encoded, &array); err == nil {
		t.Fatal("QA template was stored as a bare requirements array")
	}
}

func TestSetupRejectsMissingDependencies(t *testing.T) {
	if _, err := Setup(context.Background(), nil, "issuer", "http://localhost:3003"); err == nil {
		t.Fatal("nil database accepted")
	}
}

func TestStableIDsAreTenantScoped(t *testing.T) {
	tenantA, tenantB := identity.NewID(), identity.NewID()
	first, replay := stableIDs(tenantA), stableIDs(tenantA)
	other := stableIDs(tenantB)
	if first.participant != replay.participant || first.asset != replay.asset {
		t.Fatal("stable QA identities changed between replays")
	}
	if first.participant == other.participant || first.asset == other.asset {
		t.Fatal("stable QA identities leaked across tenants")
	}
}
