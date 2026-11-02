package expire_audio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"

	"gorm.io/gorm"
)

// postgresRepository implements the scans with forced-RLS tenant transactions.
// Lock order is always note before job/deletion, matching the API and worker.
type postgresRepository struct {
	db     *gorm.DB
	within func(context.Context, identity.ID, func(*gorm.DB) error) error
}

// stagedResultSQL matches the worker's definition of an already-paid result.
const stagedResultSQL = `SELECT EXISTS(SELECT 1 FROM transcription.transcript_revisions r
 JOIN usage.llm_calls c ON c.tenant_id=r.tenant_id AND c.call_id=r.call_id
 WHERE r.tenant_id=? AND r.note_id=? AND r.kind='MODEL' AND c.job_id=? AND c.replay_generation=? AND c.comparison_mode='TRANSCRIPTION')`

func (r postgresRepository) DueTenants(ctx context.Context, retention time.Duration) ([]identity.ID, error) {
	var tenants []identity.ID
	err := r.db.WithContext(ctx).Raw(`SELECT due.tenant_id FROM transcription.discover_due_tenants(make_interval(secs => ?)) AS due(tenant_id)`, retention.Seconds()).Scan(&tenants).Error
	return tenants, err
}

func (r postgresRepository) RecoverLeases(ctx context.Context, tenantID identity.ID, limit int, retention time.Duration) (int, int, error) {
	recovered, exhausted := 0, 0
	err := r.within(ctx, tenantID, func(tx *gorm.DB) error {
		recovered, exhausted = 0, 0
		var jobs []struct {
			ID, NoteID           identity.ID
			Generation, Attempts int
			LeaseToken           identity.ID
		}
		// Locking both rows makes PostgreSQL recheck the lease against their
		// latest committed versions; a worker holding either row is skipped.
		if err := tx.Raw(`SELECT j.id,j.note_id,j.generation,j.attempts,j.lease_token
FROM transcription.jobs j JOIN transcription.audio_notes n ON n.tenant_id=j.tenant_id AND n.id=j.note_id
WHERE j.tenant_id=? AND j.status='PROCESSING' AND j.lease_deadline<=now()
ORDER BY j.lease_deadline,j.id LIMIT ? FOR UPDATE OF n,j SKIP LOCKED`, tenantID, limit).Scan(&jobs).Error; err != nil {
			return err
		}
		for _, job := range jobs {
			var staged bool
			if err := tx.Raw(stagedResultSQL, tenantID, job.NoteID, job.ID, job.Generation).Row().Scan(&staged); err != nil {
				return err
			}
			if !staged && job.Attempts >= MaxProcessingAttempts {
				changed, err := failExhaustedJob(tx, tenantID, job.ID, job.NoteID, job.Generation, job.LeaseToken)
				if err != nil {
					return err
				}
				if changed {
					exhausted++
				}
				continue
			}
			pastRetention := false
			if !staged {
				if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM transcription.jobs WHERE tenant_id=? AND note_id=? AND generation=1 AND created_at<=now()-make_interval(secs => ?))`, tenantID, job.NoteID, retention.Seconds()).Row().Scan(&pastRetention); err != nil {
					return err
				}
			}
			// The retry scan publishes the wakeup; audio past retention gets no
			// retry time so the expiry scan closes it instead of a late worker.
			// A staged result is completed by the worker from the stored
			// revision, so the provider is not called again for this generation.
			update := tx.Exec(`UPDATE transcription.jobs SET status='RETRY_WAIT',retry_at=CASE WHEN ? THEN NULL ELSE now() END,lease_token=NULL,lease_deadline=NULL,safe_failure_code='LEASE_EXPIRED',updated_at=now()
WHERE tenant_id=? AND id=? AND generation=? AND status='PROCESSING' AND lease_token=?`, pastRetention, tenantID, job.ID, job.Generation, job.LeaseToken)
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				continue
			}
			if err := tx.Exec(`UPDATE transcription.audio_notes SET status='RETRY_WAIT',safe_failure_code='LEASE_EXPIRED',version=version+1,updated_at=now() WHERE tenant_id=? AND id=? AND status='PROCESSING'`, tenantID, job.NoteID).Error; err != nil {
				return err
			}
			recovered++
		}
		return nil
	})
	return recovered, exhausted, err
}

func failExhaustedJob(tx *gorm.DB, tenantID, jobID, noteID identity.ID, generation int, lease identity.ID) (bool, error) {
	const code = "RETRY_LIMIT_EXCEEDED"
	update := tx.Exec(`UPDATE transcription.jobs SET status='FAILED',retry_at=NULL,lease_token=NULL,lease_deadline=NULL,safe_failure_code=?,updated_at=now()
WHERE tenant_id=? AND id=? AND generation=? AND status='PROCESSING' AND lease_token=?`, code, tenantID, jobID, generation, lease)
	if update.Error != nil || update.RowsAffected != 1 {
		return false, update.Error
	}
	if err := tx.Exec(`UPDATE transcription.audio_notes SET status='FAILED',safe_failure_code=?,version=version+1,updated_at=now() WHERE tenant_id=? AND id=? AND status='PROCESSING'`, code, tenantID, noteID).Error; err != nil {
		return false, err
	}
	return true, addFailedOutbox(tx, tenantID, noteID, generation, code)
}

func (r postgresRepository) ExpireNotes(ctx context.Context, tenantID identity.ID, limit int, retention time.Duration) (int, error) {
	expired := 0
	err := r.within(ctx, tenantID, func(tx *gorm.DB) error {
		expired = 0
		var candidates []identity.ID
		if err := tx.Raw(`SELECT n.id FROM transcription.audio_notes n
WHERE n.tenant_id=? AND `+expirableNoteSQL+`
ORDER BY n.created_at,n.id LIMIT ? FOR UPDATE OF n SKIP LOCKED`, tenantID, retention.Seconds(), retention.Seconds(), limit).Scan(&candidates).Error; err != nil {
			return err
		}
		for _, noteID := range candidates {
			// Re-evaluate in a new statement: the note row is locked by this
			// transaction, and every API/worker change to its jobs locks it too.
			var note struct {
				Status    string
				UploadKey sql.NullString
				UploadID  sql.NullString
			}
			if err := tx.Raw(`SELECT n.status,n.upload_key,n.upload_id FROM transcription.audio_notes n WHERE n.tenant_id=? AND n.id=? AND `+expirableNoteSQL, tenantID, noteID, retention.Seconds(), retention.Seconds()).Scan(&note).Error; err != nil {
				return err
			}
			if note.Status == "" {
				continue
			}
			update := tx.Exec(`UPDATE transcription.audio_notes SET status='EXPIRED',audio_deletion_status=CASE WHEN audio_deletion_status='DELETED' THEN 'DELETED' ELSE 'EXPIRED' END,
safe_failure_code='AUDIO_EXPIRED',version=version+1,updated_at=now() WHERE tenant_id=? AND id=? AND status=?`, tenantID, noteID, note.Status)
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				continue
			}
			var latest struct{ Generation int }
			if err := tx.Raw(`SELECT COALESCE(max(generation),0) AS generation FROM transcription.jobs WHERE tenant_id=? AND note_id=?`, tenantID, noteID).Scan(&latest).Error; err != nil {
				return err
			}
			// FAILED jobs keep their original safe code as attempt history.
			if err := tx.Exec(`UPDATE transcription.jobs SET status='EXPIRED',retry_at=NULL,lease_token=NULL,lease_deadline=NULL,safe_failure_code='AUDIO_EXPIRED',updated_at=now()
WHERE tenant_id=? AND note_id=? AND status IN ('QUEUED','RETRY_WAIT')`, tenantID, noteID).Error; err != nil {
				return err
			}
			if note.UploadKey.Valid && note.UploadKey.String != "" {
				if err := tx.Exec(`INSERT INTO transcription.audio_deletions(tenant_id,id,note_id,object_generation,object_key,derivative_keys,status,retry_at,created_at,updated_at)
VALUES(?,?,?,1,?,'[]'::jsonb,'PENDING',now(),now(),now()) ON CONFLICT(tenant_id,note_id,object_generation) DO NOTHING`, tenantID, identity.NewID(), noteID, note.UploadKey.String).Error; err != nil {
					return err
				}
			}
			// A note that never completed its upload has no transcription event.
			if note.Status != "UPLOADING" && note.Status != "FAILED" && latest.Generation > 0 {
				if err := addFailedOutbox(tx, tenantID, noteID, latest.Generation, "AUDIO_EXPIRED"); err != nil {
					return err
				}
			}
			expired++
		}
		return nil
	})
	return expired, err
}

// expirableNoteSQL selects notes whose audio exceeded retention without a
// durable model result: abandoned uploads count from the upload intent, and
// accepted notes from the first job, written when the upload completed.
// Notes with an active worker lease or a staged result are left alone.
const expirableNoteSQL = `((n.status='UPLOADING' AND n.created_at<=now()-make_interval(secs => ?))
 OR (n.status IN ('QUEUED','RETRY_WAIT','FAILED') AND EXISTS (SELECT 1 FROM transcription.jobs first
  WHERE first.tenant_id=n.tenant_id AND first.note_id=n.id AND first.generation=1 AND first.created_at<=now()-make_interval(secs => ?))))
 AND NOT EXISTS (SELECT 1 FROM transcription.jobs active WHERE active.tenant_id=n.tenant_id AND active.note_id=n.id AND active.status='PROCESSING')
 AND NOT EXISTS (SELECT 1 FROM transcription.transcript_revisions r WHERE r.tenant_id=n.tenant_id AND r.note_id=n.id AND r.kind='MODEL')`

func (r postgresRepository) RequeueDue(ctx context.Context, tenantID identity.ID, limit int, redelivery time.Duration) (int, error) {
	requeued := 0
	err := r.within(ctx, tenantID, func(tx *gorm.DB) error {
		requeued = 0
		var jobs []struct {
			ID, NoteID           identity.ID
			Generation, Attempts int
		}
		if err := tx.Raw(`SELECT j.id,j.note_id,j.generation,j.attempts
FROM transcription.jobs j JOIN transcription.audio_notes n ON n.tenant_id=j.tenant_id AND n.id=j.note_id
WHERE j.tenant_id=? AND j.status='RETRY_WAIT' AND j.retry_at<=now() AND n.status='RETRY_WAIT'
ORDER BY j.retry_at,j.id LIMIT ? FOR UPDATE OF n,j SKIP LOCKED`, tenantID, limit).Scan(&jobs).Error; err != nil {
			return err
		}
		for _, job := range jobs {
			written, err := addRequestOutbox(tx, tenantID, job.NoteID, job.ID, job.Generation, fmt.Sprintf("retry-attempt-%d", job.Attempts), redelivery)
			if err != nil {
				return err
			}
			if written {
				requeued++
			}
		}
		return nil
	})
	return requeued, err
}

// addRequestOutbox writes a deterministic MSG01 per job, generation and wakeup.
// With a positive redelivery, a copy published at least that long ago is
// re-armed under the same ID, so a lost delivery cannot stall a due job while
// repeated scans never accumulate duplicate events.
func addRequestOutbox(tx *gorm.DB, tenantID, noteID, jobID identity.ID, generation int, wakeup string, redelivery time.Duration) (bool, error) {
	id := identity.NewDeterministicID("transcription-request-wakeup", fmt.Sprintf("%s:%d:%s", jobID, generation, wakeup))
	var now time.Time
	if err := tx.Raw("SELECT now()").Row().Scan(&now); err != nil {
		return false, err
	}
	envelope := events.Envelope[events.TranscriptionRequestedPayload]{ID: id, Type: "transcription.requested.v1", SchemaVersion: 1, OccurredAt: now.UTC(), TenantID: tenantID, AggregateID: noteID, CorrelationID: noteID.String(), Payload: events.TranscriptionRequestedPayload{NoteID: noteID, JobID: jobID, Generation: generation}}
	data, err := validatedEnvelope(envelope)
	if err != nil {
		return false, err
	}
	conflict := "DO NOTHING"
	args := []any{id, tenantID, envelope.Type, 1, data, envelope.CorrelationID}
	if redelivery > 0 {
		conflict = `DO UPDATE SET status='PENDING',next_attempt_at=now(),claimed_at=NULL,last_error='',published_at=NULL
 WHERE messaging.outbox.status='PUBLISHED' AND messaging.outbox.published_at<=now()-make_interval(secs => ?)`
		args = append(args, redelivery.Seconds())
	}
	result := tx.Exec(`INSERT INTO messaging.outbox(id,tenant_id,type,schema_version,payload,correlation_id,status,attempts,next_attempt_at,created_at)
VALUES(?,?,?,?,?,?,'PENDING',0,now(),now()) ON CONFLICT(id) `+conflict, args...)
	return result.RowsAffected == 1, result.Error
}

func addFailedOutbox(tx *gorm.DB, tenantID, noteID identity.ID, generation int, code string) error {
	id := identity.NewDeterministicID("transcription-failed", fmt.Sprintf("%s:%d:%s", noteID, generation, code))
	var now time.Time
	if err := tx.Raw("SELECT now()").Row().Scan(&now); err != nil {
		return err
	}
	envelope := events.Envelope[events.TranscriptionFailedPayload]{ID: id, Type: "transcription.failed.v1", SchemaVersion: 1, OccurredAt: now.UTC(), TenantID: tenantID, AggregateID: noteID, CorrelationID: noteID.String(), Payload: events.TranscriptionFailedPayload{NoteID: noteID, Generation: generation, SafeCode: code}}
	data, err := validatedEnvelope(envelope)
	if err != nil {
		return err
	}
	return tx.Exec(`INSERT INTO messaging.outbox(id,tenant_id,type,schema_version,payload,correlation_id,status,attempts,next_attempt_at,created_at)
VALUES(?,?,?,?,?,?,'PENDING',0,now(),now()) ON CONFLICT(id) DO NOTHING`, id, tenantID, envelope.Type, 1, data, envelope.CorrelationID).Error
}

func validatedEnvelope(envelope any) ([]byte, error) {
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	if _, err := events.DefaultRegistry().Validate(data); err != nil {
		return nil, fmt.Errorf("invalid transcription event contract: %w", err)
	}
	return data, nil
}

func (r postgresRepository) ClaimDeletions(ctx context.Context, tenantID identity.ID, limit int, lease time.Duration) ([]Deletion, error) {
	var claims []Deletion
	err := r.within(ctx, tenantID, func(tx *gorm.DB) error {
		claims = claims[:0]
		var rows []struct {
			ID, NoteID     identity.ID
			ObjectKey      string
			DerivativeKeys []byte
			UploadKey      sql.NullString
			UploadID       sql.NullString
		}
		// Only terminal notes are cleaned: a result that is not yet durable,
		// or a note still inside its recovery window, keeps its audio.
		if err := tx.Raw(`SELECT d.id,d.note_id,d.object_key,d.derivative_keys,n.upload_key,n.upload_id
FROM transcription.audio_deletions d JOIN transcription.audio_notes n ON n.tenant_id=d.tenant_id AND n.id=d.note_id
WHERE d.tenant_id=? AND ((d.status IN ('PENDING','FAILED') AND (d.retry_at IS NULL OR d.retry_at<=now())) OR (d.status='PROCESSING' AND d.lease_deadline<=now()))
 AND n.status IN ('COMPLETED','CANCELED','EXPIRED')
 AND NOT EXISTS (SELECT 1 FROM transcription.jobs j WHERE j.tenant_id=n.tenant_id AND j.note_id=n.id AND j.status='PROCESSING' AND j.lease_deadline>now())
ORDER BY COALESCE(d.retry_at,d.created_at),d.id LIMIT ? FOR UPDATE OF n,d SKIP LOCKED`, tenantID, limit).Scan(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			var derivatives []string
			if err := json.Unmarshal(row.DerivativeKeys, &derivatives); err != nil {
				return fmt.Errorf("invalid audio derivative key list: %w", err)
			}
			claim := Deletion{ID: row.ID, NoteID: row.NoteID, ObjectKey: row.ObjectKey, DerivativeKeys: derivatives, LeaseToken: identity.NewID()}
			if row.UploadKey.Valid && row.UploadID.Valid && row.UploadKey.String == row.ObjectKey {
				claim.UploadID = row.UploadID.String
			}
			if err := tx.Exec(`UPDATE transcription.audio_deletions SET status='PROCESSING',lease_token=?,lease_deadline=now()+make_interval(secs => ?),updated_at=now() WHERE tenant_id=? AND id=?`, claim.LeaseToken, lease.Seconds(), tenantID, claim.ID).Error; err != nil {
				return err
			}
			claims = append(claims, claim)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claims, nil
}

func (r postgresRepository) CompleteDeletion(ctx context.Context, tenantID identity.ID, claim Deletion) (bool, error) {
	done := false
	err := r.within(ctx, tenantID, func(tx *gorm.DB) error {
		done = false
		if err := lockNote(tx, tenantID, claim.NoteID); err != nil {
			return err
		}
		update := tx.Exec(`UPDATE transcription.audio_deletions SET status='DELETED',deleted_at=now(),lease_token=NULL,lease_deadline=NULL,retry_at=NULL,updated_at=now()
WHERE tenant_id=? AND id=? AND status='PROCESSING' AND lease_token=?`, tenantID, claim.ID, claim.LeaseToken)
		if update.Error != nil || update.RowsAffected != 1 {
			return update.Error
		}
		// The note keeps its version: deletion is independent of text review.
		if err := tx.Exec(`UPDATE transcription.audio_notes SET audio_deletion_status='DELETED',
upload_key=CASE WHEN upload_key=? THEN NULL ELSE upload_key END,upload_id=CASE WHEN upload_key=? THEN NULL ELSE upload_id END,updated_at=now()
WHERE tenant_id=? AND id=?`, claim.ObjectKey, claim.ObjectKey, tenantID, claim.NoteID).Error; err != nil {
			return err
		}
		done = true
		return nil
	})
	return done, err
}

func (r postgresRepository) FailDeletion(ctx context.Context, tenantID identity.ID, claim Deletion) (bool, error) {
	done := false
	err := r.within(ctx, tenantID, func(tx *gorm.DB) error {
		done = false
		if err := lockNote(tx, tenantID, claim.NoteID); err != nil {
			return err
		}
		var attempts int
		if err := tx.Raw(`SELECT attempts FROM transcription.audio_deletions WHERE tenant_id=? AND id=? AND status='PROCESSING' AND lease_token=? FOR UPDATE`, tenantID, claim.ID, claim.LeaseToken).Row().Scan(&attempts); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if err := tx.Exec(`UPDATE transcription.audio_deletions SET status='FAILED',attempts=attempts+1,retry_at=now()+make_interval(secs => ?),lease_token=NULL,lease_deadline=NULL,updated_at=now()
WHERE tenant_id=? AND id=?`, deletionBackoff(attempts+1).Seconds(), tenantID, claim.ID).Error; err != nil {
			return err
		}
		// Expired audio stays reported as expired; other notes become cleanup-pending.
		if err := tx.Exec(`UPDATE transcription.audio_notes SET audio_deletion_status='FAILED',updated_at=now()
WHERE tenant_id=? AND id=? AND audio_deletion_status IN ('PENDING','SCHEDULED','PROCESSING')`, tenantID, claim.NoteID).Error; err != nil {
			return err
		}
		done = true
		return nil
	})
	return done, err
}

func lockNote(tx *gorm.DB, tenantID, noteID identity.ID) error {
	var locked identity.ID
	err := tx.Raw(`SELECT id FROM transcription.audio_notes WHERE tenant_id=? AND id=? FOR UPDATE`, tenantID, noteID).Row().Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("expire_audio: audio note not found")
	}
	return err
}
