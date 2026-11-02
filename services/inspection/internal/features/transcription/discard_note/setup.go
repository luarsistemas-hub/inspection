package discard_note

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Dependencies are the tenant-scoped database boundary for discarding a note.
type Dependencies struct {
	DB     *gorm.DB
	Store  objectstore.Store
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
}

// Command identifies the note and expected version for an idempotent discard.
type Command struct {
	TenantID, NoteID identity.ID
	ExpectedVersion  int
	ClientMutationID string
	IsParticipant    bool
}

// Setup cancels pending work, records audio cleanup, and closes an eligible note atomically.
func Setup(ctx context.Context, deps Dependencies, cmd Command) error {
	if deps.DB == nil || cmd.TenantID == (identity.ID{}) || cmd.NoteID == (identity.ID{}) || cmd.ExpectedVersion < 1 || cmd.ClientMutationID == "" {
		return apperror.New(apperror.InvalidInput, "input", "Os dados para descartar a gravação estão incompletos.")
	}
	within := deps.Within
	if within == nil {
		within = (tenanttx.Runner{DB: deps.DB}).Within
	}
	body, _ := json.Marshal(struct {
		Operation       string
		ExpectedVersion int
	}{"DISCARD", cmd.ExpectedVersion})
	digestBytes := sha256.Sum256(body)
	digest := hex.EncodeToString(digestBytes[:])
	var abortKey, abortID string
	err := within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		var note struct {
			ID               identity.ID
			Status           string
			Version          int
			UploadKey        *string
			UploadID         *string
			ResponsibilityID identity.ID
		}
		query := "SELECT id,status,version,upload_key,upload_id,responsibility_id FROM transcription.audio_notes WHERE tenant_id=? AND id=?"
		if tx.Dialector.Name() == "postgres" {
			query += " FOR UPDATE"
		}
		if err := tx.Raw(query, cmd.TenantID, cmd.NoteID).Scan(&note).Error; err != nil {
			return err
		}
		if note.ID == (identity.ID{}) {
			return apperror.New(apperror.NotFound, "noteId", "A nota de áudio não foi encontrada.")
		}
		var receipt struct {
			ID            identity.ID
			PayloadDigest string
		}
		if err := tx.Raw("SELECT id,payload_digest FROM transcription.mutation_receipts WHERE tenant_id=? AND note_id=? AND client_mutation_id=?", cmd.TenantID, cmd.NoteID, cmd.ClientMutationID).Scan(&receipt).Error; err != nil {
			return err
		}
		if receipt.ID != (identity.ID{}) {
			if receipt.PayloadDigest != digest {
				return apperror.New(apperror.Conflict, "clientMutationId", "A identificação da alteração já foi usada com outros dados.")
			}
			return nil
		}
		if note.Version != cmd.ExpectedVersion {
			return apperror.New(apperror.Conflict, "expectedVersion", "A nota foi alterada. Atualize os dados e tente novamente.")
		}
		if note.Status == "UPLOADING" && note.UploadKey != nil && note.UploadID != nil {
			abortKey, abortID = *note.UploadKey, *note.UploadID
		}
		if cmd.IsParticipant {
			var draft struct{ ID identity.ID }
			draftQuery := tx.Table("capture.capture_drafts").Select("id").Where("tenant_id=? AND responsibility_id=? AND status='OPEN'", cmd.TenantID, note.ResponsibilityID)
			if tx.Dialector.Name() == "postgres" {
				draftQuery = draftQuery.Clauses(clause.Locking{Strength: "SHARE"})
			}
			if err := draftQuery.Scan(&draft).Error; err != nil {
				return err
			}
			if draft.ID == (identity.ID{}) {
				return apperror.New(apperror.InvalidState, "noteId", "A captura foi concluída e não pode mais alterar esta gravação.")
			}
		}
		switch note.Status {
		case "UPLOADING", "QUEUED", "RETRY_WAIT", "FAILED", "COMPLETED":
		default:
			return apperror.New(apperror.InvalidState, "noteId", "Esta gravação não pode mais ser descartada.")
		}
		now := time.Now().UTC()
		if err := tx.Exec("UPDATE transcription.jobs SET status='CANCELED',updated_at=? WHERE tenant_id=? AND note_id=? AND status IN ('QUEUED','RETRY_WAIT')", now, cmd.TenantID, cmd.NoteID).Error; err != nil {
			return err
		}
		update := tx.Exec("UPDATE transcription.audio_notes SET status='CANCELED',version=version+1,updated_at=? WHERE tenant_id=? AND id=? AND version=?", now, cmd.TenantID, cmd.NoteID, cmd.ExpectedVersion)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "expectedVersion", "A nota foi alterada. Atualize os dados e tente novamente.")
		}
		if note.UploadKey != nil {
			if err := tx.Exec("INSERT INTO transcription.audio_deletions (tenant_id,id,note_id,object_generation,object_key,derivative_keys,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING", cmd.TenantID, identity.NewID(), cmd.NoteID, 1, *note.UploadKey, []byte("[]"), "PENDING", now, now).Error; err != nil {
				return err
			}
			if err := tx.Exec("UPDATE transcription.audio_notes SET audio_deletion_status='SCHEDULED' WHERE tenant_id=? AND id=? AND audio_deletion_status IN ('PENDING','FAILED')", cmd.TenantID, cmd.NoteID).Error; err != nil {
				return err
			}
		}
		return tx.Table("transcription.mutation_receipts").Create(&map[string]any{"tenant_id": cmd.TenantID, "id": identity.NewID(), "note_id": cmd.NoteID, "client_mutation_id": cmd.ClientMutationID, "operation": "DISCARD", "payload_digest": digest, "result_version": cmd.ExpectedVersion + 1, "created_at": now}).Error
	})
	if err != nil {
		return err
	}
	if abortKey != "" && abortID != "" && deps.Store.Client != nil {
		_ = deps.Store.Client.AbortMultipart(ctx, deps.Store.Bucket, abortKey, abortID)
	}
	return nil
}
