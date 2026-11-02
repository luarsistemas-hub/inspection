package create_note_upload

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type uploadClient struct{}

func (uploadClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "upload", nil
}
func (uploadClient) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "https://private.invalid/part", nil
}
func (uploadClient) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (uploadClient) AbortMultipart(context.Context, string, string, string) error { return nil }
func (uploadClient) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{}, nil
}
func (uploadClient) Get(context.Context, string, string) (io.ReadCloser, error) { return nil, nil }
func (uploadClient) Put(context.Context, string, string, io.Reader, int64, string) error {
	return nil
}

func TestSetupAdmitsScopedNoteAndReservesUploadUT003(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"transcription", "inspections", "templates", "capture", "invitations"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	ddl := []string{
		`CREATE TABLE transcription.tenant_policies (tenant_id text,enabled boolean,tenant_note_limit integer,tenant_audio_limit_ms integer,inspection_note_limit integer,inspection_audio_limit_ms integer,version integer)`,
		`CREATE TABLE inspections.responsibilities (id text,tenant_id text,inspection_id text,status text)`,
		`CREATE TABLE inspections.inspections (id text,tenant_id text,template_version_id text)`,
		`CREATE TABLE templates.template_versions (id text,tenant_id text,schema_version integer,definition_json blob)`,
		`CREATE TABLE capture.capture_drafts (id text,tenant_id text,responsibility_id text,status text,media_kind text,kind text)`,
		`CREATE TABLE capture.video_attempts (id text,tenant_id text,responsibility_id text,max_duration_seconds integer,status text)`,
		`CREATE TABLE invitations.processing_acceptances (id text,tenant_id text,responsibility_id text,disclosure_version text,photo_processing boolean,video_processing boolean,voice_processing boolean,voice_consent_version text,ai_analysis boolean,accepted_at datetime)`,
		`CREATE TABLE transcription.audio_notes (tenant_id text,id text,inspection_id text,responsibility_id text,client_note_id text,requirement_key text,consent_version text,disclosure_version text,target_kind text,photo_media_id text,video_attempt_id text,source_media_id text,start_ms integer,end_ms integer,time_basis text,duration_ms integer,content_sha256 text,content_type text,size_bytes integer,status text,anchor_status text,version integer,audio_deletion_status text,upload_key text,upload_id text,created_at datetime,updated_at datetime,idempotency_payload_digest text,UNIQUE(tenant_id,inspection_id,responsibility_id,client_note_id))`,
		`CREATE TABLE transcription.jobs (tenant_id text,note_id text,id text)`,
	}
	for _, sql := range ddl {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenant, responsibilityID, inspectionID, templateVersionID, attemptID, noteID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	seed := []struct {
		query string
		args  []any
	}{
		{"INSERT INTO transcription.tenant_policies VALUES (?,?,?,?,?,?,?)", []any{tenant, true, 100, 300000, 20, 30000, 1}},
		{"INSERT INTO inspections.responsibilities VALUES (?,?,?,?)", []any{responsibilityID, tenant, inspectionID, "PENDING"}},
		{"INSERT INTO inspections.inspections VALUES (?,?,?)", []any{inspectionID, tenant, templateVersionID}},
		{"INSERT INTO templates.template_versions VALUES (?,?,?,?)", []any{templateVersionID, tenant, 2, []byte(`{"captureLayout":"ENVIRONMENTS"}`)}},
		{"INSERT INTO capture.capture_drafts VALUES (?,?,?,?,?,?)", []any{identity.NewID(), tenant, responsibilityID, "OPEN", "VIDEO", "ENVIRONMENT"}},
		{"INSERT INTO capture.video_attempts VALUES (?,?,?,?,?)", []any{attemptID, tenant, responsibilityID, 30, "ACTIVE"}},
		{"INSERT INTO invitations.processing_acceptances VALUES (?,?,?,?,?,?,?,?,?,?)", []any{identity.NewID(), tenant, responsibilityID, "disclosure-v1", false, true, true, "voice-v1", true, time.Now()}},
	}
	for _, row := range seed {
		if err := db.Exec(row.query, row.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	deps := Dependencies{
		DB: db, Store: objectstore.Store{Bucket: "private", Client: uploadClient{}, Clock: func() time.Time { return now }},
		Now: func() time.Time { return now },
		Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
			return db.WithContext(ctx).Transaction(fn)
		},
	}
	start, end := int64(500), int64(1500)
	intent, err := Setup(context.Background(), deps, Command{
		TenantID: tenant, ResponsibilityID: responsibilityID, ClientNoteID: noteID.String(), RequirementKey: "room-1",
		VideoAttemptID: &attemptID, StartMS: &start, EndMS: &end, TimeBasis: "CAPTURE_CLOCK",
		ConsentVersion: "voice-v1", DisclosureVersion: "disclosure-v1", ContentType: "audio/webm", ContentSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 1024, DurationMS: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if intent.NoteID != noteID || intent.URL == "" || intent.UploadProof != "upload" {
		t.Fatalf("unexpected upload intent: %+v", intent)
	}
	if err := db.Exec("UPDATE templates.template_versions SET definition_json=? WHERE tenant_id=? AND id=?", []byte(`{"captureLayout":"LEGACY"}`), tenant, templateVersionID).Error; err != nil {
		t.Fatal(err)
	}
	ineligibleCommand := Command{TenantID: tenant, ResponsibilityID: responsibilityID, ClientNoteID: identity.NewID().String(), RequirementKey: "room-1", VideoAttemptID: &attemptID, StartMS: &start, EndMS: &end, TimeBasis: "CAPTURE_CLOCK", ConsentVersion: "voice-v1", DisclosureVersion: "disclosure-v1", ContentType: "audio/webm", ContentSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 1024, DurationMS: 1000}
	_, err = Setup(context.Background(), deps, ineligibleCommand)
	if !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("legacy capture layout should be rejected with INVALID_STATE, got %v", err)
	}
	code, field, message := apperror.Public(err)
	if code != apperror.InvalidState || field != "inspection" || message != "Esta vistoria não é elegível para anotações de voz." {
		t.Fatalf("legacy capture layout should expose the stable Portuguese error, got code=%s field=%q message=%q", code, field, message)
	}
	if err := db.Exec("UPDATE templates.template_versions SET definition_json=? WHERE tenant_id=? AND id=?", []byte(`{"captureLayout":"ENVIRONMENTS"}`), tenant, templateVersionID).Error; err != nil {
		t.Fatal(err)
	}
	retry, err := Setup(context.Background(), deps, Command{
		TenantID: tenant, ResponsibilityID: responsibilityID, ClientNoteID: noteID.String(), RequirementKey: "room-1",
		VideoAttemptID: &attemptID, StartMS: &start, EndMS: &end, TimeBasis: "CAPTURE_CLOCK",
		ConsentVersion: "voice-v1", DisclosureVersion: "disclosure-v1", ContentType: "audio/webm", ContentSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 1024, DurationMS: 1000,
	})
	if err != nil || retry.NoteID != intent.NoteID || retry.UploadProof != intent.UploadProof {
		t.Fatalf("identical retry should resume the original upload: intent=%+v retry=%+v err=%v", intent, retry, err)
	}
	var count int
	if err := db.Raw("SELECT count(*) FROM transcription.audio_notes WHERE tenant_id=? AND responsibility_id=?", tenant, responsibilityID).Scan(&count).Error; err != nil || count != 1 {
		t.Fatalf("admission should reserve one note: count=%d err=%v", count, err)
	}
	conflicting := Command{
		TenantID: tenant, ResponsibilityID: responsibilityID, ClientNoteID: noteID.String(), RequirementKey: "room-1",
		VideoAttemptID: &attemptID, StartMS: &start, EndMS: &end, TimeBasis: "CAPTURE_CLOCK",
		ConsentVersion: "voice-v1", DisclosureVersion: "disclosure-v1", ContentType: "audio/webm", ContentSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", SizeBytes: 1024, DurationMS: 1000,
	}
	if _, err := Setup(context.Background(), deps, conflicting); err == nil {
		t.Fatal("reusing a note identity with different content must fail")
	}
	for _, status := range []string{"ACTIVE", "CANCELED", "REJECTED"} {
		if err := db.Model(&database.VideoAttempt{}).Where("tenant_id=? AND id=?", tenant, attemptID).Update("status", status).Error; err != nil {
			t.Fatal(err)
		}
		invalidStateCommand := Command{TenantID: tenant, ResponsibilityID: responsibilityID, ClientNoteID: identity.NewID().String(), RequirementKey: "room-1", VideoAttemptID: &attemptID, StartMS: &start, EndMS: &end, TimeBasis: "CAPTURE_CLOCK", ConsentVersion: "voice-v1", DisclosureVersion: "disclosure-v1", ContentType: "audio/webm", ContentSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 1024, DurationMS: 1000}
		_, err := Setup(context.Background(), deps, invalidStateCommand)
		if status == "ACTIVE" && err != nil {
			t.Fatalf("active video attempt should accept a voice note, got %v", err)
		}
		if status != "ACTIVE" && !apperror.Is(err, apperror.InvalidState) {
			t.Fatalf("video attempt status %s: expected INVALID_STATE, got %v", status, err)
		}
	}
	if err := db.Model(&database.VideoAttempt{}).Where("tenant_id=? AND id=?", tenant, attemptID).Update("status", "ACTIVE").Error; err != nil {
		t.Fatal(err)
	}
	transient := errors.New("temporary database timeout")
	failReadTable := "capture_drafts"
	if err := db.Callback().Query().Before("gorm:query").Register("test:transient_capture_draft_read", func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.Table, failReadTable) {
			tx.AddError(transient)
		}
	}); err != nil {
		t.Fatal(err)
	}
	transientCommand := Command{TenantID: tenant, ResponsibilityID: responsibilityID, ClientNoteID: identity.NewID().String(), RequirementKey: "room-1", VideoAttemptID: &attemptID, StartMS: &start, EndMS: &end, TimeBasis: "CAPTURE_CLOCK", ConsentVersion: "voice-v1", DisclosureVersion: "disclosure-v1", ContentType: "audio/webm", ContentSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 1024, DurationMS: 1000}
	if _, err := Setup(context.Background(), deps, transientCommand); !errors.Is(err, transient) || apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("transient draft read failure must remain an internal error, got %v", err)
	}
	failReadTable = "responsibilities"
	transientCommand.ClientNoteID = identity.NewID().String()
	if _, err := Setup(context.Background(), deps, transientCommand); !errors.Is(err, transient) || apperror.Is(err, apperror.NotFound) {
		t.Fatalf("transient responsibility read failure must remain an internal error, got %v", err)
	}
}
