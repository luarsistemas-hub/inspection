package get_note

import (
	"context"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// VisibleRevisionPredicate is the SQL condition, over revision alias r and
// note alias n, that hides only worker staging. A MODEL revision is visible
// when it is the note's current revision or when the job that produced it
// (linked through its usage.llm_calls row) is COMPLETED; it stays hidden only
// while it is the staged result of a job that has not completed. Human
// revisions are always visible. The rule ignores the note status, so a
// discarded note keeps its revisions, and a model result completed after a
// declaration or edit is shown without replacing the current human revision.
const VisibleRevisionPredicate = `(r.kind <> 'MODEL' OR r.id = n.current_revision_id OR EXISTS (SELECT 1 FROM usage.llm_calls c JOIN transcription.jobs j ON j.tenant_id=c.tenant_id AND j.id=c.job_id AND j.generation=c.replay_generation WHERE c.tenant_id=r.tenant_id AND c.call_id=r.call_id AND j.note_id=r.note_id AND j.status='COMPLETED'))`

// Dependencies are the tenant-scoped persistence boundary for loading a note.
type Dependencies struct {
	DB     *gorm.DB
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
}

// Command identifies the note to load.
type Command struct {
	TenantID, NoteID      identity.ID
	ScopeOnly             bool
	IncludeResponsibility bool
}

// Note contains the durable note state and immutable revisions needed by read surfaces.
type Note struct {
	ID, InspectionID, ResponsibilityID   identity.ID
	Version, DurationMS                  int
	RequirementKey, TargetKind           string
	Status, AnchorStatus                 string
	AudioDeletionStatus                  string
	PhotoMediaID, VideoAttemptID         *identity.ID
	SourceMediaID, CurrentRevisionID     *identity.ID
	StartMS, EndMS                       *int64
	Highlighted, TextConfirmed, NoSpeech bool
	HighlightSource, FailureCode         *string
	CreatedAt, UpdatedAt                 time.Time
	Revisions                            []database.TranscriptRevision
}

// Setup loads a note and its revisions under the supplied tenant transaction.
func Setup(ctx context.Context, deps Dependencies, cmd Command) (Note, error) {
	if deps.DB == nil || cmd.TenantID == (identity.ID{}) || cmd.NoteID == (identity.ID{}) {
		return Note{}, apperror.New(apperror.InvalidInput, "noteId", "A nota de áudio informada é inválida.")
	}
	within := deps.Within
	if within == nil {
		within = (tenanttx.Runner{DB: deps.DB}).Within
	}
	var out Note
	err := within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		if cmd.ScopeOnly {
			columns := "id,inspection_id"
			if cmd.IncludeResponsibility {
				columns += ",responsibility_id"
			}
			return tx.Raw("SELECT "+columns+" FROM transcription.audio_notes WHERE tenant_id=? AND id=?", cmd.TenantID, cmd.NoteID).Scan(&out).Error
		}
		var row struct {
			ID, InspectionID, ResponsibilityID                            identity.ID
			Status                                                        string
			Version                                                       int
			RequirementKey, TargetKind, AnchorStatus, AudioDeletionStatus string
			PhotoMediaID, VideoAttemptID, SourceMediaID                   *identity.ID
			StartMS, EndMS                                                *int64
			DurationMS                                                    int
			Highlighted                                                   bool
			HighlightSource, CurrentRevisionID, SafeFailureCode           *string
			TextConfirmed, NoSpeech                                       bool
			CreatedAt, UpdatedAt                                          time.Time
		}
		if err := tx.Raw("SELECT id,status,version,inspection_id,responsibility_id,requirement_key,target_kind,anchor_status,audio_deletion_status,photo_media_id,video_attempt_id,source_media_id,start_ms,end_ms,duration_ms,highlighted,highlight_source,current_revision_id,text_confirmed,no_speech,safe_failure_code,created_at,updated_at FROM transcription.audio_notes WHERE tenant_id=? AND id=?", cmd.TenantID, cmd.NoteID).Scan(&row).Error; err != nil {
			return err
		}
		if row.ID == (identity.ID{}) {
			return apperror.New(apperror.NotFound, "noteId", "A nota de áudio não foi encontrada.")
		}
		var revisions []database.TranscriptRevision
		if err := tx.Raw(`SELECT r.* FROM transcription.transcript_revisions r JOIN transcription.audio_notes n ON n.tenant_id=r.tenant_id AND n.id=r.note_id WHERE r.tenant_id=? AND r.note_id=? AND `+VisibleRevisionPredicate+` ORDER BY r.revision_number ASC`, cmd.TenantID, cmd.NoteID).Scan(&revisions).Error; err != nil {
			return err
		}
		var highlightSource, failureCode *string
		if row.HighlightSource != nil {
			v := *row.HighlightSource
			highlightSource = &v
		}
		if row.SafeFailureCode != nil {
			v := *row.SafeFailureCode
			failureCode = &v
		}
		var currentRevisionID *identity.ID
		if row.CurrentRevisionID != nil {
			parsed, err := identity.ParseID(*row.CurrentRevisionID)
			if err != nil {
				return err
			}
			currentRevisionID = &parsed
		}
		out = Note{ID: row.ID, InspectionID: row.InspectionID, ResponsibilityID: row.ResponsibilityID, Version: row.Version, DurationMS: row.DurationMS, RequirementKey: row.RequirementKey, TargetKind: row.TargetKind, Status: row.Status, AnchorStatus: row.AnchorStatus, AudioDeletionStatus: row.AudioDeletionStatus, PhotoMediaID: row.PhotoMediaID, VideoAttemptID: row.VideoAttemptID, SourceMediaID: row.SourceMediaID, CurrentRevisionID: currentRevisionID, StartMS: row.StartMS, EndMS: row.EndMS, Highlighted: row.Highlighted, TextConfirmed: row.TextConfirmed, NoSpeech: row.NoSpeech, HighlightSource: highlightSource, FailureCode: failureCode, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Revisions: revisions}
		return nil
	})
	return out, err
}
