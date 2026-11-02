package publication

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	publicationcore "inspection/services/inspection/internal/features/reports/publication/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func ledgerDB(t *testing.T) *gorm.DB {
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
	for _, schema := range []string{"reports", "audit", "messaging"} {
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

func seedSnapshot(t *testing.T, db *gorm.DB, tenant, inspection identity.ID, version int) identity.ID {
	t.Helper()
	id := identity.NewID()
	row := database.ReportSnapshot{ID: id, TenantID: tenant, InspectionID: inspection, Mode: "HISTORICAL", Classification: "NORMAL", JSONDigest: id.String(), HTMLDigest: id.String(), SchemaVersion: 1, VersionNumber: version, PublicationPolicyVersion: 5, CanonicalJSON: []byte(`{}`), HTML: []byte(`""`), CreatedAt: time.Now().UTC()}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	return id
}

func publishTx(t *testing.T, db *gorm.DB, in PublishInput) (database.ReportPublication, error) {
	t.Helper()
	var out database.ReportPublication
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = PublishTx(context.Background(), tx, in, time.Now())
		return err
	})
	return out, err
}

func count(t *testing.T, db *gorm.DB, model any, where string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.Model(model).Where(where, args...).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// TestPublishTxLifecycleUT029ToUT031 covers non-final rejection (UT-029),
// mutation-key idempotency with one event (UT-030) and supersession (UT-031).
func TestPublishTxLifecycleUT029ToUT031(t *testing.T) {
	db := ledgerDB(t)
	tenant, inspection, actor := identity.NewID(), identity.NewID(), identity.NewID()
	first, second := seedSnapshot(t, db, tenant, inspection, 1), seedSnapshot(t, db, tenant, inspection, 2)
	if _, err := publishTx(t, db, PublishInput{TenantID: tenant, InspectionID: inspection, SnapshotID: first, ActorID: actor, ClientMutationID: "draft", Final: false}); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("non-final snapshot error = %v, want INVALID_STATE", err)
	}
	if count(t, db, &database.ReportPublication{}, "tenant_id=?", tenant) != 0 {
		t.Fatal("rejected publication left a row")
	}
	in := PublishInput{TenantID: tenant, InspectionID: inspection, SnapshotID: first, ActorID: actor, ClientMutationID: "key-1", Final: true}
	one, err := publishTx(t, db, in)
	if err != nil || one.Status != publicationcore.Published || one.PolicyVersion != 5 || one.ActorID != actor {
		t.Fatalf("publish = %+v (%v)", one, err)
	}
	again, err := publishTx(t, db, in)
	if err != nil || again.ID != one.ID {
		t.Fatalf("replay = %+v (%v), want same publication", again, err)
	}
	in.ClientMutationID = "key-2"
	if again, err = publishTx(t, db, in); err != nil || again.ID != one.ID {
		t.Fatalf("second key for a released snapshot = %+v (%v)", again, err)
	}
	if count(t, db, &database.OutboxIntent{}, "type=?", "report.published.v1") != 1 || count(t, db, &database.AuditEvent{}, "action=?", "report.published") != 1 {
		t.Fatal("idempotent replays must keep exactly one event and one audit record")
	}
	next, err := publishTx(t, db, PublishInput{TenantID: tenant, InspectionID: inspection, SnapshotID: second, ActorID: actor, ClientMutationID: "key-3", Final: true})
	if err != nil {
		t.Fatal(err)
	}
	var former database.ReportPublication
	if err := db.First(&former, "id=?", one.ID).Error; err != nil || former.Status != publicationcore.Superseded || former.SupersededBy == nil || *former.SupersededBy != next.ID || former.Version != 2 {
		t.Fatalf("former = %+v (%v)", former, err)
	}
	if count(t, db, &database.AuditEvent{}, "action=?", "report.publication_superseded") != 1 {
		t.Fatal("supersession must be audited")
	}
	if _, err := publishTx(t, db, PublishInput{TenantID: tenant, InspectionID: inspection, SnapshotID: first, ActorID: actor, ClientMutationID: "key-4", Final: true}); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("republishing a superseded snapshot error = %v, want INVALID_STATE", err)
	}
	if _, err := publishTx(t, db, PublishInput{TenantID: tenant, InspectionID: inspection, SnapshotID: identity.NewID(), ActorID: actor, ClientMutationID: "key-5", Final: true}); !apperror.Is(err, apperror.NotFound) {
		t.Fatalf("unknown snapshot error = %v, want NOT_FOUND", err)
	}
	other := identity.NewID()
	if _, err := publishTx(t, db, PublishInput{TenantID: other, InspectionID: inspection, SnapshotID: first, ActorID: actor, ClientMutationID: "key-6", Final: true}); !apperror.Is(err, apperror.NotFound) {
		t.Fatalf("foreign tenant error = %v, want NOT_FOUND", err)
	}
}

func TestPublishTxRequiresIdentityAndMutationKey(t *testing.T) {
	db := ledgerDB(t)
	for name, in := range map[string]PublishInput{
		"no tenant":   {SnapshotID: identity.NewID(), ClientMutationID: "k", Final: true},
		"no snapshot": {TenantID: identity.NewID(), ClientMutationID: "k", Final: true},
		"no key":      {TenantID: identity.NewID(), SnapshotID: identity.NewID(), ClientMutationID: "  ", Final: true},
	} {
		if _, err := publishTx(t, db, in); !apperror.Is(err, apperror.InvalidInput) {
			t.Errorf("%s: error = %v, want INVALID_INPUT", name, err)
		}
	}
}

// A mutation key identifies one release: reusing it for another snapshot is a
// CONFLICT, not a silent answer with the unrelated earlier publication.
func TestPublishTxRejectsMutationKeyReusedForAnotherSnapshot(t *testing.T) {
	db := ledgerDB(t)
	tenant, inspection, actor := identity.NewID(), identity.NewID(), identity.NewID()
	first, second := seedSnapshot(t, db, tenant, inspection, 1), seedSnapshot(t, db, tenant, inspection, 2)
	if _, err := publishTx(t, db, PublishInput{TenantID: tenant, InspectionID: inspection, SnapshotID: first, ActorID: actor, ClientMutationID: "shared-key", Final: true}); err != nil {
		t.Fatal(err)
	}
	_, err := publishTx(t, db, PublishInput{TenantID: tenant, InspectionID: inspection, SnapshotID: second, ActorID: actor, ClientMutationID: "shared-key", Final: true})
	if !apperror.Is(err, apperror.Conflict) {
		t.Fatalf("reused mutation key error = %v, want CONFLICT", err)
	}
	if count(t, db, &database.ReportPublication{}, "tenant_id=? AND snapshot_id=?", tenant, second) != 0 {
		t.Fatal("the conflicting publication left a row")
	}
}

// An invalidated release keeps its audit columns when a later release is
// published: only a PUBLISHED predecessor is superseded, and only its own
// supersession columns are written.
func TestPublishAfterInvalidationKeepsInvalidationAudit(t *testing.T) {
	db := ledgerDB(t)
	tenant, inspection, actor := identity.NewID(), identity.NewID(), identity.NewID()
	first, second := seedSnapshot(t, db, tenant, inspection, 1), seedSnapshot(t, db, tenant, inspection, 2)
	released, err := publishTx(t, db, PublishInput{TenantID: tenant, InspectionID: inspection, SnapshotID: first, ActorID: actor, ClientMutationID: "k1", Final: true})
	if err != nil {
		t.Fatal(err)
	}
	invalidatedAt := time.Now().UTC()
	if err := db.Model(&database.ReportPublication{}).Where("id=?", released.ID).Updates(map[string]any{"status": publicationcore.Invalidated, "invalidated_at": invalidatedAt, "invalidated_by": actor, "reason": "Correção", "version": released.Version + 1}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := publishTx(t, db, PublishInput{TenantID: tenant, InspectionID: inspection, SnapshotID: second, ActorID: actor, ClientMutationID: "k2", Final: true}); err != nil {
		t.Fatal(err)
	}
	var former database.ReportPublication
	if err := db.First(&former, "id=?", released.ID).Error; err != nil {
		t.Fatal(err)
	}
	if former.Status != publicationcore.Invalidated || former.InvalidatedAt == nil || former.InvalidatedBy == nil || *former.InvalidatedBy != actor || former.Reason != "Correção" || former.SupersededBy != nil || former.Version != 2 {
		t.Fatalf("invalidated release was rewritten: %+v", former)
	}
}

// Publication errors reach the UI, so they are Portuguese with stable codes.
func TestPublicationErrorsArePortuguese(t *testing.T) {
	db := ledgerDB(t)
	tenant, inspection := identity.NewID(), identity.NewID()
	draft := seedSnapshot(t, db, tenant, inspection, 1)
	_, err := publishTx(t, db, PublishInput{TenantID: tenant, InspectionID: inspection, SnapshotID: draft, ClientMutationID: "k", Final: false})
	if !apperror.Is(err, apperror.InvalidState) || err.Error() != "Somente laudos finais podem ser publicados." {
		t.Fatalf("non-final error = %v", err)
	}
	_, err = publishTx(t, db, PublishInput{TenantID: tenant, InspectionID: inspection, SnapshotID: identity.NewID(), ClientMutationID: "k", Final: true})
	if !apperror.Is(err, apperror.NotFound) || err.Error() != "Laudo não encontrado." {
		t.Fatalf("missing snapshot error = %v", err)
	}
}
