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
	if err := db.Exec(`CREATE TABLE analysis.prompt_snapshots(id TEXT PRIMARY KEY,analysis_type TEXT,task TEXT,definition_json BLOB,canonical_digest TEXT,schema_version INTEGER NOT NULL DEFAULT 1,created_at DATETIME)`).Error; err != nil {
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
	inventorySnapshot, err := findInventoryPromptSnapshot(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if inventorySnapshot.Task != string(analysisprompt.InventoryCatalog) || inventorySnapshot.CanonicalDigest != "catalog" {
		t.Fatalf("seed selected wrong inventory snapshot: %+v", inventorySnapshot)
	}
}

func TestQATemplateIsUsableByCreationFlows(t *testing.T) {
	segmentID := identity.NewID()
	encoded, err := qaTemplateDefinition(segmentID, "PHOTO", false)
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
	if compiled.Document.SchemaVersion != catalog.LegacySchemaVersion || templateSchemaVersion(false) != catalog.LegacySchemaVersion {
		t.Fatalf("standard QA seed should remain on legacy schema: %+v", compiled.Document)
	}
	var array []json.RawMessage
	if err := json.Unmarshal(encoded, &array); err == nil {
		t.Fatal("QA template was stored as a bare requirements array")
	}
}

func TestQAVideoTemplateDeclaresVideoCapture(t *testing.T) {
	segmentID := identity.NewID()
	encoded, err := qaTemplateDefinition(segmentID, "VIDEO", false)
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

func TestQAVoiceTemplatesUseEnvironmentCapture(t *testing.T) {
	segmentID := identity.NewID()
	for _, mediaKind := range []string{"PHOTO", "VIDEO"} {
		t.Run(mediaKind, func(t *testing.T) {
			encoded, err := qaTemplateDefinition(segmentID, mediaKind, true)
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := catalog.Compile(encoded, qaTemplateReferences{segment: segmentID.String()})
			if err != nil {
				t.Fatal(err)
			}
			document := compiled.Document
			if document.SchemaVersion != catalog.EnvironmentSchemaVersion || document.CaptureLayout != catalog.EnvironmentsLayout {
				t.Fatalf("voice QA template is not environment capture: %+v", document)
			}
			if len(document.Requirements) != 0 || len(document.SuggestedEnvironmentTypes) < 2 || document.SuggestedEnvironmentTypes[0] != "KITCHEN" || document.SuggestedEnvironmentTypes[1] != "LIVING_ROOM" {
				t.Fatalf("voice QA template lacks Cocina and Sala environments: %+v", document)
			}
			if document.MediaKind() != mediaKind {
				t.Fatalf("media kind = %s, want %s", document.MediaKind(), mediaKind)
			}
		})
	}
}

func TestSetupRejectsMissingDependencies(t *testing.T) {
	if _, _, err := Setup(context.Background(), nil, "issuer", "http://localhost:3003", "PHOTO", false); err == nil {
		t.Fatal("nil database accepted")
	}
	if _, _, err := Setup(context.Background(), nil, "issuer", "http://localhost:3003", "UNKNOWN", false); err == nil {
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

func TestSeedQAVoiceAndLegacyTemplatesStayIsolatedAcrossAlternatingRuns(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS templates").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE templates.template_versions (
		id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, template_id TEXT NOT NULL, version_number INTEGER NOT NULL,
		schema_version INTEGER NOT NULL, definition_json TEXT NOT NULL, canonical_digest TEXT NOT NULL,
		status TEXT NOT NULL, idempotency_key TEXT NOT NULL, published_at DATETIME NOT NULL, created_by TEXT NOT NULL
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE templates.templates (
		id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, key TEXT NOT NULL, name TEXT NOT NULL,
		segment_version_id TEXT NOT NULL, active_version_id TEXT, version INTEGER NOT NULL,
		created_at DATETIME, updated_at DATETIME
	)`).Error; err != nil {
		t.Fatal(err)
	}

	tenantID, membershipID, segmentID := identity.NewID(), identity.NewID(), identity.NewID()
	ids := stableIDs(tenantID)
	voiceIDs := voiceTemplateIDs(tenantID)
	if ids.template == voiceIDs.template || ids.templateVersion == voiceIDs.templateVersion {
		t.Fatal("voice template IDs must be distinct from the stable legacy template")
	}
	legacyDefinition, err := qaTemplateDefinition(segmentID, "PHOTO", false)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the prior broken seed: stable legacy identity with a v2 document.
	corruptedDefinition, err := qaTemplateDefinition(segmentID, "PHOTO", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO templates.template_versions
		(id,tenant_id,template_id,version_number,schema_version,definition_json,canonical_digest,status,idempotency_key,published_at,created_by)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`, ids.templateVersion, tenantID, ids.template, 1, 2, corruptedDefinition, digest(corruptedDefinition), "ACTIVE", "qa-seed-template", time.Now(), membershipID).Error; err != nil {
		t.Fatal(err)
	}

	seedWithoutVoice := func() {
		t.Helper()
		if err := repairLegacyTemplateVersion(db, tenantID, ids.templateVersion, legacyDefinition); err != nil {
			t.Fatal(err)
		}
	}
	seedWithVoice := func() {
		t.Helper()
		seedWithoutVoice()
		if err := ensureVoiceTemplate(db, tenantID, membershipID, segmentID, voiceIDs, "PHOTO", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}

	seedWithoutVoice()
	assertQATemplateVersion(t, db, tenantID, ids.templateVersion, 1, legacyDefinition)
	seedWithVoice()
	assertQATemplateVersion(t, db, tenantID, ids.templateVersion, 1, legacyDefinition)
	var voice database.TemplateVersion
	if err := db.Where("tenant_id=? AND id=?", tenantID, voiceIDs.templateVersion).Take(&voice).Error; err != nil {
		t.Fatal(err)
	}
	if voice.SchemaVersion != catalog.EnvironmentSchemaVersion {
		t.Fatalf("voice template schema version = %d, want 2", voice.SchemaVersion)
	}
	var voiceDocument catalog.TemplateDocument
	if err := json.Unmarshal(voice.DefinitionJSON, &voiceDocument); err != nil {
		t.Fatal(err)
	}
	if voiceDocument.CaptureLayout != catalog.EnvironmentsLayout || len(voiceDocument.SuggestedEnvironmentTypes) != 2 || voiceDocument.SuggestedEnvironmentTypes[0] != "KITCHEN" || voiceDocument.SuggestedEnvironmentTypes[1] != "LIVING_ROOM" {
		t.Fatalf("voice template does not retain the environment layout: %+v", voiceDocument)
	}
	seedWithoutVoice()
	assertQATemplateVersion(t, db, tenantID, ids.templateVersion, 1, legacyDefinition)
	assertQATemplateVersion(t, db, tenantID, voiceIDs.templateVersion, 2, voice.DefinitionJSON)
}

func assertQATemplateVersion(t *testing.T, db *gorm.DB, tenantID, id identity.ID, schemaVersion int, definition []byte) {
	t.Helper()
	var version database.TemplateVersion
	if err := db.Where("tenant_id=? AND id=?", tenantID, id).Take(&version).Error; err != nil {
		t.Fatal(err)
	}
	if version.SchemaVersion != schemaVersion || string(version.DefinitionJSON) != string(definition) {
		t.Fatalf("template %s changed: schema=%d definition=%s", id, version.SchemaVersion, version.DefinitionJSON)
	}
}
