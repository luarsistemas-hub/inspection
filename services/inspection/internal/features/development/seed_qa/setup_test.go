package seed_qa

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	analysisprompt "inspection/services/inspection/internal/features/analysis/prompt"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type qaTemplateReferences struct{ segment string }

func (r qaTemplateReferences) SegmentExists(value string) bool { return value == r.segment }
func (r qaTemplateReferences) AnalysisTypeExists(value string) bool {
	return value == analysisprompt.RealEstate
}

func TestSeedQAPinsFindingsSnapshotWhenTasksShareTimestamp(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS analysis").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE analysis.prompt_snapshots(id TEXT PRIMARY KEY,analysis_type TEXT,task TEXT,definition_json BLOB,canonical_digest TEXT,created_at DATETIME)`).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	for _, row := range []database.AnalysisPromptSnapshot{
		{ID: identity.NewID(), AnalysisType: analysisprompt.RealEstate, Task: string(analysisprompt.InventoryCatalog), DefinitionJSON: []byte(`{}`), CanonicalDigest: "catalog", CreatedAt: now},
		{ID: identity.NewID(), AnalysisType: analysisprompt.RealEstate, Task: string(analysisprompt.InventoryCompare), DefinitionJSON: []byte(`{}`), CanonicalDigest: "compare", CreatedAt: now},
		{ID: identity.NewID(), AnalysisType: analysisprompt.RealEstate, Task: string(analysisprompt.Findings), DefinitionJSON: []byte(`{}`), CanonicalDigest: "findings", CreatedAt: now},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := findFindingsPromptSnapshot(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Task != string(analysisprompt.Findings) || snapshot.CanonicalDigest != "findings" {
		t.Fatalf("seed selected wrong snapshot: %+v", snapshot)
	}
}

func TestQATemplateIsUsableByCreationFlows(t *testing.T) {
	segmentID := identity.NewID()
	encoded, err := qaTemplateDefinition(segmentID, "PHOTO")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := catalog.Compile(encoded, qaTemplateReferences{segment: segmentID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Document.DefaultComparisonMode != catalog.ChecklistOnly {
		t.Fatalf("QA template has invalid default comparison mode: %+v", compiled.Document)
	}
	var array []json.RawMessage
	if err := json.Unmarshal(encoded, &array); err == nil {
		t.Fatal("QA template was stored as a bare requirements array")
	}
}

func TestQAVideoTemplateDeclaresVideoCapture(t *testing.T) {
	segmentID := identity.NewID()
	encoded, err := qaTemplateDefinition(segmentID, "VIDEO")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := catalog.Compile(encoded, qaTemplateReferences{segment: segmentID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Document.MediaKind() != "VIDEO" || len(compiled.Document.Requirements) != 1 || compiled.Document.Requirements[0].EvidenceKind != "VIDEO" {
		t.Fatalf("QA origin template is not VIDEO: %+v", compiled.Document)
	}
}

func TestSetupRejectsMissingDependencies(t *testing.T) {
	if _, err := Setup(context.Background(), nil, "issuer", "http://localhost:3003", "PHOTO", false); err == nil {
		t.Fatal("nil database accepted")
	}
	if _, err := Setup(context.Background(), nil, "issuer", "http://localhost:3003", "UNKNOWN", false); err == nil {
		t.Fatal("unknown media kind accepted")
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
