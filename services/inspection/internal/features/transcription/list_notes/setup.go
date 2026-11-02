package list_notes

import (
	"context"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/transcription/get_note"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// Dependencies are the tenant-scoped persistence boundary for listing notes.
type Dependencies struct {
	DB     *gorm.DB
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
}

// Command scopes a stable page of notes to one inspection and optional responsibility/room.
type Command struct {
	TenantID, InspectionID, ResponsibilityID identity.ID
	RequirementKey                           string
	Offset, Limit                            int
}

// Result returns ordered note identifiers and whether another page exists.
type Result struct {
	IDs         []identity.ID
	Notes       []get_note.Note
	HasNextPage bool
}

// Setup returns a page of authorized notes for the requested scope.
func Setup(ctx context.Context, deps Dependencies, cmd Command) (Result, error) {
	if deps.DB == nil || cmd.TenantID == (identity.ID{}) || cmd.InspectionID == (identity.ID{}) || cmd.Offset < 0 || cmd.Limit < 1 || cmd.Limit > 100 {
		return Result{}, apperror.New(apperror.PaginationInvalid, "first", "O limite ou o cursor da lista é inválido.")
	}
	within := deps.Within
	if within == nil {
		within = (tenanttx.Runner{DB: deps.DB}).Within
	}
	var ids []identity.ID
	var notes []get_note.Note
	var hasNext bool
	err := within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		q := tx.Table("transcription.audio_notes").Where("tenant_id=? AND inspection_id=?", cmd.TenantID, cmd.InspectionID)
		if cmd.ResponsibilityID != (identity.ID{}) {
			q = q.Where("responsibility_id=?", cmd.ResponsibilityID)
		}
		if cmd.RequirementKey != "" {
			q = q.Where("requirement_key=?", cmd.RequirementKey)
		}
		if err := q.Order("created_at ASC,id ASC").Offset(cmd.Offset).Limit(cmd.Limit+1).Pluck("id", &ids).Error; err != nil {
			return err
		}
		hasNext = len(ids) > cmd.Limit
		if hasNext {
			ids = ids[:cmd.Limit]
		}
		if len(ids) == 0 {
			return nil
		}
		var rows []struct {
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
		fresh := tx.Session(&gorm.Session{NewDB: true})
		if err := fresh.Raw("SELECT id,inspection_id,responsibility_id,status,version,requirement_key,target_kind,anchor_status,audio_deletion_status,photo_media_id,video_attempt_id,source_media_id,start_ms,end_ms,duration_ms,highlighted,highlight_source,current_revision_id,text_confirmed,no_speech,safe_failure_code,created_at,updated_at FROM transcription.audio_notes WHERE tenant_id=? AND id IN ?", cmd.TenantID, ids).Scan(&rows).Error; err != nil {
			return err
		}
		// Same staging filter as get_note: hide only staging of non-completed jobs.
		var revisions []database.TranscriptRevision
		if err := tx.Session(&gorm.Session{NewDB: true}).Raw(`SELECT r.* FROM transcription.transcript_revisions r JOIN transcription.audio_notes n ON n.tenant_id=r.tenant_id AND n.id=r.note_id WHERE r.tenant_id=? AND r.note_id IN ? AND `+get_note.VisibleRevisionPredicate+` ORDER BY r.note_id ASC,r.revision_number ASC`, cmd.TenantID, ids).Scan(&revisions).Error; err != nil {
			return err
		}
		revisionsByNote := make(map[identity.ID][]database.TranscriptRevision, len(ids))
		for _, revision := range revisions {
			revisionsByNote[revision.NoteID] = append(revisionsByNote[revision.NoteID], revision)
		}
		rowsByID := make(map[identity.ID]get_note.Note, len(rows))
		for _, row := range rows {
			var current *identity.ID
			if row.CurrentRevisionID != nil {
				parsed, err := identity.ParseID(*row.CurrentRevisionID)
				if err != nil {
					return err
				}
				current = &parsed
			}
			rowsByID[row.ID] = get_note.Note{ID: row.ID, InspectionID: row.InspectionID, ResponsibilityID: row.ResponsibilityID, Version: row.Version, DurationMS: row.DurationMS, RequirementKey: row.RequirementKey, TargetKind: row.TargetKind, Status: row.Status, AnchorStatus: row.AnchorStatus, AudioDeletionStatus: row.AudioDeletionStatus, PhotoMediaID: row.PhotoMediaID, VideoAttemptID: row.VideoAttemptID, SourceMediaID: row.SourceMediaID, CurrentRevisionID: current, StartMS: row.StartMS, EndMS: row.EndMS, Highlighted: row.Highlighted, TextConfirmed: row.TextConfirmed, NoSpeech: row.NoSpeech, HighlightSource: row.HighlightSource, FailureCode: row.SafeFailureCode, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Revisions: revisionsByNote[row.ID]}
		}
		for _, id := range ids {
			note, ok := rowsByID[id]
			if !ok {
				continue
			}
			note.Revisions = revisionsByNote[id]
			notes = append(notes, note)
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return Result{IDs: ids, Notes: notes, HasNextPage: hasNext}, nil
}
