package generate_snapshot

import (
	"context"
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
