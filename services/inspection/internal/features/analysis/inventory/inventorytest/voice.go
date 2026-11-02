package inventorytest

import (
	"fmt"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
)

// audioNotesDDL mirrors the columns of transcription.audio_notes that the
// catalog reads and writes.
const audioNotesDDL = `CREATE TABLE transcription.audio_notes (
 tenant_id text NOT NULL, id text NOT NULL, inspection_id text NOT NULL, responsibility_id text NOT NULL,
 client_note_id text, requirement_key text NOT NULL, target_kind text NOT NULL,
 photo_media_id text, video_attempt_id text, source_media_id text, source_generation integer,
 start_ms integer, end_ms integer, time_basis text, duration_ms integer,
 status text NOT NULL, anchor_status text NOT NULL, no_speech boolean NOT NULL DEFAULT false,
 current_revision_id text, version integer NOT NULL DEFAULT 1, created_at datetime, updated_at datetime,
 PRIMARY KEY (tenant_id, id))`

// NoteSpec describes one audio note and its current transcript revision.
type NoteSpec struct {
	// Key is the requirement key of the room.
	Key string
	// Status defaults to COMPLETED and Anchor to VALID.
	Status, Anchor string
	// TargetKind defaults to PHOTO.
	TargetKind                       string
	PhotoMedia, VideoAttempt, Source *identity.ID
	SourceGeneration                 *int
	StartMS, EndMS                   *int64
	NoSpeech                         bool
	Inspection, Responsibility       identity.ID
	// Text is the first revision; empty means the note has no revision yet.
	Text      string
	CreatedAt time.Time
}

// Note inserts an audio note and, when Text is set, its first revision.
func (s *Seed) Note(t testing.TB, spec NoteSpec) (noteID, revisionID identity.ID) {
	t.Helper()
	if spec.Status == "" {
		spec.Status = "COMPLETED"
	}
	if spec.Anchor == "" {
		spec.Anchor = "VALID"
	}
	if spec.TargetKind == "" {
		spec.TargetKind = "PHOTO"
	}
	if spec.Inspection == (identity.ID{}) {
		spec.Inspection = s.Inspection
	}
	if spec.Responsibility == (identity.ID{}) {
		spec.Responsibility = s.Responsibility
	}
	if spec.CreatedAt.IsZero() {
		spec.CreatedAt = s.Now
	}
	noteID = identity.NewID()
	if spec.PhotoMedia == nil && spec.TargetKind == "PHOTO" {
		photo := s.Photo(t, spec.Key)
		spec.PhotoMedia, spec.Source = &photo, &photo
	}
	if err := s.DB.Exec(`INSERT INTO transcription.audio_notes (tenant_id,id,inspection_id,responsibility_id,client_note_id,requirement_key,target_kind,photo_media_id,video_attempt_id,source_media_id,source_generation,start_ms,end_ms,time_basis,duration_ms,status,anchor_status,no_speech,version,created_at,updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,?,?)`, s.Tenant, noteID, spec.Inspection, spec.Responsibility, noteID, spec.Key, spec.TargetKind, spec.PhotoMedia, spec.VideoAttempt, spec.Source, spec.SourceGeneration, spec.StartMS, spec.EndMS, nil, 3000, spec.Status, spec.Anchor, spec.NoSpeech, spec.CreatedAt, spec.CreatedAt).Error; err != nil {
		t.Fatalf("seed audio note: %v", err)
	}
	if spec.Text != "" {
		revisionID = s.Revise(t, noteID, spec.Text)
	}
	return noteID, revisionID
}

// Revise appends an immutable revision to a note and makes it the current one.
func (s *Seed) Revise(t testing.TB, noteID identity.ID, text string) identity.ID {
	t.Helper()
	var number int64
	if err := s.DB.Model(&database.TranscriptRevision{}).Where("tenant_id=? AND note_id=?", s.Tenant, noteID).Count(&number).Error; err != nil {
		t.Fatal(err)
	}
	callID := identity.NewID()
	kind := "HUMAN_EDIT"
	revision := database.TranscriptRevision{ID: identity.NewID(), TenantID: s.Tenant, NoteID: noteID, RevisionNumber: int(number) + 1, Text: text, Kind: kind, CreatedAt: s.Now}
	if number == 0 {
		revision.Kind, revision.CallID = "MODEL", &callID
	}
	if err := s.DB.Create(&revision).Error; err != nil {
		t.Fatalf("seed transcript revision: %v", err)
	}
	if err := s.DB.Exec("UPDATE transcription.audio_notes SET current_revision_id=? WHERE tenant_id=? AND id=?", revision.ID, s.Tenant, noteID).Error; err != nil {
		t.Fatal(err)
	}
	return revision.ID
}

// SetNote updates the lifecycle columns of a note.
func (s *Seed) SetNote(t testing.TB, noteID identity.ID, column string, value any) {
	t.Helper()
	if err := s.DB.Exec(fmt.Sprintf("UPDATE transcription.audio_notes SET %s=? WHERE tenant_id=? AND id=?", column), value, s.Tenant, noteID).Error; err != nil {
		t.Fatal(err)
	}
}
