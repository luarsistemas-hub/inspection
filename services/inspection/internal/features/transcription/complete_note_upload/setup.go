package complete_note_upload

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Dependencies are the tenant-scoped persistence and private audio store used by this operation.
type Dependencies struct {
	DB     *gorm.DB
	Store  objectstore.Store
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
}

// Command identifies one authorized note completion.
type Command struct {
	TenantID, NoteID identity.ID
	UploadProof      string
	// IsParticipant requires an open capture before audio bytes are verified.
	IsParticipant bool
}

type upload struct {
	ID                                 identity.ID
	ResponsibilityID                   identity.ID
	Status, ContentType, ContentSHA256 string
	SizeBytes, DurationMS              int64
	UploadKey, UploadID                *string
	TargetKind                         string
	PhotoMediaID, VideoAttemptID       *identity.ID
}

// Setup verifies stored bytes before opening the short transaction that queues durable work.
func Setup(ctx context.Context, deps Dependencies, cmd Command) error {
	if deps.DB == nil || deps.Store.Client == nil || cmd.TenantID == (identity.ID{}) || cmd.NoteID == (identity.ID{}) || cmd.UploadProof == "" {
		return apperror.New(apperror.InvalidInput, "uploadProof", "Os dados do envio estão incompletos.")
	}
	within := deps.Within
	if within == nil {
		within = (tenanttx.Runner{DB: deps.DB}).Within
	}
	var note upload
	if err := within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		return tx.Raw("SELECT id,responsibility_id,status,content_type,content_sha256,size_bytes,duration_ms,upload_key,upload_id,target_kind,photo_media_id,video_attempt_id FROM transcription.audio_notes WHERE tenant_id=? AND id=?", cmd.TenantID, cmd.NoteID).Scan(&note).Error
	}); err != nil {
		return err
	}
	if note.ID == (identity.ID{}) {
		return apperror.New(apperror.NotFound, "noteId", "A nota de áudio não foi encontrada.")
	}
	if note.Status != "UPLOADING" {
		if note.Status == "QUEUED" || note.Status == "PROCESSING" || note.Status == "RETRY_WAIT" || note.Status == "COMPLETED" || note.Status == "FAILED" {
			return nil
		}
		return apperror.New(apperror.InvalidState, "noteId", "Esta gravação já foi encerrada e não pode ser concluída.")
	}
	if note.UploadKey == nil || note.UploadID == nil || *note.UploadID != cmd.UploadProof {
		return apperror.New(apperror.InvalidAudio, "uploadProof", "Não foi possível validar o áudio enviado.")
	}
	if cmd.IsParticipant {
		open := false
		if err := within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
			var draft database.CaptureDraft
			err := tx.Where("tenant_id=? AND responsibility_id=? AND status='OPEN'", cmd.TenantID, note.ResponsibilityID).First(&draft).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			open = true
			return nil
		}); err != nil {
			return err
		}
		if !open {
			return apperror.New(apperror.InvalidState, "noteId", "A captura foi concluída e não pode mais receber esta gravação.")
		}
	}
	// Object-store verification may perform multipart completion and bounded media probing; keep it outside the row lock.
	if err := deps.Store.CompleteAudio(ctx, *note.UploadKey, *note.UploadID, cmd.UploadProof, note.SizeBytes, note.DurationMS, note.ContentType, note.ContentSHA256); err != nil {
		if errors.Is(err, objectstore.ErrInvalid) || errors.Is(err, objectstore.ErrChecksum) {
			return apperror.New(apperror.InvalidAudio, "uploadProof", "Não foi possível validar o áudio enviado.")
		}
		return apperror.Wrap(apperror.Internal, err)
	}
	expiredDuringVerification := false
	completeErr := within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		var current upload
		query := "SELECT id,responsibility_id,status,content_type,content_sha256,size_bytes,duration_ms,upload_key,upload_id,target_kind,photo_media_id,video_attempt_id FROM transcription.audio_notes WHERE tenant_id=? AND id=?"
		if tx.Dialector.Name() == "postgres" {
			query += " FOR UPDATE"
		}
		if err := tx.Raw(query, cmd.TenantID, cmd.NoteID).Scan(&current).Error; err != nil {
			return err
		}
		if current.ID == (identity.ID{}) {
			return apperror.New(apperror.NotFound, "noteId", "A nota de áudio não foi encontrada.")
		}
		if current.Status == "QUEUED" || current.Status == "PROCESSING" || current.Status == "RETRY_WAIT" || current.Status == "COMPLETED" || current.Status == "FAILED" {
			return nil
		}
		if current.Status == "CANCELED" {
			return scheduleCompletedObjectDeletion(tx, cmd, current, time.Now().UTC())
		}
		if current.Status == "EXPIRED" {
			if err := scheduleCompletedObjectDeletion(tx, cmd, current, time.Now().UTC()); err != nil {
				return err
			}
			expiredDuringVerification = true
			return nil
		}
		if current.Status != "UPLOADING" {
			return apperror.New(apperror.InvalidState, "noteId", "Esta gravação já foi encerrada e não pode ser concluída.")
		}
		if current.UploadKey == nil || current.UploadID == nil || *current.UploadID != cmd.UploadProof {
			return apperror.New(apperror.InvalidAudio, "uploadProof", "Não foi possível validar o áudio enviado.")
		}
		if tx.Dialector.Name() == "postgres" {
			var draft database.CaptureDraft
			draftQuery := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("tenant_id=? AND responsibility_id=? AND status='OPEN'", cmd.TenantID, current.ResponsibilityID)
			if err := draftQuery.First(&draft).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return apperror.New(apperror.InvalidState, "noteId", "A captura foi concluída e não pode mais receber esta gravação.")
				}
				return err
			}
		}
		anchorStatus, err := resolveAnchorStatus(tx, cmd.TenantID, current)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		update := tx.Exec("UPDATE transcription.audio_notes SET status='QUEUED',anchor_status=?,version=version+1,updated_at=? WHERE tenant_id=? AND id=? AND status='UPLOADING'", anchorStatus, now, cmd.TenantID, cmd.NoteID)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "noteId", "A gravação foi alterada. Atualize os dados e tente novamente.")
		}
		jobID := identity.NewID()
		profile, err := llm.ResolveTranscriptionProfile(llm.DefaultTranscriptionProfile)
		if err != nil {
			return apperror.Wrap(apperror.Internal, err)
		}
		digest := sha256.Sum256([]byte(fmt.Sprintf("%s:v%d:TRANSCRIPTION", profile.Key, profile.Version)))
		job := map[string]any{"tenant_id": cmd.TenantID, "id": jobID, "note_id": cmd.NoteID, "generation": 1, "profile_key": profile.Key, "profile_version": profile.Version, "profile_digest": hex.EncodeToString(digest[:]), "status": "QUEUED", "attempts": 0, "created_at": now, "updated_at": now}
		if err := tx.Table("transcription.jobs").Create(&job).Error; err != nil {
			return err
		}
		envelope := events.Envelope[events.TranscriptionRequestedPayload]{ID: identity.NewID(), Type: "transcription.requested.v1", SchemaVersion: 1, OccurredAt: now, TenantID: cmd.TenantID, AggregateID: cmd.NoteID, CorrelationID: cmd.NoteID.String(), Payload: events.TranscriptionRequestedPayload{NoteID: cmd.NoteID, JobID: jobID, Generation: 1}}
		payload, err := json.Marshal(envelope)
		if err != nil {
			return err
		}
		if err := tx.Table("messaging.outbox").Create(&map[string]any{"id": identity.NewID(), "tenant_id": cmd.TenantID, "type": envelope.Type, "schema_version": 1, "payload": payload, "correlation_id": envelope.CorrelationID, "status": "PENDING", "attempts": 0, "next_attempt_at": now, "created_at": now}).Error; err != nil {
			return err
		}
		return nil
	})
	if expiredDuringVerification {
		return apperror.New(apperror.InvalidState, "noteId", "Esta gravação expirou e não pode ser concluída.")
	}
	if completeErr != nil {
		if apperror.Is(completeErr, apperror.InvalidState) {
			cleanupErr := within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
				now := time.Now().UTC()
				update := tx.Exec("UPDATE transcription.audio_notes SET status='EXPIRED',version=version+1,updated_at=? WHERE tenant_id=? AND id=? AND status='UPLOADING'", now, cmd.TenantID, cmd.NoteID)
				if update.Error != nil {
					return update.Error
				}
				if update.RowsAffected != 1 {
					return nil
				}
				return scheduleCompletedObjectDeletion(tx, cmd, note, now)
			})
			if cleanupErr != nil {
				return apperror.Wrap(apperror.Internal, cleanupErr)
			}
		} else {
			return completeErr
		}
	}
	return completeErr
}

func scheduleCompletedObjectDeletion(tx *gorm.DB, cmd Command, current upload, now time.Time) error {
	if current.UploadKey == nil {
		return nil
	}
	if err := tx.Exec(`INSERT INTO transcription.audio_deletions (tenant_id,id,note_id,object_generation,object_key,derivative_keys,status,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT (tenant_id,note_id,object_generation) DO UPDATE SET
		object_key=CASE WHEN transcription.audio_deletions.status='DELETED' THEN EXCLUDED.object_key ELSE transcription.audio_deletions.object_key END,
		derivative_keys=CASE WHEN transcription.audio_deletions.status='DELETED' THEN EXCLUDED.derivative_keys ELSE transcription.audio_deletions.derivative_keys END,
		status=CASE WHEN transcription.audio_deletions.status IN ('DELETED','PROCESSING') THEN 'PENDING' ELSE transcription.audio_deletions.status END,
		lease_token=CASE WHEN transcription.audio_deletions.status IN ('DELETED','PROCESSING') THEN NULL ELSE transcription.audio_deletions.lease_token END,
		lease_deadline=CASE WHEN transcription.audio_deletions.status IN ('DELETED','PROCESSING') THEN NULL ELSE transcription.audio_deletions.lease_deadline END,
		retry_at=CASE WHEN transcription.audio_deletions.status IN ('DELETED','PROCESSING') THEN EXCLUDED.updated_at ELSE transcription.audio_deletions.retry_at END,
		deleted_at=CASE WHEN transcription.audio_deletions.status IN ('DELETED','PROCESSING') THEN NULL ELSE transcription.audio_deletions.deleted_at END,
		updated_at=EXCLUDED.updated_at`, cmd.TenantID, identity.NewID(), cmd.NoteID, 1, *current.UploadKey, []byte("[]"), "PENDING", now, now).Error; err != nil {
		return err
	}
	return tx.Exec("UPDATE transcription.audio_notes SET audio_deletion_status='SCHEDULED' WHERE tenant_id=? AND id=? AND audio_deletion_status IN ('PENDING','FAILED','PROCESSING','DELETED','EXPIRED')", cmd.TenantID, cmd.NoteID).Error
}

func resolveAnchorStatus(tx *gorm.DB, tenantID identity.ID, note upload) (string, error) {
	if note.TargetKind == "PHOTO" && note.PhotoMediaID != nil {
		var status string
		if err := tx.Raw("SELECT status FROM media.media_objects WHERE tenant_id=? AND id=?", tenantID, *note.PhotoMediaID).Row().Scan(&status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "UNAVAILABLE", nil
			}
			return "", err
		}
		switch status {
		case "READY":
			return "VALID", nil
		case "REJECTED", "FAILED":
			return "INVALID", nil
		case "ABORTED", "PURGED":
			return "UNAVAILABLE", nil
		default:
			return "PENDING", nil
		}
	}
	if note.TargetKind == "VIDEO_INTERVAL" && note.VideoAttemptID != nil {
		var status string
		if err := tx.Raw("SELECT status FROM capture.video_attempts WHERE tenant_id=? AND id=?", tenantID, *note.VideoAttemptID).Row().Scan(&status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "UNAVAILABLE", nil
			}
			return "", err
		}
		switch status {
		case "RECEIVED":
			// Video preparation resolves the exact attempt and interval asynchronously.
			return "PENDING", nil
		case "REJECTED":
			return "INVALID", nil
		case "CANCELED", "EXPIRED":
			return "UNAVAILABLE", nil
		default:
			return "PENDING", nil
		}
	}
	return "UNAVAILABLE", nil
}
