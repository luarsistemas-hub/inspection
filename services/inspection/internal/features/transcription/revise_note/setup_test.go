package revise_note

import (
	"context"
	"errors"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupAppendsImmutableRevisionUT005(t *testing.T) {
	db := reviseDB(t)
	tenant, note, old := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,status,version,current_revision_id) VALUES(?,?,?,?,?)`, tenant, note, "COMPLETED", 1, old).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO transcription.transcript_revisions(id,tenant_id,note_id,revision_number,text,kind) VALUES(?,?,?,?,?,?)`, old, tenant, note, 1, "cinco pratos", "MODEL").Error; err != nil {
		t.Fatal(err)
	}
	within := func(ctx context.Context, tenantID identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
	author := identity.NewID()
	_, err := Setup(context.Background(), Dependencies{DB: db, Within: within}, Command{TenantID: tenant, NoteID: note, ExpectedVersion: 1, ClientMutationID: "edit-1", Text: "seis pratos", Kind: "HUMAN_EDIT", AuthorID: &author})
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Table("transcription.transcript_revisions").Where("tenant_id=? AND note_id=?", tenant, note).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("revision count=%d err=%v", count, err)
	}
	var savedAuthor identity.ID
	if err := db.Raw(`SELECT author_id FROM transcription.transcript_revisions WHERE tenant_id=? AND note_id=? AND kind='HUMAN_EDIT'`, tenant, note).Row().Scan(&savedAuthor); err != nil || savedAuthor != author {
		t.Fatalf("saved author=%s err=%v, want %s", savedAuthor, err, author)
	}
	var preserved string
	_ = db.Raw(`SELECT text FROM transcription.transcript_revisions WHERE id=?`, old).Scan(&preserved).Error
	if preserved != "cinco pratos" {
		t.Fatalf("original revision changed: %q", preserved)
	}
}

func TestSetupRejectsStaleVersionUT006(t *testing.T) {
	db := reviseDB(t)
	tenant, note := identity.NewID(), identity.NewID()
	_ = db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,status,version) VALUES(?,?,?,?)`, tenant, note, "COMPLETED", 2).Error
	within := func(ctx context.Context, tenantID identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
	author := identity.NewID()
	_, err := Setup(context.Background(), Dependencies{DB: db, Within: within}, Command{TenantID: tenant, NoteID: note, ExpectedVersion: 1, ClientMutationID: "edit-stale", Text: "texto válido", Kind: "HUMAN_EDIT", AuthorID: &author})
	if !apperror.Is(err, apperror.Conflict) {
		t.Fatalf("expected CONFLICT, got %v", err)
	}
}

func TestSetupValidatesDependencies(t *testing.T) {
	_, err := Setup(context.Background(), Dependencies{}, Command{})
	if !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}
}

func TestSetupRequiresExistingRevisionForHumanEditUT005(t *testing.T) {
	db := reviseDB(t)
	tenant, note := identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,status,version) VALUES(?,?,?,?)`, tenant, note, "QUEUED", 1).Error; err != nil {
		t.Fatal(err)
	}
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
	author := identity.NewID()
	_, err := Setup(context.Background(), Dependencies{DB: db, Within: within}, Command{TenantID: tenant, NoteID: note, ExpectedVersion: 1, ClientMutationID: "queued-edit", Text: "texto válido", Kind: "HUMAN_EDIT", AuthorID: &author})
	if !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("expected INVALID_STATE for queued note without revision, got %v", err)
	}
}

func TestSetupRejectsFailedNoteWithQueuedJob(t *testing.T) {
	db := reviseDB(t)
	tenant, note := identity.NewID(), identity.NewID()
	responsibility := identity.NewID()
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,status,version,anchor_status,responsibility_id) VALUES(?,?,?,?,?,?)`, tenant, note, "FAILED", 1, "VALID", responsibility).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO capture.capture_drafts(id,tenant_id,responsibility_id,status) VALUES(?,?,?,'OPEN')`, identity.NewID(), tenant, responsibility).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO transcription.jobs(tenant_id,note_id,status) VALUES(?,?,?)`, tenant, note, "QUEUED").Error; err != nil {
		t.Fatal(err)
	}
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
	_, err := Setup(context.Background(), Dependencies{DB: db, Within: within}, Command{TenantID: tenant, NoteID: note, ExpectedVersion: 1, ClientMutationID: "failed-with-queued-job", Text: "texto válido", Kind: "PARTICIPANT_DECLARATION", IsParticipant: true})
	if !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("expected INVALID_STATE for a FAILED note with a queued job, got %v", err)
	}
}

func TestSetupParticipantDeclarationOnlyAfterFailedJobIsTerminal(t *testing.T) {
	db := reviseDB(t)
	tenant, note, responsibility := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,status,version,anchor_status,responsibility_id) VALUES(?,?,?,?,?,?)`, tenant, note, "FAILED", 1, "VALID", responsibility).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO capture.capture_drafts(id,tenant_id,responsibility_id,status) VALUES(?,?,?,?)`, identity.NewID(), tenant, responsibility, "OPEN").Error; err != nil {
		t.Fatal(err)
	}
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
	if _, err := Setup(context.Background(), Dependencies{DB: db, Within: within}, Command{TenantID: tenant, NoteID: note, ExpectedVersion: 1, ClientMutationID: "fallback", Text: "cinco cadeiras", Kind: "PARTICIPANT_DECLARATION"}); err != nil {
		t.Fatalf("failed note declaration: %v", err)
	}
	var kind string
	if err := db.Raw(`SELECT kind FROM transcription.transcript_revisions WHERE tenant_id=? AND note_id=?`, tenant, note).Row().Scan(&kind); err != nil || kind != "PARTICIPANT_DECLARATION" {
		t.Fatalf("revision kind=%q err=%v", kind, err)
	}
	queuedTenant, queuedNote, queuedResp := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,status,version,anchor_status,responsibility_id) VALUES(?,?,?,?,?,?)`, queuedTenant, queuedNote, "RETRY_WAIT", 1, "VALID", queuedResp).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO capture.capture_drafts(id,tenant_id,responsibility_id,status) VALUES(?,?,?,?)`, identity.NewID(), queuedTenant, queuedResp, "OPEN").Error; err != nil {
		t.Fatal(err)
	}
	_, err := Setup(context.Background(), Dependencies{DB: db, Within: within}, Command{TenantID: queuedTenant, NoteID: queuedNote, ExpectedVersion: 1, ClientMutationID: "fallback-retry", Text: "texto informado", Kind: "PARTICIPANT_DECLARATION"})
	if !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("expected RETRY_WAIT declaration to be rejected, got %v", err)
	}
}

func TestSetupPropagatesPersistenceFailure(t *testing.T) {
	db := reviseDB(t)
	failure := errors.New("database unavailable")
	within := func(context.Context, identity.ID, func(*gorm.DB) error) error { return failure }
	author := identity.NewID()
	_, err := Setup(context.Background(), Dependencies{DB: db, Within: within}, Command{TenantID: identity.NewID(), NoteID: identity.NewID(), ExpectedVersion: 1, ClientMutationID: "edit-1", Text: "texto válido", Kind: "HUMAN_EDIT", AuthorID: &author})
	if !errors.Is(err, failure) {
		t.Fatalf("expected persistence failure, got %v", err)
	}
}

func reviseDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"transcription", "capture"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, ddl := range []string{`CREATE TABLE transcription.audio_notes(tenant_id text,id text,status text,version integer,anchor_status text,responsibility_id text,current_revision_id text,text_confirmed boolean,updated_at datetime)`, `CREATE TABLE transcription.transcript_revisions(id text,tenant_id text,note_id text,revision_number integer,text text,kind text,author_id text,created_at datetime)`, `CREATE TABLE transcription.mutation_receipts(id text,tenant_id text,note_id text,client_mutation_id text,operation text,payload_digest text,result_version integer,created_at datetime)`, `CREATE TABLE transcription.jobs(tenant_id text,note_id text,status text)`, `CREATE TABLE capture.capture_drafts(id text PRIMARY KEY,tenant_id text,responsibility_id text,status text)`} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}
