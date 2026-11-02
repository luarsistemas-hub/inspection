package revise_note

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/transcription/core"
	"inspection/services/inspection/internal/platform/apperror"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TenantScope executes the supplied query in the caller's tenant database scope.
type TenantScope func(context.Context, identity.ID, func(*gorm.DB) error) error

// Dependencies contains the persistence boundary required by this operation.
type Dependencies struct {
	DB     *gorm.DB
	Within TenantScope
}

// Command describes an immutable text revision and the expected note version.
type Command struct {
	TenantID, NoteID identity.ID
	ExpectedVersion  int
	ClientMutationID string
	Text             string
	Kind             string
	Highlighted      bool
	AuthorID         *identity.ID
	HighlightSource  string
	IsParticipant    bool
}

// Setup appends an immutable revision and records an idempotency receipt.
func Setup(ctx context.Context, deps Dependencies, cmd Command) (identity.ID, error) {
	if deps.DB == nil || deps.Within == nil || cmd.TenantID == (identity.ID{}) || cmd.NoteID == (identity.ID{}) || cmd.ExpectedVersion < 1 || cmd.ClientMutationID == "" {
		return identity.ID{}, apperror.New(apperror.InvalidInput, "input", "Os dados da revisão estão incompletos.")
	}
	if cmd.Kind != "HUMAN_EDIT" && cmd.Kind != "PARTICIPANT_DECLARATION" && cmd.Kind != "CONFIRM" && cmd.Kind != "HIGHLIGHT" {
		return identity.ID{}, apperror.New(apperror.InvalidInput, "revisionKind", "O tipo de revisão informado é inválido.")
	}
	clean := ""
	if cmd.Kind == "HUMAN_EDIT" || cmd.Kind == "PARTICIPANT_DECLARATION" {
		var err error
		clean, err = core.NormalizeTranscript(cmd.Text)
		if err != nil {
			return identity.ID{}, apperror.New(apperror.InvalidInput, "text", "O texto deve conter até 12 mil caracteres.")
		}
	}
	payload, _ := json.Marshal(struct {
		Text, Kind      string
		ExpectedVersion int
		Highlighted     bool
	}{clean, cmd.Kind, cmd.ExpectedVersion, cmd.Highlighted})
	digestBytes := sha256.Sum256(payload)
	digest := hex.EncodeToString(digestBytes[:])
	var revisionID identity.ID
	err := deps.Within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		var note struct {
			ID                identity.ID
			Status            string
			Version           int
			AnchorStatus      string
			ResponsibilityID  identity.ID
			CurrentRevisionID *identity.ID
		}
		query := "SELECT id,status,version,anchor_status,responsibility_id,current_revision_id FROM transcription.audio_notes WHERE tenant_id=? AND id=?"
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
		if cmd.IsParticipant && (cmd.Kind == "HUMAN_EDIT" || cmd.Kind == "HIGHLIGHT") {
			var open struct{ ID identity.ID }
			draftQuery := tx.Table("capture.capture_drafts").Select("id").Where("tenant_id=? AND responsibility_id=? AND status='OPEN'", cmd.TenantID, note.ResponsibilityID)
			if tx.Dialector.Name() == "postgres" {
				draftQuery = draftQuery.Clauses(clause.Locking{Strength: "SHARE"})
			}
			if err := draftQuery.Scan(&open).Error; err != nil {
				return err
			}
			if open.ID == (identity.ID{}) {
				return apperror.New(apperror.InvalidState, "noteId", "A captura não está mais aberta para editar esta gravação.")
			}
		}
		if cmd.Kind == "CONFIRM" || cmd.Kind == "HIGHLIGHT" {
			now := time.Now().UTC()
			var update *gorm.DB
			if cmd.Kind == "CONFIRM" {
				if note.Status != "COMPLETED" || note.CurrentRevisionID == nil {
					return apperror.New(apperror.InvalidState, "noteId", "A nota ainda não possui texto para confirmar.")
				}
				update = tx.Exec("UPDATE transcription.audio_notes SET text_confirmed=true,version=version+1,updated_at=? WHERE tenant_id=? AND id=? AND version=?", now, cmd.TenantID, cmd.NoteID, cmd.ExpectedVersion)
			} else {
				if note.Status == "CANCELED" || note.Status == "EXPIRED" {
					return apperror.New(apperror.InvalidState, "noteId", "Esta nota não pode mais ser destacada.")
				}
				if cmd.HighlightSource == "" {
					cmd.HighlightSource = "PARTICIPANT"
				}
				update = tx.Exec("UPDATE transcription.audio_notes SET highlighted=?,highlight_source=?,version=version+1,updated_at=? WHERE tenant_id=? AND id=? AND version=?", cmd.Highlighted, cmd.HighlightSource, now, cmd.TenantID, cmd.NoteID, cmd.ExpectedVersion)
			}
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return apperror.New(apperror.Conflict, "expectedVersion", "A nota foi alterada. Atualize os dados e tente novamente.")
			}
			return tx.Table("transcription.mutation_receipts").Create(&map[string]any{"tenant_id": cmd.TenantID, "id": identity.NewID(), "note_id": cmd.NoteID, "client_mutation_id": cmd.ClientMutationID, "operation": cmd.Kind, "payload_digest": digest, "result_version": cmd.ExpectedVersion + 1, "created_at": now}).Error
		}
		if cmd.Kind == "PARTICIPANT_DECLARATION" {
			// RETRY_WAIT can still have scheduled work; typed participant fallback is limited to terminal FAILED jobs.
			if note.Status != "FAILED" {
				return apperror.New(apperror.InvalidState, "noteId", "A declaração só pode ser registrada quando a transcrição falhar.")
			}
			var pendingJobs int64
			if err := tx.Session(&gorm.Session{NewDB: true}).Table("transcription.jobs").Where("tenant_id=? AND note_id=? AND status IN ('QUEUED','PROCESSING','RETRY_WAIT')", cmd.TenantID, cmd.NoteID).Count(&pendingJobs).Error; err != nil {
				return err
			}
			if pendingJobs > 0 {
				return apperror.New(apperror.InvalidState, "noteId", "A transcrição ainda tem uma tentativa pendente.")
			}
			if note.AnchorStatus != "VALID" {
				return apperror.New(apperror.InvalidState, "noteId", "O vínculo da gravação ainda não foi validado.")
			}
			var open struct{ ID identity.ID }
			draftQuery := tx.Table("capture.capture_drafts").Select("id").Where("tenant_id=? AND responsibility_id=? AND status='OPEN'", cmd.TenantID, note.ResponsibilityID)
			if tx.Dialector.Name() == "postgres" {
				draftQuery = draftQuery.Clauses(clause.Locking{Strength: "SHARE"})
			}
			if err := draftQuery.Scan(&open).Error; err != nil {
				return err
			}
			if open.ID == (identity.ID{}) {
				return apperror.New(apperror.InvalidState, "noteId", "A captura não está mais aberta para receber uma declaração.")
			}
		} else {
			if (note.Status != "COMPLETED" && note.Status != "FAILED") || note.CurrentRevisionID == nil {
				return apperror.New(apperror.InvalidState, "noteId", "Esta nota ainda não possui uma revisão disponível para correção.")
			}
		}
		var number int
		if err := tx.Raw("SELECT coalesce(max(revision_number),0)+1 FROM transcription.transcript_revisions WHERE tenant_id=? AND note_id=?", cmd.TenantID, cmd.NoteID).Scan(&number).Error; err != nil {
			return err
		}
		revisionID = identity.NewID()
		now := time.Now().UTC()
		if err := tx.Exec("INSERT INTO transcription.transcript_revisions (id,tenant_id,note_id,revision_number,text,kind,author_id,created_at) VALUES (?,?,?,?,?,?,?,?)", revisionID, cmd.TenantID, cmd.NoteID, number, clean, cmd.Kind, cmd.AuthorID, now).Error; err != nil {
			return err
		}
		update := tx.Exec("UPDATE transcription.audio_notes SET current_revision_id=?,text_confirmed=false,version=version+1,updated_at=? WHERE tenant_id=? AND id=? AND version=?", revisionID, now, cmd.TenantID, cmd.NoteID, cmd.ExpectedVersion)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "expectedVersion", "A nota foi alterada. Atualize os dados e tente novamente.")
		}
		return tx.Table("transcription.mutation_receipts").Create(&map[string]any{"tenant_id": cmd.TenantID, "id": identity.NewID(), "note_id": cmd.NoteID, "client_mutation_id": cmd.ClientMutationID, "operation": cmd.Kind, "payload_digest": digest, "result_version": cmd.ExpectedVersion + 1, "created_at": now}).Error
	})
	return revisionID, err
}
