package retry_note

import (
	"context"
	"errors"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/llm"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupQueuesOneNewGeneration(t *testing.T) {
	db := retryDB(t)
	tenant, note := identity.NewID(), identity.NewID()
	inspection := identity.NewID()
	now := time.Now().UTC()
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,inspection_id,status,version,audio_deletion_status,duration_ms,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, tenant, note, inspection, "FAILED", 2, "PENDING", 1000, now.Add(-48*time.Hour), now.Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO transcription.jobs(tenant_id,id,note_id,generation,status,created_at) VALUES(?,?,?,?,?,?)`, tenant, identity.NewID(), note, 1, "FAILED", now.Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO transcription.tenant_policies VALUES(?,?,?,?,?)`, tenant, 10, 60000, 10, 60000).Error; err != nil {
		t.Fatal(err)
	}
	cmd := Command{TenantID: tenant, NoteID: note, ExpectedVersion: 2, ClientMutationID: "retry-1"}
	deps := Dependencies{DB: db, Within: retryWithin(db)}
	if err := Setup(context.Background(), deps, cmd); err != nil {
		t.Fatal(err)
	}
	if err := Setup(context.Background(), deps, cmd); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	var jobs, events int64
	if err := db.Table("transcription.jobs").Count(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("messaging.outbox").Count(&events).Error; err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.Raw(`SELECT status FROM transcription.audio_notes WHERE tenant_id=? AND id=?`, tenant, note).Row().Scan(&status); err != nil {
		t.Fatal(err)
	}
	if jobs != 2 || events != 1 || status != "QUEUED" {
		t.Fatalf("jobs=%d events=%d status=%s", jobs, events, status)
	}
	var profileKey string
	if err := db.Raw(`SELECT profile_key FROM transcription.jobs WHERE tenant_id=? AND note_id=? AND generation=2`, tenant, note).Row().Scan(&profileKey); err != nil {
		t.Fatal(err)
	}
	if profileKey != llm.DefaultTranscriptionProfile {
		t.Fatalf("retry profile=%q, want default %q", profileKey, llm.DefaultTranscriptionProfile)
	}
}

func TestSetupRejectsUnavailableStateAndDependencies(t *testing.T) {
	db := retryDB(t)
	tenant, note := identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,status,version,audio_deletion_status) VALUES(?,?,?,?,?)`, tenant, note, "COMPLETED", 1, "DELETED").Error; err != nil {
		t.Fatal(err)
	}
	cmd := Command{TenantID: tenant, NoteID: note, ExpectedVersion: 1, ClientMutationID: "retry"}
	if err := Setup(context.Background(), Dependencies{DB: db, Within: retryWithin(db)}, cmd); !apperror.Is(err, apperror.RetryUnavailable) {
		t.Fatalf("expected unavailable retry, got %v", err)
	}
	missingJobNote := identity.NewID()
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,status,version,audio_deletion_status) VALUES(?,?,?,?,?)`, tenant, missingJobNote, "FAILED", 1, "PENDING").Error; err != nil {
		t.Fatal(err)
	}
	err := Setup(context.Background(), Dependencies{DB: db, Within: retryWithin(db)}, Command{TenantID: tenant, NoteID: missingJobNote, ExpectedVersion: 1, ClientMutationID: "missing-job"})
	if !apperror.Is(err, apperror.RetryUnavailable) {
		t.Fatalf("expected unavailable retry without generation 1 job, got %v", err)
	}
	failure := errors.New("database unavailable")
	if err := Setup(context.Background(), Dependencies{DB: db, Within: func(context.Context, identity.ID, func(*gorm.DB) error) error { return failure }}, cmd); !errors.Is(err, failure) {
		t.Fatalf("expected dependency failure, got %v", err)
	}
	if err := Setup(context.Background(), Dependencies{}, Command{}); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
}

func TestSetupReturnsExpiredWhenAudioDeletionExpired(t *testing.T) {
	db := retryDB(t)
	tenant, note := identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,inspection_id,status,version,audio_deletion_status,duration_ms,updated_at) VALUES(?,?,?,?,?,?,?,?)`, tenant, note, identity.NewID(), "FAILED", 1, "EXPIRED", 1000, time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	err := Setup(context.Background(), Dependencies{DB: db, Within: retryWithin(db)}, Command{TenantID: tenant, NoteID: note, ExpectedVersion: 1, ClientMutationID: "expired-audio"})
	if !apperror.Is(err, apperror.Expired) {
		t.Fatalf("expected EXPIRED for expired audio, got %v", err)
	}
}

func TestSetupMeasuresRetryWindowFromUploadCompletion(t *testing.T) {
	db := retryDB(t)
	tenant, note := identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	completedAt := now.Add(-24*time.Hour - time.Second)
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,inspection_id,status,version,audio_deletion_status,duration_ms,updated_at) VALUES(?,?,?,?,?,?,?,?)`, tenant, note, identity.NewID(), "FAILED", 1, "PENDING", 1000, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO transcription.jobs(tenant_id,id,note_id,generation,status,created_at) VALUES(?,?,?,?,?,?)`, tenant, identity.NewID(), note, 1, "FAILED", completedAt).Error; err != nil {
		t.Fatal(err)
	}
	err := Setup(context.Background(), Dependencies{DB: db, Within: retryWithin(db)}, Command{TenantID: tenant, NoteID: note, ExpectedVersion: 1, ClientMutationID: "expired-window"})
	if !apperror.Is(err, apperror.RetryUnavailable) {
		t.Fatalf("expected RETRY_UNAVAILABLE after 24 hours from upload completion, got %v", err)
	}
}

func TestSetupRejectsUnavailableStateBeforeCheckingRetryWindow(t *testing.T) {
	db := retryDB(t)
	tenant, note := identity.NewID(), identity.NewID()
	old := time.Now().UTC().Add(-48 * time.Hour)
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,status,version,audio_deletion_status,updated_at) VALUES(?,?,?,?,?,?)`, tenant, note, "COMPLETED", 1, "PENDING", time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO transcription.jobs(tenant_id,id,note_id,generation,status,created_at) VALUES(?,?,?,?,?,?)`, tenant, identity.NewID(), note, 1, "COMPLETED", old).Error; err != nil {
		t.Fatal(err)
	}
	err := Setup(context.Background(), Dependencies{DB: db, Within: retryWithin(db)}, Command{TenantID: tenant, NoteID: note, ExpectedVersion: 1, ClientMutationID: "completed-old"})
	if !apperror.Is(err, apperror.RetryUnavailable) || err.Error() != "O áudio original não está disponível para nova tentativa." {
		t.Fatalf("expected status-specific unavailable error before expiry check, got %v", err)
	}
}

func retryDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"transcription", "messaging", "capture"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, ddl := range []string{`CREATE TABLE transcription.audio_notes(tenant_id text,id text,inspection_id text,duration_ms integer,status text,version integer,audio_deletion_status text,updated_at datetime,responsibility_id text,created_at datetime DEFAULT CURRENT_TIMESTAMP,safe_failure_code text,no_speech boolean)`, `CREATE TABLE transcription.jobs(tenant_id text,id text,note_id text,generation integer,profile_key text,profile_version integer,profile_digest text,status text,attempts integer,created_at datetime,updated_at datetime)`, `CREATE TABLE transcription.tenant_policies(tenant_id text,tenant_note_limit integer,tenant_audio_limit_ms integer,inspection_note_limit integer,inspection_audio_limit_ms integer)`, `CREATE TABLE messaging.outbox(id text,tenant_id text,type text,schema_version integer,payload blob,correlation_id text,status text,attempts integer,next_attempt_at datetime,created_at datetime)`, `CREATE TABLE transcription.mutation_receipts(tenant_id text,id text,note_id text,client_mutation_id text,operation text,payload_digest text,result_version integer,created_at datetime)`, `CREATE TABLE capture.capture_drafts(id text,tenant_id text,responsibility_id text,status text)`} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}
func retryWithin(db *gorm.DB) func(context.Context, identity.ID, func(*gorm.DB) error) error {
	return func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
}

func TestSetupReturnsExpiredAfterExpiredAudioWasDeleted(t *testing.T) {
	db := retryDB(t)
	tenant, note := identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,inspection_id,status,version,audio_deletion_status,duration_ms,updated_at) VALUES(?,?,?,?,?,?,?,?)`, tenant, note, identity.NewID(), "EXPIRED", 2, "DELETED", 1000, time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	err := Setup(context.Background(), Dependencies{DB: db, Within: retryWithin(db)}, Command{TenantID: tenant, NoteID: note, ExpectedVersion: 2, ClientMutationID: "expired-deleted-audio"})
	if !apperror.Is(err, apperror.Expired) {
		t.Fatalf("expected EXPIRED for an expired note, got %v", err)
	}
}
