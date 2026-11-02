package complete_note_upload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type completionClient struct {
	data      []byte
	headErr   error
	headCalls *int
}

func (completionClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (completionClient) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "", nil
}
func (completionClient) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (completionClient) ListMultipartParts(context.Context, string, string, string) ([]objectstore.UploadedPart, error) {
	return []objectstore.UploadedPart{{Number: 1, ETag: "real-etag", SizeBytes: 8}}, nil
}
func (completionClient) AbortMultipart(context.Context, string, string, string) error { return nil }

func (c completionClient) Head(context.Context, string, string) (objectstore.Head, error) {
	if c.headCalls != nil {
		(*c.headCalls)++
	}
	if c.headErr != nil {
		return objectstore.Head{}, c.headErr
	}
	return objectstore.Head{Size: int64(len(c.data)), ContentType: "audio/webm"}, nil
}

func TestSetupRejectsClosedParticipantBeforeObjectStoreIO(t *testing.T) {
	db := completionDB(t)
	if err := db.Exec(`CREATE TABLE capture.capture_drafts(id text,tenant_id text,responsibility_id text,status text)`).Error; err != nil {
		t.Fatal(err)
	}
	tenant, noteID := identity.NewID(), identity.NewID()
	responsibilityID := identity.NewID()
	if err := db.Exec(`INSERT INTO capture.capture_drafts(id,tenant_id,responsibility_id,status) VALUES(?,?,?,'SUBMITTED')`, identity.NewID(), tenant, responsibilityID).Error; err != nil {
		t.Fatal(err)
	}
	data := []byte{0x1a, 0x45, 0xdf, 0xa3, 1, 2, 3, 4}
	digest := sha256.Sum256(data)
	if err := db.Exec("INSERT INTO transcription.audio_notes(tenant_id,id,responsibility_id,status,content_type,content_sha256,size_bytes,duration_ms,upload_key,upload_id,version) VALUES(?,?,?,?,?,?,?,?,?,?,?)", tenant, noteID, responsibilityID, "UPLOADING", "audio/webm", hex.EncodeToString(digest[:]), len(data), 5000, "key", "proof", 1).Error; err != nil {
		t.Fatal(err)
	}
	headCalls := 0
	store := objectstore.Store{Bucket: "private", Client: completionClient{data: data, headCalls: &headCalls}, AudioProbe: func(context.Context, []byte) (time.Duration, error) { return 5 * time.Second, nil }}
	err := Setup(context.Background(), Dependencies{DB: db, Store: store, Within: testWithin(db)}, Command{TenantID: tenant, NoteID: noteID, UploadProof: "proof", IsParticipant: true})
	if !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("expected closed-capture INVALID_STATE, got %v", err)
	}
	if headCalls != 0 {
		t.Fatalf("closed capture performed object-store reads: %d", headCalls)
	}
}
func (c completionClient) Get(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(bytesReader(c.data)), nil
}
func (completionClient) Put(context.Context, string, string, io.Reader, int64, string) error {
	return nil
}

func TestSetupRejectsDigestMismatchWithoutCreatingJobUT004(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"transcription", "messaging"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`CREATE TABLE transcription.audio_notes(tenant_id text,id text,responsibility_id text,status text,content_type text,content_sha256 text,size_bytes integer,duration_ms integer,upload_key text,upload_id text,target_kind text,photo_media_id text,video_attempt_id text,anchor_status text,version integer,updated_at datetime)`,
		`CREATE TABLE transcription.jobs(id text,tenant_id text,note_id text,generation integer,profile_key text,profile_version integer,profile_digest text,status text,attempts integer,created_at datetime,updated_at datetime)`,
		`CREATE TABLE messaging.outbox(id text,tenant_id text,type text,schema_version integer,payload blob,correlation_id text,status text,attempts integer,next_attempt_at datetime,created_at datetime)`,
		`CREATE TABLE media.media_objects(tenant_id text,id text,status text)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenant, noteID := identity.NewID(), identity.NewID()
	data := []byte{0x1a, 0x45, 0xdf, 0xa3, 1, 2, 3, 4}
	wrong := sha256.Sum256([]byte("different"))
	if err := db.Exec("INSERT INTO transcription.audio_notes(tenant_id,id,responsibility_id,status,content_type,content_sha256,size_bytes,duration_ms,upload_key,upload_id,version) VALUES(?,?,?,?,?,?,?,?,?,?,?)", tenant, noteID, identity.NewID(), "UPLOADING", "audio/webm", hex.EncodeToString(wrong[:]), len(data), 5000, "key", "proof", 1).Error; err != nil {
		t.Fatal(err)
	}
	store := objectstore.Store{Bucket: "private", Client: completionClient{data: data}, AudioProbe: func(context.Context, []byte) (time.Duration, error) { return 5 * time.Second, nil }}
	err = Setup(context.Background(), Dependencies{DB: db, Store: store, Within: testWithin(db)}, Command{TenantID: tenant, NoteID: noteID, UploadProof: "proof"})
	if !apperror.Is(err, apperror.InvalidAudio) {
		t.Fatalf("expected INVALID_AUDIO, got %v", err)
	}
	var jobs, outbox int64
	if err := db.Table("transcription.jobs").Count(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("messaging.outbox").Count(&outbox).Error; err != nil {
		t.Fatal(err)
	}
	if jobs != 0 || outbox != 0 {
		t.Fatalf("mismatch created durable work: jobs=%d outbox=%d", jobs, outbox)
	}
}

func TestSetupVerifiesBytesThenQueuesNoteAndIsIdempotent(t *testing.T) {
	db := completionDB(t)
	tenant, noteID, mediaID := identity.NewID(), identity.NewID(), identity.NewID()
	data := []byte{0x1a, 0x45, 0xdf, 0xa3, 1, 2, 3, 4}
	digest := sha256.Sum256(data)
	if err := db.Exec("INSERT INTO media.media_objects(tenant_id,id,status) VALUES(?,?,?)", tenant, mediaID, "READY").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO transcription.audio_notes(tenant_id,id,responsibility_id,status,content_type,content_sha256,size_bytes,duration_ms,upload_key,upload_id,target_kind,photo_media_id,version) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)", tenant, noteID, identity.NewID(), "UPLOADING", "audio/webm", hex.EncodeToString(digest[:]), len(data), 5000, "key", "proof", "PHOTO", mediaID, 1).Error; err != nil {
		t.Fatal(err)
	}
	store := objectstore.Store{Bucket: "private", Client: completionClient{data: data}, AudioProbe: func(context.Context, []byte) (time.Duration, error) { return 5 * time.Second, nil }}
	deps := Dependencies{DB: db, Store: store, Within: testWithin(db)}
	for i := 0; i < 2; i++ {
		if err := Setup(context.Background(), deps, Command{TenantID: tenant, NoteID: noteID, UploadProof: "proof"}); err != nil {
			t.Fatalf("completion %d: %v", i+1, err)
		}
	}
	var jobs, outbox int64
	if err := db.Table("transcription.jobs").Count(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("messaging.outbox").Count(&outbox).Error; err != nil {
		t.Fatal(err)
	}
	var status, anchor string
	if err := db.Raw("SELECT status,anchor_status FROM transcription.audio_notes WHERE tenant_id=? AND id=?", tenant, noteID).Row().Scan(&status, &anchor); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 || outbox != 1 || status != "QUEUED" || anchor != "VALID" {
		t.Fatalf("completion state jobs=%d outbox=%d status=%s anchor=%s", jobs, outbox, status, anchor)
	}
	var profileKey string
	if err := db.Raw(`SELECT profile_key FROM transcription.jobs WHERE tenant_id=? AND note_id=? AND generation=1`, tenant, noteID).Row().Scan(&profileKey); err != nil {
		t.Fatal(err)
	}
	if profileKey != llm.DefaultTranscriptionProfile {
		t.Fatalf("completion profile=%q, want default %q", profileKey, llm.DefaultTranscriptionProfile)
	}
}

func TestSetupObjectStoreFailureCreatesNoJob(t *testing.T) {
	db := completionDB(t)
	tenant, noteID := identity.NewID(), identity.NewID()
	data := []byte{0x1a, 0x45, 0xdf, 0xa3, 1, 2, 3, 4}
	digest := sha256.Sum256(data)
	if err := db.Exec("INSERT INTO transcription.audio_notes(tenant_id,id,responsibility_id,status,content_type,content_sha256,size_bytes,duration_ms,upload_key,upload_id,target_kind,video_attempt_id,version) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)", tenant, noteID, identity.NewID(), "UPLOADING", "audio/webm", hex.EncodeToString(digest[:]), len(data), 5000, "key", "proof", "VIDEO_INTERVAL", identity.NewID(), 1).Error; err != nil {
		t.Fatal(err)
	}
	store := objectstore.Store{Bucket: "private", Client: completionClient{data: data, headErr: errors.New("store unavailable")}}
	err := Setup(context.Background(), Dependencies{DB: db, Store: store, Within: testWithin(db)}, Command{TenantID: tenant, NoteID: noteID, UploadProof: "proof"})
	if !apperror.Is(err, apperror.Internal) {
		t.Fatalf("expected generic internal error, got %v", err)
	}
	var jobs int64
	if err := db.Table("transcription.jobs").Count(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatalf("object-store failure created %d jobs", jobs)
	}
	var status string
	if err := db.Raw("SELECT status FROM transcription.audio_notes WHERE tenant_id=? AND id=?", tenant, noteID).Row().Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "UPLOADING" {
		t.Fatalf("transient object-store failure changed note to %s", status)
	}
}

func TestSetupTransientCompletionTransactionErrorDoesNotExpireNote(t *testing.T) {
	db := completionDB(t)
	tenant, noteID := identity.NewID(), identity.NewID()
	data := []byte{0x1a, 0x45, 0xdf, 0xa3, 1, 2, 3, 4}
	digest := sha256.Sum256(data)
	if err := db.Exec("INSERT INTO transcription.audio_notes(tenant_id,id,responsibility_id,status,content_type,content_sha256,size_bytes,duration_ms,upload_key,upload_id,version) VALUES(?,?,?,?,?,?,?,?,?,?,?)", tenant, noteID, identity.NewID(), "UPLOADING", "audio/webm", hex.EncodeToString(digest[:]), len(data), 5000, "key", "proof", 1).Error; err != nil {
		t.Fatal(err)
	}
	transient := errors.New("temporary database timeout")
	withinCalls := 0
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		withinCalls++
		if withinCalls == 2 {
			return transient
		}
		return db.WithContext(ctx).Transaction(fn)
	}
	store := objectstore.Store{Bucket: "private", Client: completionClient{data: data}, AudioProbe: func(context.Context, []byte) (time.Duration, error) { return 5 * time.Second, nil }}
	err := Setup(context.Background(), Dependencies{DB: db, Store: store, Within: within}, Command{TenantID: tenant, NoteID: noteID, UploadProof: "proof"})
	if !errors.Is(err, transient) || apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("transient persistence failure was reclassified: %v", err)
	}
	var status string
	if err := db.Raw("SELECT status FROM transcription.audio_notes WHERE tenant_id=? AND id=?", tenant, noteID).Row().Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "UPLOADING" {
		t.Fatalf("transient error changed note status to %s", status)
	}
}

func TestSetupDerivesVideoAnchorFromAttemptState(t *testing.T) {
	for _, tc := range []struct{ attempt, want string }{{"RECEIVED", "PENDING"}, {"REJECTED", "INVALID"}, {"EXPIRED", "UNAVAILABLE"}} {
		t.Run(tc.attempt, func(t *testing.T) {
			db := completionDB(t)
			tenant, noteID, attemptID := identity.NewID(), identity.NewID(), identity.NewID()
			data := []byte{0x1a, 0x45, 0xdf, 0xa3, 1, 2, 3, 4}
			digest := sha256.Sum256(data)
			if err := db.Exec("INSERT INTO capture.video_attempts(tenant_id,id,status) VALUES(?,?,?)", tenant, attemptID, tc.attempt).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("INSERT INTO transcription.audio_notes(tenant_id,id,responsibility_id,status,content_type,content_sha256,size_bytes,duration_ms,upload_key,upload_id,target_kind,video_attempt_id,version) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)", tenant, noteID, identity.NewID(), "UPLOADING", "audio/webm", hex.EncodeToString(digest[:]), len(data), 5000, "key", "proof", "VIDEO_INTERVAL", attemptID, 1).Error; err != nil {
				t.Fatal(err)
			}
			store := objectstore.Store{Bucket: "private", Client: completionClient{data: data}, AudioProbe: func(context.Context, []byte) (time.Duration, error) { return 5 * time.Second, nil }}
			if err := Setup(context.Background(), Dependencies{DB: db, Store: store, Within: testWithin(db)}, Command{TenantID: tenant, NoteID: noteID, UploadProof: "proof"}); err != nil {
				t.Fatal(err)
			}
			var anchor string
			if err := db.Raw("SELECT anchor_status FROM transcription.audio_notes WHERE tenant_id=? AND id=?", tenant, noteID).Row().Scan(&anchor); err != nil {
				t.Fatal(err)
			}
			if anchor != tc.want {
				t.Fatalf("anchor=%s, want %s", anchor, tc.want)
			}
		})
	}
}

func completionDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"transcription", "messaging", "media", "capture"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, ddl := range []string{
		`CREATE TABLE transcription.audio_notes(tenant_id text,id text,responsibility_id text,status text,content_type text,content_sha256 text,size_bytes integer,duration_ms integer,upload_key text,upload_id text,target_kind text,photo_media_id text,video_attempt_id text,anchor_status text,version integer,updated_at datetime)`,
		`CREATE TABLE transcription.jobs(id text,tenant_id text,note_id text,generation integer,profile_key text,profile_version integer,profile_digest text,status text,attempts integer,created_at datetime,updated_at datetime,UNIQUE(tenant_id,note_id,generation))`,
		`CREATE TABLE messaging.outbox(id text,tenant_id text,type text,schema_version integer,payload blob,correlation_id text,status text,attempts integer,next_attempt_at datetime,created_at datetime)`,
		`CREATE TABLE media.media_objects(tenant_id text,id text,status text)`,
		`CREATE TABLE capture.video_attempts(tenant_id text,id text,status text)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func testWithin(db *gorm.DB) func(context.Context, identity.ID, func(*gorm.DB) error) error {
	return func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
}

func TestSetupValidatesDependencies(t *testing.T) {
	err := Setup(context.Background(), Dependencies{}, Command{})
	if !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}
}

type byteReader struct{ data []byte }

func (r *byteReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}
func bytesReader(data []byte) io.Reader { return &byteReader{data: append([]byte(nil), data...)} }
