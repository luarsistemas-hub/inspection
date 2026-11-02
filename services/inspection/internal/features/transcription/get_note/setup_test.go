package get_note

import (
	"context"
	"errors"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupLoadsNoteAndRevisions(t *testing.T) {
	db := getDB(t)
	tenant, note, inspection := identity.NewID(), identity.NewID(), identity.NewID()
	revisionID := identity.NewID()
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,status,version,inspection_id,requirement_key,target_kind,anchor_status,audio_deletion_status,duration_ms,current_revision_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenant, note, "COMPLETED", 2, inspection, "room-1", "PHOTO", "VALID", "DELETED", 1200, revisionID, time.Now(), time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO transcription.transcript_revisions(id,tenant_id,note_id,revision_number,text,kind,created_at) VALUES(?,?,?,?,?,?,?)`, revisionID, tenant, note, 1, "texto", "MODEL", time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	out, err := Setup(context.Background(), Dependencies{DB: db, Within: getWithin(db)}, Command{TenantID: tenant, NoteID: note})
	if err != nil || out.ID != note || len(out.Revisions) != 1 || out.Status != "COMPLETED" {
		t.Fatalf("note=%+v err=%v", out, err)
	}
}

func TestSetupValidatesAndPropagatesDependencyFailure(t *testing.T) {
	db := getDB(t)
	if _, err := Setup(context.Background(), Dependencies{DB: db}, Command{}); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
	failure := errors.New("database unavailable")
	if _, err := Setup(context.Background(), Dependencies{DB: db, Within: func(context.Context, identity.ID, func(*gorm.DB) error) error { return failure }}, Command{TenantID: identity.NewID(), NoteID: identity.NewID()}); !errors.Is(err, failure) {
		t.Fatalf("expected dependency failure, got %v", err)
	}
}

func TestSetupHidesStagedModelRevisionsForNoncompletedNotes(t *testing.T) {
	db := getDB(t)
	tenant, inspection := identity.NewID(), identity.NewID()
	for _, status := range []string{"PROCESSING", "FAILED", "CANCELED"} {
		note, revision := identity.NewID(), identity.NewID()
		if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,status,version,inspection_id,requirement_key,target_kind,anchor_status,audio_deletion_status,duration_ms,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, tenant, note, status, 1, inspection, "room", "PHOTO", "VALID", "PENDING", 1000, time.Now(), time.Now()).Error; err != nil {
			t.Fatal(err)
		}
		insertModelRevision(t, db, tenant, note, revision, 1, 1, status)
		out, err := Setup(context.Background(), Dependencies{DB: db, Within: getWithin(db)}, Command{TenantID: tenant, NoteID: note})
		if err != nil || len(out.Revisions) != 0 {
			t.Fatalf("status=%s revisions=%+v err=%v", status, out.Revisions, err)
		}
	}
}

func TestSetupExcludesModelRevisionsAfterCurrentRevisionWhenCompleted(t *testing.T) {
	db := getDB(t)
	tenant, note, inspection := identity.NewID(), identity.NewID(), identity.NewID()
	current, orphan := identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,status,version,inspection_id,requirement_key,target_kind,anchor_status,audio_deletion_status,duration_ms,current_revision_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenant, note, "COMPLETED", 1, inspection, "room", "PHOTO", "VALID", "PENDING", 1000, current, time.Now(), time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	for i, id := range []identity.ID{current, orphan} {
		if err := db.Exec(`INSERT INTO transcription.transcript_revisions(id,tenant_id,note_id,revision_number,text,kind,created_at) VALUES(?,?,?,?,?,?,?)`, id, tenant, note, i+1, "model", "MODEL", time.Now()).Error; err != nil {
			t.Fatal(err)
		}
	}
	out, err := Setup(context.Background(), Dependencies{DB: db, Within: getWithin(db)}, Command{TenantID: tenant, NoteID: note})
	if err != nil || len(out.Revisions) != 1 || out.Revisions[0].ID != current {
		t.Fatalf("revisions=%+v err=%v", out.Revisions, err)
	}
}

func TestSetupKeepsCurrentAndCitedRevisionsAfterCompletedNoteIsDiscarded(t *testing.T) {
	db := getDB(t)
	tenant, note, inspection := identity.NewID(), identity.NewID(), identity.NewID()
	cited, current, staged := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,status,version,inspection_id,requirement_key,target_kind,anchor_status,audio_deletion_status,duration_ms,current_revision_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenant, note, "CANCELED", 3, inspection, "room", "PHOTO", "VALID", "SCHEDULED", 1000, current, time.Now(), time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	insertModelRevision(t, db, tenant, note, cited, 1, 1, "COMPLETED")
	insertModelRevision(t, db, tenant, note, current, 2, 2, "COMPLETED")
	insertModelRevision(t, db, tenant, note, staged, 3, 3, "CANCELED")
	out, err := Setup(context.Background(), Dependencies{DB: db, Within: getWithin(db)}, Command{TenantID: tenant, NoteID: note})
	if err != nil || len(out.Revisions) != 2 || out.Revisions[0].ID != cited || out.Revisions[1].ID != current {
		t.Fatalf("discarded note revisions=%+v err=%v", out.Revisions, err)
	}
}

func TestSetupHidesStagingOfUnfinishedJobEvenBeforeCurrentRevision(t *testing.T) {
	db := getDB(t)
	tenant, note, inspection := identity.NewID(), identity.NewID(), identity.NewID()
	staged, declaration := identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,status,version,inspection_id,requirement_key,target_kind,anchor_status,audio_deletion_status,duration_ms,current_revision_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenant, note, "PROCESSING", 2, inspection, "room", "PHOTO", "VALID", "PENDING", 1000, declaration, time.Now(), time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	insertModelRevision(t, db, tenant, note, staged, 1, 1, "PROCESSING")
	insertHumanRevision(t, db, tenant, note, declaration, 2, "PARTICIPANT_DECLARATION")
	out, err := Setup(context.Background(), Dependencies{DB: db, Within: getWithin(db)}, Command{TenantID: tenant, NoteID: note})
	if err != nil || len(out.Revisions) != 1 || out.Revisions[0].ID != declaration {
		t.Fatalf("staging of an unfinished job became visible: revisions=%+v err=%v", out.Revisions, err)
	}
}

func TestSetupShowsCompletedModelRevisionAfterDeclarationAndRetry(t *testing.T) {
	db := getDB(t)
	tenant, note, inspection := identity.NewID(), identity.NewID(), identity.NewID()
	declaration, model := identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,status,version,inspection_id,requirement_key,target_kind,anchor_status,audio_deletion_status,duration_ms,current_revision_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenant, note, "COMPLETED", 4, inspection, "room", "PHOTO", "VALID", "SCHEDULED", 1000, declaration, time.Now(), time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	// Generation 1 failed, the participant declared the text, and the retry
	// (generation 2) completed afterwards without replacing the declaration.
	insertHumanRevision(t, db, tenant, note, declaration, 1, "PARTICIPANT_DECLARATION")
	insertModelRevision(t, db, tenant, note, model, 2, 2, "COMPLETED")
	out, err := Setup(context.Background(), Dependencies{DB: db, Within: getWithin(db)}, Command{TenantID: tenant, NoteID: note})
	if err != nil || len(out.Revisions) != 2 || out.Revisions[0].ID != declaration || out.Revisions[1].ID != model {
		t.Fatalf("completed model revision is not visible: revisions=%+v err=%v", out.Revisions, err)
	}
	if out.CurrentRevisionID == nil || *out.CurrentRevisionID != declaration {
		t.Fatalf("current revision=%v, want the human declaration %s", out.CurrentRevisionID, declaration)
	}
}

// insertModelRevision stores a MODEL revision produced by a job of the given
// generation and status, linked through its usage.llm_calls row.
func insertModelRevision(t *testing.T, db *gorm.DB, tenant, note, revision identity.ID, number, generation int, jobStatus string) {
	t.Helper()
	job, call := identity.NewID(), identity.NewID()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO transcription.jobs(tenant_id,id,note_id,generation,status) VALUES(?,?,?,?,?)`, []any{tenant, job, note, generation, jobStatus}},
		{`INSERT INTO usage.llm_calls(tenant_id,call_id,job_id,replay_generation) VALUES(?,?,?,?)`, []any{tenant, call, job, generation}},
		{`INSERT INTO transcription.transcript_revisions(id,tenant_id,note_id,revision_number,text,kind,call_id,created_at) VALUES(?,?,?,?,?,?,?,?)`, []any{revision, tenant, note, number, "modelo", "MODEL", call, time.Now()}},
	} {
		if err := db.Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func insertHumanRevision(t *testing.T, db *gorm.DB, tenant, note, revision identity.ID, number int, kind string) {
	t.Helper()
	if err := db.Exec(`INSERT INTO transcription.transcript_revisions(id,tenant_id,note_id,revision_number,text,kind,created_at) VALUES(?,?,?,?,?,?,?)`, revision, tenant, note, number, "humano", kind, time.Now()).Error; err != nil {
		t.Fatal(err)
	}
}

func getDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"transcription", "usage"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, ddl := range []string{`CREATE TABLE transcription.jobs(tenant_id text,id text,note_id text,generation integer,status text)`, `CREATE TABLE usage.llm_calls(tenant_id text,call_id text,job_id text,replay_generation integer)`, `CREATE TABLE transcription.audio_notes(tenant_id text,id text,status text,version integer,inspection_id text,responsibility_id text,requirement_key text,target_kind text,anchor_status text,audio_deletion_status text,photo_media_id text,video_attempt_id text,source_media_id text,start_ms integer,end_ms integer,duration_ms integer,highlighted boolean,highlight_source text,current_revision_id text,text_confirmed boolean,no_speech boolean,safe_failure_code text,created_at datetime,updated_at datetime)`, `CREATE TABLE transcription.transcript_revisions(id text,tenant_id text,note_id text,revision_number integer,text text,kind text,author_id text,call_id text,created_at datetime)`} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}
func getWithin(db *gorm.DB) func(context.Context, identity.ID, func(*gorm.DB) error) error {
	return func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
}
