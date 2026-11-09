package generate_snapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	report "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func publicationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, schema := range []string{"reports", "audit", "messaging", "media"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AutoMigrate(&database.ReportSnapshot{}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE reports.report_publications (id blob PRIMARY KEY, tenant_id blob NOT NULL, snapshot_id blob NOT NULL, inspection_id blob NOT NULL, policy_version integer, client_mutation_id text NOT NULL, status text NOT NULL, actor_id blob NOT NULL, reason text, version integer NOT NULL, published_at datetime, invalidated_at datetime, invalidated_by blob, superseded_by blob, created_at datetime, updated_at datetime, UNIQUE(tenant_id, client_mutation_id), UNIQUE(tenant_id, snapshot_id))`,
		`CREATE TABLE audit.events (id blob PRIMARY KEY, tenant_id blob NOT NULL, actor_id blob NOT NULL, action text NOT NULL, target_type text NOT NULL, target_id text NOT NULL, outcome text NOT NULL, reason text, correlation_id text NOT NULL, occurred_at datetime NOT NULL)`,
		`CREATE TABLE messaging.outbox (id blob primary key, tenant_id blob not null, type text not null, schema_version integer not null, payload blob not null, correlation_id text not null, causation_id text, status text not null, attempts integer not null default 0, next_attempt_at datetime not null, claimed_at datetime, last_error text, published_at datetime, created_at datetime)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func automaticInput(tenant, inspection identity.ID, mode string, version int64, prompt string) Input {
	return Input{TenantID: tenant, InspectionID: inspection, Mode: "HISTORICAL", Classification: "CRITICAL", TemplateVersionID: "template-v1", ReferenceVersionID: "reference-v1", PromptDigest: prompt, PublicationMode: mode, PublicationPolicyVersion: version, ReasonCodes: []string{"INCONCLUSIVE"}, Context: report.Context{Asset: report.AssetContext{ID: "asset", Name: "Imóvel", ExternalKey: "A-1", Address: "Rua 1"}, Participant: report.ParticipantContext{ID: "participant", Name: "Responsável"}, Template: report.TemplateContext{ID: "template", Name: "Modelo", Version: 1}, Inspection: report.InspectionContext{GeneratedAt: "2026-09-16T00:00:00Z"}}}
}

// TestAutomaticPolicyPublishesInSnapshotTransactionUT027 covers UT-027 and UT-031.
func TestAutomaticPolicyPublishesInSnapshotTransactionUT027(t *testing.T) {
	db := publicationDB(t)
	tenant, inspection := identity.NewID(), identity.NewID()
	first, err := Create(context.Background(), db, automaticInput(tenant, inspection, "AUTOMATIC", 4, "prompt-1"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var publication database.ReportPublication
	if err := db.Where("snapshot_id=?", first.SnapshotID).Take(&publication).Error; err != nil {
		t.Fatalf("automatic mode created no publication: %v", err)
	}
	if first.PublicationID != publication.ID || publication.Status != "PUBLISHED" || publication.PolicyVersion != 4 || publication.ClientMutationID != AutomaticMutationID(first.SnapshotID) {
		t.Fatalf("automatic publication = %+v result=%+v", publication, first)
	}
	var events, audits int64
	db.Model(&database.OutboxIntent{}).Where("type=?", "report.published.v1").Count(&events)
	db.Model(&database.AuditEvent{}).Where("action=?", "report.published").Count(&audits)
	if events != 1 || audits != 1 {
		t.Fatalf("events=%d audits=%d, want one of each", events, audits)
	}
	// A replay of the identical report neither duplicates nor republishes.
	replay, err := Create(context.Background(), db, automaticInput(tenant, inspection, "AUTOMATIC", 4, "prompt-1"), time.Now())
	if err != nil || replay.SnapshotID != first.SnapshotID {
		t.Fatalf("replay = %+v (%v)", replay, err)
	}
	// UT-031: a newer final snapshot supersedes the former active publication.
	second, err := Create(context.Background(), db, automaticInput(tenant, inspection, "AUTOMATIC", 4, "prompt-2"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var former database.ReportPublication
	if err := db.First(&former, "id=?", publication.ID).Error; err != nil || former.Status != "SUPERSEDED" || former.SupersededBy == nil || *former.SupersededBy != second.PublicationID {
		t.Fatalf("former publication = %+v (%v)", former, err)
	}
	var active int64
	db.Model(&database.ReportPublication{}).Where("inspection_id=? AND status='PUBLISHED'", inspection).Count(&active)
	if active != 1 {
		t.Fatalf("active publications = %d, want 1", active)
	}
}

// TestV3AutomaticPolicyPublishesInSnapshotTransactionUT027 repeats UT-027 and
// UT-031 for a v3 descriptive snapshot; the v1 case above stays unchanged.
func TestV3AutomaticPolicyPublishesInSnapshotTransactionUT027(t *testing.T) {
	db := publicationDB(t)
	tenant, inspection := identity.NewID(), identity.NewID()
	input := automaticInput(tenant, inspection, "AUTOMATIC", 4, "prompt-1")
	input = publicationV3(input)
	first, err := Create(context.Background(), db, input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var publication database.ReportPublication
	if err := db.Where("snapshot_id=?", first.SnapshotID).Take(&publication).Error; err != nil {
		t.Fatalf("automatic mode created no publication: %v", err)
	}
	if first.PublicationID != publication.ID || publication.Status != "PUBLISHED" || publication.PolicyVersion != 4 || publication.ClientMutationID != AutomaticMutationID(first.SnapshotID) {
		t.Fatalf("automatic publication = %+v result=%+v", publication, first)
	}
	var events, audits int64
	db.Model(&database.OutboxIntent{}).Where("type=?", "report.published.v1").Count(&events)
	db.Model(&database.AuditEvent{}).Where("action=?", "report.published").Count(&audits)
	if events != 1 || audits != 1 {
		t.Fatalf("events=%d audits=%d, want one of each", events, audits)
	}
	// A replay of the identical report neither duplicates nor republishes.
	replay, err := Create(context.Background(), db, input, time.Now())
	if err != nil || replay.SnapshotID != first.SnapshotID {
		t.Fatalf("replay = %+v (%v)", replay, err)
	}
	// UT-031: a newer final snapshot supersedes the former active publication.
	secondInput := publicationV3(automaticInput(tenant, inspection, "AUTOMATIC", 4, "prompt-2"))
	second, err := Create(context.Background(), db, secondInput, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var former database.ReportPublication
	if err := db.First(&former, "id=?", publication.ID).Error; err != nil || former.Status != "SUPERSEDED" || former.SupersededBy == nil || *former.SupersededBy != second.PublicationID {
		t.Fatalf("former publication = %+v (%v)", former, err)
	}
	var active int64
	db.Model(&database.ReportPublication{}).Where("inspection_id=? AND status='PUBLISHED'", inspection).Count(&active)
	if active != 1 {
		t.Fatalf("active publications = %d, want 1", active)
	}
}

func publicationV3(input Input) Input {
	input.Checklist = &report.ChecklistSection{Rooms: []report.ChecklistRoom{}, Coverage: report.ChecklistCoverage{UnknownScope: true}}
	input.Processing = &report.ProcessingState{Status: "INCONCLUSIVE"}
	input.Scope = &report.ScopeSnapshot{Unknown: true}
	input.Handover = &report.HandoverSection{Entries: []report.HandoverEntry{}}
	input.Provenance = &report.Provenance{PromptRevision: input.PromptDigest}
	return input
}

// TestManualPolicyRecordsVersionWithoutPublicationUT028 covers UT-028.
func TestManualPolicyRecordsVersionWithoutPublicationUT028(t *testing.T) {
	db := publicationDB(t)
	created, err := Create(context.Background(), db, automaticInput(identity.NewID(), identity.NewID(), "MANUAL", 2, "prompt-manual"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var snapshot database.ReportSnapshot
	if err := db.First(&snapshot, "id=?", created.SnapshotID).Error; err != nil || snapshot.PublicationPolicyVersion != 2 {
		t.Fatalf("snapshot = %+v (%v)", snapshot, err)
	}
	var count int64
	db.Model(&database.ReportPublication{}).Count(&count)
	if count != 0 || created.PublicationID != (identity.ID{}) {
		t.Fatalf("manual policy published: count=%d result=%+v", count, created)
	}
}

// TestUT047UT057UT058UT059UT061UT062UT063SnapshotVersioningIsImmutableAndIdempotent covers append-only report version behavior.
func TestUT047UT057UT058UT059UT061UT062UT063SnapshotVersioningIsImmutableAndIdempotent(t *testing.T) {
	db := publicationDB(t)
	tenant, inspection := identity.NewID(), identity.NewID()
	input := automaticInput(tenant, inspection, "MANUAL", 1, "prompt-v1")
	input.Checklist = &report.ChecklistSection{Rooms: []report.ChecklistRoom{{Key: "kitchen", Name: "Cozinha", Components: []report.ChecklistComponent{{Key: "floor", Label: "Piso", Status: "NOT_OBSERVED", ReviewStatus: "NOT_REVIEWED"}}}}, Coverage: report.ChecklistCoverage{Expected: 1, NotObserved: 1}}
	input.Processing = &report.ProcessingState{Status: "INCONCLUSIVE"}
	input.Scope = &report.ScopeSnapshot{Digest: "scope-v1", Version: 1, Profile: "PROPERTY_COMPONENTS"}
	input.Handover = &report.HandoverSection{Entries: []report.HandoverEntry{}}
	input.Provenance = &report.Provenance{PromptRevision: "prompt-v1", ModelRevision: "UNKNOWN", ScopeDigest: "scope-v1"}
	first, err := Create(context.Background(), db, input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var old database.ReportSnapshot
	if err := db.First(&old, "id=?", first.SnapshotID).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot report.Snapshot
	if err := json.Unmarshal(old.CanonicalJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaVersion != 3 || snapshot.VerificationCode != "" || strings.Contains(string(old.CanonicalJSON), "verificationCode") {
		t.Fatalf("snapshot schema/code persisted in JSON=%d/%q", snapshot.SchemaVersion, snapshot.VerificationCode)
	}
	replay, err := Create(context.Background(), db, input, time.Now().Add(time.Minute))
	if err != nil || replay.SnapshotID != first.SnapshotID || replay.Version != first.Version {
		t.Fatalf("duplicate input produced %+v err=%v", replay, err)
	}
	input.PromptDigest = "prompt-v2"
	input.Provenance.PromptRevision = "prompt-v2"
	second, err := Create(context.Background(), db, input, time.Now().Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if second.Version != first.Version+1 || second.SnapshotID == first.SnapshotID {
		t.Fatalf("changed report facts did not append: first=%+v second=%+v", first, second)
	}
	var unchanged database.ReportSnapshot
	if err := db.First(&unchanged, "id=?", old.ID).Error; err != nil {
		t.Fatal(err)
	}
	if string(unchanged.CanonicalJSON) != string(old.CanonicalJSON) || string(unchanged.HTML) != string(old.HTML) {
		t.Fatal("reissue mutated an issued historical artifact")
	}
}

func TestV3PublishesWhenHandlerRequestsAutomaticAndDeduplicatesExecutionTime(t *testing.T) {
	db := publicationDB(t)
	tenant, inspection := identity.NewID(), identity.NewID()
	input := automaticInput(tenant, inspection, "AUTOMATIC", 0, "prompt-v1")
	input.Checklist = &report.ChecklistSection{Rooms: []report.ChecklistRoom{{Key: "kitchen", Name: "Cozinha", Components: []report.ChecklistComponent{{Key: "floor", Label: "Piso", Status: "OBSERVED"}}}}, Coverage: report.ChecklistCoverage{Expected: 1, FullyAssessed: 1}}
	input.Processing = &report.ProcessingState{Status: "COMPLETED"}
	input.Scope = &report.ScopeSnapshot{Digest: "scope-v1", Version: 1, Profile: "PROPERTY_COMPONENTS"}
	input.Handover = &report.HandoverSection{Entries: []report.HandoverEntry{}}
	input.Provenance = &report.Provenance{PromptRevision: "prompt-v1", ModelRevision: "UNKNOWN", ScopeDigest: "scope-v1"}
	first, err := Create(context.Background(), db, input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var publication database.ReportPublication
	if err := db.Where("snapshot_id=?", first.SnapshotID).Take(&publication).Error; err != nil || publication.Status != "PUBLISHED" || publication.ClientMutationID != AutomaticMutationID(first.SnapshotID) {
		t.Fatalf("v3 manual-policy publication=%+v err=%v", publication, err)
	}
	input.Context.Inspection.GeneratedAt = "2026-12-01T12:00:00Z"
	replay, err := Create(context.Background(), db, input, time.Now().Add(time.Minute))
	if err != nil || replay.SnapshotID != first.SnapshotID || replay.Version != first.Version {
		t.Fatalf("execution-time-only change created a version: first=%+v replay=%+v err=%v", first, replay, err)
	}
}

func TestV3CanceledPublicationModeDoesNotPublish(t *testing.T) {
	db := publicationDB(t)
	input := automaticInput(identity.NewID(), identity.NewID(), "", 0, "cancelled")
	input.Checklist = &report.ChecklistSection{Rooms: []report.ChecklistRoom{}, Coverage: report.ChecklistCoverage{UnknownScope: true}}
	input.Processing = &report.ProcessingState{Status: "INCONCLUSIVE"}
	input.Scope = &report.ScopeSnapshot{Unknown: true}
	input.Handover = &report.HandoverSection{Entries: []report.HandoverEntry{}}
	input.Provenance = &report.Provenance{PromptRevision: "cancelled"}
	created, err := Create(context.Background(), db, input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var publications int64
	if err := db.Model(&database.ReportPublication{}).Where("snapshot_id=?", created.SnapshotID).Count(&publications).Error; err != nil {
		t.Fatal(err)
	}
	if created.PublicationID != (identity.ID{}) || publications != 0 {
		t.Fatalf("empty publication mode released v3 snapshot: result=%+v publications=%d", created, publications)
	}
}

func TestV3SnapshotSanitizesUserURLsBeforeValidation(t *testing.T) {
	db := publicationDB(t)
	input := automaticInput(identity.NewID(), identity.NewID(), "AUTOMATIC", 1, "url-note")
	input.Checklist = &report.ChecklistSection{Rooms: []report.ChecklistRoom{{Key: "room", Name: "Cozinha", Components: []report.ChecklistComponent{{Key: "floor", Label: "Piso", Status: "OBSERVED", Description: "Ver https://exemplo.com/pasta"}}}}, Coverage: report.ChecklistCoverage{Expected: 1, FullyAssessed: 1}}
	input.Processing = &report.ProcessingState{Status: "COMPLETED"}
	input.Scope = &report.ScopeSnapshot{Document: json.RawMessage(`{"note":"escopo sem URL"}`)}
	input.Handover = &report.HandoverSection{Entries: []report.HandoverEntry{}}
	input.Provenance = &report.Provenance{PromptRevision: "url-note"}
	created, err := Create(context.Background(), db, input, time.Now())
	if err != nil {
		t.Fatalf("user URL prevented report creation: %v", err)
	}
	if created.PublicationID == (identity.ID{}) {
		t.Fatal("URL-bearing observation prevented automatic publication")
	}
	var stored database.ReportSnapshot
	if err := db.Where("id=?", created.SnapshotID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored.CanonicalJSON), "https://exemplo.com") || !strings.Contains(string(stored.CanonicalJSON), "[endereço removido]") {
		t.Fatalf("canonical report URL was not neutralized: %s", stored.CanonicalJSON)
	}
}

// TestV3SnapshotSanitizesURLsInsideJSONPayloads issues a v3 report whose
// handover note and pinned scope carry a URL followed by escaped quotes (the
// note that used to corrupt the serialized JSON) and variations of it.
func TestV3SnapshotSanitizesURLsInsideJSONPayloads(t *testing.T) {
	// The last forms carry only a scheme (round 8): the validation rejects
	// them, so the sanitizer must remove them as well.
	notes := []string{`veja https://x.com/"abc"`, `fim https://x.com/foto`, `barra \\ e https://x.com/a "b"`, "linha https://x.com/\"c\"\ncafé 🏠", "veja https:// depois", "link: HTTP://", "termina em Https://"}
	for index, note := range notes {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			db := publicationDB(t)
			input := automaticInput(identity.NewID(), identity.NewID(), "AUTOMATIC", 1, "json-note")
			payload, err := json.Marshal(map[string]any{"note": note})
			if err != nil {
				t.Fatal(err)
			}
			input.Checklist = &report.ChecklistSection{Rooms: []report.ChecklistRoom{}, Coverage: report.ChecklistCoverage{UnknownScope: true}}
			input.Processing = &report.ProcessingState{Status: "COMPLETED"}
			input.Scope = &report.ScopeSnapshot{Document: json.RawMessage(`{"rooms":[],"notes":` + string(payload) + `}`)}
			input.Handover = &report.HandoverSection{Entries: []report.HandoverEntry{{Key: "note", Kind: "NOTE", Status: "DECLARED", Payload: payload, Declarant: "Participante"}}}
			input.Provenance = &report.Provenance{PromptRevision: "json-note"}
			created, err := Create(context.Background(), db, input, time.Now())
			if err != nil {
				t.Fatalf("URL inside a JSON note blocked the report: %v", err)
			}
			var stored database.ReportSnapshot
			if err := db.Where("id=?", created.SnapshotID).Take(&stored).Error; err != nil {
				t.Fatal(err)
			}
			var snapshot report.Snapshot
			if err := json.Unmarshal(stored.CanonicalJSON, &snapshot); err != nil {
				t.Fatal(err)
			}
			var decoded map[string]string
			if err := json.Unmarshal(snapshot.Handover.Entries[0].Payload, &decoded); err != nil {
				t.Fatalf("stored handover payload is invalid JSON: %v", err)
			}
			if strings.Contains(strings.ToLower(decoded["note"]), "://") || !strings.Contains(decoded["note"], "[endereço removido]") || strings.Contains(strings.ToLower(string(snapshot.Scope.Document)), "://") {
				t.Fatalf("URL not neutralized inside JSON values: note=%q scope=%s", decoded["note"], snapshot.Scope.Document)
			}
		})
	}
}

// TestV3ReportNumberStaysStableAcrossVersions checks the v3-only rule: every
// version of an inspection keeps the report number of its first v3 version.
func TestV3ReportNumberStaysStableAcrossVersions(t *testing.T) {
	db := publicationDB(t)
	input := publicationV3(automaticInput(identity.NewID(), identity.NewID(), "MANUAL", 1, "stable-1"))
	reportIDs := make([]string, 0, 3)
	for index := 0; index < 3; index++ {
		input.PromptDigest = fmt.Sprintf("stable-%d", index)
		created, err := Create(context.Background(), db, input, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if created.Version != index+1 {
			t.Fatalf("version=%d, want %d", created.Version, index+1)
		}
		var stored database.ReportSnapshot
		if err := db.Where("id=?", created.SnapshotID).Take(&stored).Error; err != nil {
			t.Fatal(err)
		}
		var snapshot report.Snapshot
		if err := json.Unmarshal(stored.CanonicalJSON, &snapshot); err != nil {
			t.Fatal(err)
		}
		reportIDs = append(reportIDs, snapshot.ReportID)
	}
	if reportIDs[0] == "" || reportIDs[1] != reportIDs[0] || reportIDs[2] != reportIDs[0] {
		t.Fatalf("v3 report number changed across versions: %v", reportIDs)
	}
}

// TestV1AndV2KeepHistoricalSnapshotBytesAndReportIDs pins the pre-v3 rules:
// URL text is not rewritten, the canonical JSON is exactly the historical
// struct (no v3 fields), version N carries the snapshot ID of N-1 as its
// report number, and a replay of version 2 or later is not deduplicated.
func TestV1AndV2KeepHistoricalSnapshotBytesAndReportIDs(t *testing.T) {
	for _, schema := range []string{"v1", "v2"} {
		t.Run(schema, func(t *testing.T) {
			db := publicationDB(t)
			input := automaticInput(identity.NewID(), identity.NewID(), "MANUAL", 1, "generated-at-"+schema)
			input.Evidence = []report.Evidence{{ID: "note-photo", RequirementKey: "overview", Role: "CURRENT", Description: `ver https://example.test/"raw" e \\ barra`}}
			schemaVersion := 1
			if schema == "v2" {
				schemaVersion = 2
				input.Inventory = &report.InventorySection{Reference: report.InventoryReference{VersionID: "reference", VersionNumber: 1}, Problems: []report.InventoryEntry{}, Informational: []report.InventoryEntry{}}
			}
			historical := func(reportID string) []byte {
				t.Helper()
				expected := report.Snapshot{SchemaVersion: schemaVersion, ReportID: reportID, InspectionID: input.InspectionID.String(), Mode: input.Mode, Classification: input.Classification, TemplateVersionID: input.TemplateVersionID, ReferenceVersionID: input.ReferenceVersionID, PromptDigest: input.PromptDigest, ReasonCodes: input.ReasonCodes, Timeline: input.Timeline, Coverage: input.Coverage, Context: input.Context, Requirements: input.Requirements, Findings: input.Findings, Evidence: input.Evidence, Inventory: input.Inventory, Advisory: "Esta triagem interna descreve alterações observadas e não atribui culpa, custo, responsabilidade ou consequência automática."}
				canonical, _, err := report.CanonicalJSON(expected)
				if err != nil {
					t.Fatal(err)
				}
				return canonical
			}
			row := func(id identity.ID) database.ReportSnapshot {
				t.Helper()
				var value database.ReportSnapshot
				if err := db.Where("id=?", id).Take(&value).Error; err != nil {
					t.Fatal(err)
				}
				return value
			}
			first, err := Create(context.Background(), db, input, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if got := row(first.SnapshotID); string(got.CanonicalJSON) != string(historical(first.SnapshotID.String())) || got.SchemaVersion != schemaVersion {
				t.Fatalf("%s first snapshot differs from the historical bytes:\n%s", schema, got.CanonicalJSON)
			}
			replay, err := Create(context.Background(), db, input, time.Now())
			if err != nil || replay.SnapshotID != first.SnapshotID {
				t.Fatalf("%s replay of version 1 = %+v (%v), want the stored version", schema, replay, err)
			}
			input.Context.Inspection.GeneratedAt = "2026-12-31T23:59:00Z"
			second, err := Create(context.Background(), db, input, time.Now().Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if second.Version != first.Version+1 || second.SnapshotID == first.SnapshotID {
				t.Fatalf("%s GeneratedAt change was deduplicated: first=%+v second=%+v", schema, first, second)
			}
			if got := row(second.SnapshotID); string(got.CanonicalJSON) != string(historical(first.SnapshotID.String())) {
				t.Fatalf("%s version 2 must carry the snapshot ID of version 1 as report number with historical bytes:\n%s", schema, got.CanonicalJSON)
			}
			third, err := Create(context.Background(), db, input, time.Now().Add(2*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if third.Version != second.Version+1 {
				t.Fatalf("%s replay of version 2 was deduplicated (%+v); the historical rule creates a new version", schema, third)
			}
			if got := row(third.SnapshotID); string(got.CanonicalJSON) != string(historical(second.SnapshotID.String())) {
				t.Fatalf("%s version 3 must carry the snapshot ID of version 2 as report number:\n%s", schema, got.CanonicalJSON)
			}
		})
	}
}

// TestAutomaticPublicationFailureRollsBackSnapshot proves the release shares
// the snapshot transaction: if it cannot be written, no snapshot remains.
func TestAutomaticPublicationFailureRollsBackSnapshot(t *testing.T) {
	db := publicationDB(t)
	if err := db.Exec("DROP TABLE audit.events").Error; err != nil {
		t.Fatal(err)
	}
	tenant, inspection := identity.NewID(), identity.NewID()
	if _, err := Create(context.Background(), db, automaticInput(tenant, inspection, "AUTOMATIC", 1, "prompt-rollback"), time.Now()); err == nil {
		t.Fatal("publication failure must fail finalization")
	}
	var snapshots, publications int64
	db.Model(&database.ReportSnapshot{}).Count(&snapshots)
	db.Model(&database.ReportPublication{}).Count(&publications)
	if snapshots != 0 || publications != 0 {
		t.Fatalf("partial state survived: snapshots=%d publications=%d", snapshots, publications)
	}
}
