package retry_note

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/features/transcription/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Dependencies are the tenant-scoped persistence boundary for retrying one note.
type Dependencies struct {
	DB     *gorm.DB
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
}

// Command contains the note version and idempotency key expected by the participant or operator.
type Command struct {
	TenantID, NoteID identity.ID
	ExpectedVersion  int
	ClientMutationID string
	IsParticipant    bool
}

const maxJobGenerations = 4

// Setup creates one new generation and its outbox event for a recoverable note.
func Setup(ctx context.Context, deps Dependencies, cmd Command) error {
	if deps.DB == nil || cmd.TenantID == (identity.ID{}) || cmd.NoteID == (identity.ID{}) || cmd.ExpectedVersion < 1 || cmd.ClientMutationID == "" {
		return apperror.New(apperror.InvalidInput, "input", "Os dados da nova tentativa estão incompletos.")
	}
	within := deps.Within
	if within == nil {
		within = (tenanttx.Runner{DB: deps.DB}).Within
	}
	body, _ := json.Marshal(struct {
		Operation       string
		ExpectedVersion int
	}{"RETRY", cmd.ExpectedVersion})
	digestBytes := sha256.Sum256(body)
	digest := hex.EncodeToString(digestBytes[:])
	return within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		var note struct {
			ID                  identity.ID
			Status              string
			Version             int
			AudioDeletionStatus string
			ResponsibilityID    identity.ID
			InspectionID        identity.ID
			DurationMS          int64
		}
		query := "SELECT id,status,version,audio_deletion_status,responsibility_id,inspection_id,duration_ms FROM transcription.audio_notes WHERE tenant_id=? AND id=?"
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
		// The scheduler expiry marks both the note and its audio; deletion may already have finished.
		if note.Status == "EXPIRED" || note.AudioDeletionStatus == "EXPIRED" {
			return apperror.New(apperror.Expired, "noteId", "O áudio original expirou e não pode ser enviado novamente.")
		}
		if note.Status != "FAILED" || note.AudioDeletionStatus == "DELETED" {
			return apperror.New(apperror.RetryUnavailable, "noteId", "O áudio original não está disponível para nova tentativa.")
		}
		var uploadCompletedAt *time.Time
		if err := tx.Raw("SELECT created_at FROM transcription.jobs WHERE tenant_id=? AND note_id=? AND generation=1", cmd.TenantID, cmd.NoteID).Scan(&uploadCompletedAt).Error; err != nil {
			return err
		}
		if uploadCompletedAt == nil {
			return apperror.New(apperror.RetryUnavailable, "noteId", "O áudio original não está disponível para nova tentativa.")
		}
		// O created_at do job da geração 1 é gravado na conclusão do upload e não muda depois.
		// O prazo usa o relógio do banco, o mesmo da varredura de expiração, e não o do processo.
		dbNow := time.Now()
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Raw("SELECT now()").Row().Scan(&dbNow); err != nil {
				return err
			}
		}
		if dbNow.Sub(*uploadCompletedAt) >= 24*time.Hour {
			return apperror.New(apperror.RetryUnavailable, "noteId", "O prazo para tentar novamente esta gravação expirou.")
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
				return apperror.New(apperror.RetryUnavailable, "noteId", "A captura foi concluída e não aceita nova tentativa.")
			}
		}
		var pending int64
		if err := tx.Table("transcription.jobs").Where("tenant_id=? AND note_id=? AND status IN ('QUEUED','PROCESSING','RETRY_WAIT')", cmd.TenantID, cmd.NoteID).Count(&pending).Error; err != nil {
			return err
		}
		if pending != 0 {
			return apperror.New(apperror.RetryUnavailable, "noteId", "A gravação já possui uma tentativa em andamento.")
		}
		var policy struct {
			TenantNoteLimit, InspectionNoteLimit       int
			TenantAudioLimitMS, InspectionAudioLimitMS int64
		}
		policyQuery := "SELECT tenant_note_limit,tenant_audio_limit_ms,inspection_note_limit,inspection_audio_limit_ms FROM transcription.tenant_policies WHERE tenant_id=?"
		if tx.Dialector.Name() == "postgres" {
			policyQuery += " FOR UPDATE"
		}
		if err := tx.Raw(policyQuery, cmd.TenantID).Scan(&policy).Error; err != nil {
			return err
		}
		var tenantCount, inspectionCount int64
		var tenantAudio, inspectionAudio int64
		reservationTotals := `SELECT coalesce(sum(attempts),0),coalesce(sum(duration_ms*attempts),0) FROM (
		 SELECT n.duration_ms,CASE WHEN count(j.id)>1 THEN count(j.id) ELSE 1 END AS attempts FROM transcription.audio_notes n
		 LEFT JOIN transcription.jobs j ON j.tenant_id=n.tenant_id AND j.note_id=n.id
		 WHERE n.tenant_id=? AND (n.status='UPLOADING' OR j.id IS NOT NULL) %s GROUP BY n.tenant_id,n.id,n.duration_ms
		) reservations`
		tenantSQL := fmt.Sprintf(reservationTotals, "")
		if err := tx.Raw(tenantSQL, cmd.TenantID).Row().Scan(&tenantCount, &tenantAudio); err != nil {
			return err
		}
		inspectionSQL := fmt.Sprintf(reservationTotals, "AND n.inspection_id=?")
		if err := tx.Raw(inspectionSQL, cmd.TenantID, note.InspectionID).Row().Scan(&inspectionCount, &inspectionAudio); err != nil {
			return err
		}
		limits := core.Policy{Enabled: true, TenantNoteLimit: policy.TenantNoteLimit, TenantAudioLimitMS: policy.TenantAudioLimitMS, InspectionNoteLimit: policy.InspectionNoteLimit, InspectionAudioLimitMS: policy.InspectionAudioLimitMS}
		if err := limits.CanReserve(int(tenantCount), tenantAudio, int(inspectionCount), inspectionAudio, note.DurationMS); err != nil {
			if errors.Is(err, core.ErrLimitReached) {
				return apperror.New(apperror.LimitReached, "audio", "O limite de uso de voz foi atingido.")
			}
			return apperror.New(apperror.InvalidInput, "limits", "Os limites de uso de voz precisam ser configurados.")
		}
		var generation int
		if err := tx.Raw("SELECT coalesce(max(generation),0)+1 FROM transcription.jobs WHERE tenant_id=? AND note_id=?", cmd.TenantID, cmd.NoteID).Scan(&generation).Error; err != nil {
			return err
		}
		if generation > maxJobGenerations {
			return apperror.New(apperror.RetryUnavailable, "noteId", "O limite de tentativas foi atingido.")
		}
		now := time.Now().UTC()
		jobID := identity.NewID()
		profile, err := llm.ResolveTranscriptionProfile(llm.DefaultTranscriptionProfile)
		if err != nil {
			return apperror.Wrap(apperror.Internal, err)
		}
		profileDigest := sha256.Sum256([]byte(fmt.Sprintf("%s:v%d:TRANSCRIPTION", profile.Key, profile.Version)))
		if err := tx.Table("transcription.jobs").Create(&map[string]any{"tenant_id": cmd.TenantID, "id": jobID, "note_id": cmd.NoteID, "generation": generation, "profile_key": profile.Key, "profile_version": profile.Version, "profile_digest": hex.EncodeToString(profileDigest[:]), "status": "QUEUED", "attempts": 0, "created_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		envelope := events.Envelope[events.TranscriptionRequestedPayload]{ID: identity.NewID(), Type: "transcription.requested.v1", SchemaVersion: 1, OccurredAt: now, TenantID: cmd.TenantID, AggregateID: cmd.NoteID, CorrelationID: cmd.NoteID.String(), Payload: events.TranscriptionRequestedPayload{NoteID: cmd.NoteID, JobID: jobID, Generation: generation}}
		payload, err := json.Marshal(envelope)
		if err != nil {
			return err
		}
		if err := tx.Table("messaging.outbox").Create(&map[string]any{"id": identity.NewID(), "tenant_id": cmd.TenantID, "type": envelope.Type, "schema_version": 1, "payload": payload, "correlation_id": envelope.CorrelationID, "status": "PENDING", "attempts": 0, "next_attempt_at": now, "created_at": now}).Error; err != nil {
			return err
		}
		update := tx.Exec("UPDATE transcription.audio_notes SET status='QUEUED',safe_failure_code=NULL,no_speech=false,version=version+1,updated_at=? WHERE tenant_id=? AND id=? AND version=?", now, cmd.TenantID, cmd.NoteID, cmd.ExpectedVersion)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "expectedVersion", "A nota foi alterada. Atualize os dados e tente novamente.")
		}
		return tx.Table("transcription.mutation_receipts").Create(&map[string]any{"tenant_id": cmd.TenantID, "id": identity.NewID(), "note_id": cmd.NoteID, "client_mutation_id": cmd.ClientMutationID, "operation": "RETRY", "payload_digest": digest, "result_version": cmd.ExpectedVersion + 1, "created_at": now}).Error
	})
}
