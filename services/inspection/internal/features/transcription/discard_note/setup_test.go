package discard_note

import (
	"context"
	"errors"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupDiscardsAndSchedulesCleanup(t *testing.T) {
	db := discardDB(t)
	tenant, note := identity.NewID(), identity.NewID()
	key := "private/audio"
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,status,version,upload_key,responsibility_id) VALUES(?,?,?,?,?,?)`, tenant, note, "QUEUED", 1, key, identity.NewID()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO transcription.jobs(tenant_id,note_id,status) VALUES(?,?,?)`, tenant, note, "QUEUED").Error; err != nil {
		t.Fatal(err)
	}
	if err := Setup(context.Background(), Dependencies{DB: db, Within: discardWithin(db)}, Command{TenantID: tenant, NoteID: note, ExpectedVersion: 1, ClientMutationID: "discard-1"}); err != nil {
		t.Fatal(err)
	}
	var status, jobStatus, deletionStatus string
	if err := db.Raw(`SELECT status FROM transcription.audio_notes WHERE tenant_id=? AND id=?`, tenant, note).Row().Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(`SELECT status FROM transcription.jobs WHERE tenant_id=? AND note_id=?`, tenant, note).Row().Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(`SELECT status FROM transcription.audio_deletions WHERE tenant_id=? AND note_id=?`, tenant, note).Row().Scan(&deletionStatus); err != nil {
		t.Fatal(err)
	}
	if status != "CANCELED" || jobStatus != "CANCELED" || deletionStatus != "PENDING" {
		t.Fatalf("status=%s job=%s deletion=%s", status, jobStatus, deletionStatus)
	}
}

func TestSetupRejectsProcessingAndDependencies(t *testing.T) {
	db := discardDB(t)
	tenant, note := identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,status,version) VALUES(?,?,?,?)`, tenant, note, "PROCESSING", 1).Error; err != nil {
		t.Fatal(err)
	}
	cmd := Command{TenantID: tenant, NoteID: note, ExpectedVersion: 1, ClientMutationID: "discard"}
	if err := Setup(context.Background(), Dependencies{DB: db, Within: discardWithin(db)}, cmd); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("expected invalid state, got %v", err)
	}
	failure := errors.New("database unavailable")
	if err := Setup(context.Background(), Dependencies{DB: db, Within: func(context.Context, identity.ID, func(*gorm.DB) error) error { return failure }}, cmd); !errors.Is(err, failure) {
		t.Fatalf("expected dependency error, got %v", err)
	}
	if err := Setup(context.Background(), Dependencies{}, Command{}); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
}

func discardDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS transcription").Error; err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{`CREATE TABLE transcription.audio_notes(tenant_id text,id text,status text,version integer,upload_key text,upload_id text,audio_deletion_status text,updated_at datetime,responsibility_id text)`, `CREATE TABLE transcription.jobs(tenant_id text,note_id text,status text,updated_at datetime)`, `CREATE TABLE transcription.audio_deletions(tenant_id text,id text,note_id text,object_generation integer,object_key text,derivative_keys blob,status text,created_at datetime,updated_at datetime)`, `CREATE TABLE transcription.mutation_receipts(tenant_id text,id text,note_id text,client_mutation_id text,operation text,payload_digest text,result_version integer,created_at datetime)`} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}
func discardWithin(db *gorm.DB) func(context.Context, identity.ID, func(*gorm.DB) error) error {
	return func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
}
