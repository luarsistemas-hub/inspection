package process_note

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"

	"gorm.io/gorm"
)

// RepositoryDependencies configures the PostgreSQL processing adapter.
type RepositoryDependencies struct {
	DB     *gorm.DB
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
	Lease  time.Duration
	Now    func() time.Time
}

var (
	// ErrLeaseActive means another worker owns a valid processing lease.
	ErrLeaseActive = errors.New("transcription job has an active lease")
	// ErrNotClaimable means a job is terminal, delayed, or outside its retry window.
	ErrNotClaimable = errors.New("transcription job is not claimable")
	// ErrStaleLease means the job's fencing token or lease deadline is no longer valid.
	ErrStaleLease = errors.New("transcription lease is stale")
)

// NewRepository creates the fenced PostgreSQL repository used by the worker.
func NewRepository(deps RepositoryDependencies) (Repository, error) {
	if deps.DB == nil {
		return nil, errors.New("process_note: missing database")
	}
	if deps.Within == nil {
		deps.Within = workerTenantScope(deps.DB)
	}
	if deps.Lease <= 0 {
		deps.Lease = defaultLeaseDuration()
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	return postgresRepository{deps: deps}, nil
}

func defaultLeaseDuration() time.Duration {
	return requiredLeaseDuration(DefaultSilenceDetectorTimeout, llm.DefaultTranscriptionTimeout)
}

// requiredLeaseDuration sums every sequential stage of one attempt: probing,
// provider transport, audio download, pre-transport persistence, and the
// post-deadline margin reserved for staging a paid result.
func requiredLeaseDuration(detectorTimeout, gatewayTimeout time.Duration) time.Duration {
	if detectorTimeout <= 0 {
		detectorTimeout = DefaultSilenceDetectorTimeout
	}
	if gatewayTimeout <= 0 {
		gatewayTimeout = llm.DefaultTranscriptionTimeout
	}
	return detectorTimeout + gatewayTimeout + audioDownloadAllowance + persistenceTimeout + leaseSafetyMargin
}

type postgresRepository struct{ deps RepositoryDependencies }

type claimedJob struct {
	JobID, NoteID, TenantID, InspectionID      identity.ID
	Generation, Attempts, ProfileVersion       int
	ProfileKey, ProfileDigest, ObjectKey, MIME string
	DurationMS                                 int64
	Status                                     string
	LeaseToken                                 *identity.ID
	LeaseDeadline                              *time.Time
	RetryAt                                    *time.Time
}

func (r postgresRepository) Claim(ctx context.Context, jobID, tenantID identity.ID, generation int) (Job, error) {
	return r.claim(ctx, jobID, tenantID, generation, r.deps.Lease)
}

// ClaimWithLease uses the larger of the configured lease and the attempt's effective timeout budget.
func (r postgresRepository) ClaimWithLease(ctx context.Context, jobID, tenantID identity.ID, generation int, minimumLease time.Duration) (Job, error) {
	lease := r.deps.Lease
	if minimumLease > lease {
		lease = minimumLease
	}
	return r.claim(ctx, jobID, tenantID, generation, lease)
}

func (r postgresRepository) claim(ctx context.Context, jobID, tenantID identity.ID, generation int, lease time.Duration) (Job, error) {
	var result Job
	claimErr := error(nil)
	err := r.deps.Within(ctx, tenantID, func(tx *gorm.DB) error {
		var row claimedJob
		query := `SELECT j.id AS job_id,j.note_id,j.tenant_id,n.inspection_id,j.generation,j.attempts,j.profile_key,j.profile_version,j.profile_digest,j.status,j.lease_token,j.lease_deadline,j.retry_at,n.upload_key AS object_key,n.content_type AS mime,n.duration_ms FROM transcription.jobs j JOIN transcription.audio_notes n ON n.tenant_id=j.tenant_id AND n.id=j.note_id WHERE j.tenant_id=? AND j.id=? AND j.generation=?`
		if tx.Dialector.Name() == "postgres" {
			// PostgreSQL locks the listed relations in clause order. The note is
			// locked before the job, like the API commands and the scheduler scans,
			// so a claim cannot deadlock with them over the same pair of rows.
			query += " FOR UPDATE OF n,j"
		}
		if err := tx.Raw(query, tenantID, jobID, generation).Scan(&row).Error; err != nil {
			return err
		}
		if row.JobID == (identity.ID{}) {
			return errors.New("transcription job not found")
		}
		now, err := r.databaseNow(tx)
		if err != nil {
			return err
		}
		if row.Status == "PROCESSING" && row.LeaseDeadline != nil && row.LeaseDeadline.After(now) {
			claimErr = ErrLeaseActive
			return nil
		}
		if row.Status != "QUEUED" && !(row.Status == "RETRY_WAIT" && row.RetryAt != nil && !row.RetryAt.After(now)) && !(row.Status == "PROCESSING" && row.LeaseDeadline != nil && !row.LeaseDeadline.After(now)) {
			claimErr = ErrNotClaimable
			return nil
		}
		resumeStagedResult := false
		if row.Attempts >= 3 {
			var staged bool
			if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM transcription.transcript_revisions r JOIN usage.llm_calls c ON c.tenant_id=r.tenant_id AND c.call_id=r.call_id WHERE r.tenant_id=? AND r.note_id=? AND r.kind='MODEL' AND c.job_id=? AND c.replay_generation=? AND c.comparison_mode='TRANSCRIPTION')`, tenantID, row.NoteID, row.JobID, row.Generation).Row().Scan(&staged); err != nil {
				return err
			}
			if !staged {
				if err := r.terminalFailure(tx, row, "RETRY_LIMIT_EXCEEDED", now); err != nil {
					return err
				}
				claimErr = ErrNotClaimable
				return nil
			}
			resumeStagedResult = true
		}
		token := identity.NewID()
		deadline := now.Add(lease)
		attempts := row.Attempts + 1
		if resumeStagedResult {
			attempts = row.Attempts
		}
		update := tx.Exec(`UPDATE transcription.jobs SET status='PROCESSING',attempts=?,lease_token=?,lease_deadline=?,retry_at=NULL,updated_at=? WHERE tenant_id=? AND id=? AND generation=? AND attempts=?`, attempts, token, deadline, now, tenantID, jobID, generation, row.Attempts)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return errors.New("transcription claim lost")
		}
		noteUpdate := tx.Exec(`UPDATE transcription.audio_notes SET status='PROCESSING',version=version+1,updated_at=? WHERE tenant_id=? AND id=? AND status IN ('QUEUED','RETRY_WAIT','PROCESSING')`, now, tenantID, row.NoteID)
		if noteUpdate.Error != nil {
			return noteUpdate.Error
		}
		if noteUpdate.RowsAffected != 1 {
			return errors.New("transcription note is not processable")
		}
		profile, profileErr := llm.ResolveTranscriptionProfile(row.ProfileKey)
		if profileErr != nil || profile.Version != row.ProfileVersion {
			return errors.New("transcription profile unavailable")
		}
		result = Job{TenantID: tenantID, NoteID: row.NoteID, JobID: row.JobID, InspectionID: row.InspectionID, Generation: generation, Attempt: attempts, LeaseToken: token, LeaseDeadline: deadline, LeaseDuration: lease, Profile: profile, ProfileDigest: row.ProfileDigest, PromptDigest: row.ProfileDigest, ObjectKey: row.ObjectKey, MIME: row.MIME, DurationMS: row.DurationMS}
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, claimErr
}

func (r postgresRepository) databaseNow(tx *gorm.DB) (time.Time, error) {
	if tx.Dialector.Name() == "postgres" {
		var now time.Time
		if err := tx.Raw("SELECT now()").Row().Scan(&now); err != nil {
			return time.Time{}, err
		}
		return now.UTC(), nil
	}
	return r.deps.Now(), nil
}

func (r postgresRepository) Complete(ctx context.Context, job Job, callID, transcript string, noSpeech bool) error {
	if !noSpeech && strings.TrimSpace(transcript) == "" {
		return errors.New("completed transcription has no text")
	}
	return r.deps.Within(ctx, job.TenantID, func(tx *gorm.DB) error {
		now, err := r.databaseNow(tx)
		if err != nil {
			return err
		}
		var current identity.ID
		var note struct{ CurrentRevisionID *identity.ID }
		if err := tx.Raw(`SELECT current_revision_id FROM transcription.audio_notes WHERE tenant_id=? AND id=? FOR UPDATE`, job.TenantID, job.NoteID).Scan(&note).Error; err != nil {
			return err
		}
		if note.CurrentRevisionID != nil {
			current = *note.CurrentRevisionID
		}
		if err := r.checkFence(tx, job, "PROCESSING"); err != nil {
			return err
		}
		revisionID := identity.ID{}
		if !noSpeech && strings.TrimSpace(transcript) != "" {
			call, err := identity.ParseID(callID)
			if err != nil {
				return fmt.Errorf("invalid model call id: %w", err)
			}
			var staged struct {
				ID   identity.ID
				Text string
			}
			err = tx.Raw(`SELECT id,text FROM transcription.transcript_revisions WHERE tenant_id=? AND note_id=? AND call_id=?`, job.TenantID, job.NoteID, call).Row().Scan(&staged.ID, &staged.Text)
			if err == nil {
				if staged.Text != transcript {
					return errors.New("staged transcription result mismatch")
				}
				revisionID = staged.ID
			} else if errors.Is(err, sql.ErrNoRows) {
				var latest int
				if err := tx.Raw(`SELECT COALESCE(MAX(revision_number),0) FROM transcription.transcript_revisions WHERE tenant_id=? AND note_id=?`, job.TenantID, job.NoteID).Row().Scan(&latest); err != nil {
					return err
				}
				revisionID = identity.NewID()
				revision := database.TranscriptRevision{ID: revisionID, TenantID: job.TenantID, NoteID: job.NoteID, RevisionNumber: latest + 1, Text: transcript, Kind: "MODEL", CallID: &call, CreatedAt: now}
				created := tx.Create(&revision)
				if created.Error != nil {
					return created.Error
				}
				if created.RowsAffected != 1 {
					return errors.New("transcription revision insert lost")
				}
			} else {
				return err
			}
		}
		var edited int64
		if err := tx.Table("transcription.transcript_revisions").Where("tenant_id=? AND note_id=? AND kind IN ('PARTICIPANT_DECLARATION','HUMAN_EDIT')", job.TenantID, job.NoteID).Count(&edited).Error; err != nil {
			return err
		}
		var revisionForEvent *identity.ID
		if edited > 0 && current != (identity.ID{}) {
			revisionForEvent = &current
		} else if revisionID != (identity.ID{}) {
			revisionForEvent = &revisionID
		} else if current != (identity.ID{}) {
			revisionForEvent = &current
		}
		updates := map[string]any{"status": "COMPLETED", "no_speech": noSpeech, "safe_failure_code": nil, "audio_deletion_status": "SCHEDULED", "version": gorm.Expr("version+1"), "updated_at": now}
		if edited == 0 && revisionID != (identity.ID{}) {
			updates["current_revision_id"] = revisionID
		}
		updated := tx.Model(&audioNoteProjection{}).Where("tenant_id=? AND id=? AND status='PROCESSING'", job.TenantID, job.NoteID).Updates(updates)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return errors.New("transcription note completion lost")
		}
		jobUpdate := tx.Exec(`UPDATE transcription.jobs SET status='COMPLETED',lease_token=NULL,lease_deadline=NULL,retry_at=NULL,safe_failure_code=NULL,updated_at=? WHERE tenant_id=? AND id=? AND generation=? AND status='PROCESSING' AND lease_token=?`, now, job.TenantID, job.JobID, job.Generation, job.LeaseToken)
		if jobUpdate.Error != nil {
			return jobUpdate.Error
		}
		if jobUpdate.RowsAffected != 1 {
			return errors.New("transcription completion fence lost")
		}
		deletion := tx.Exec(`INSERT INTO transcription.audio_deletions(tenant_id,id,note_id,object_generation,object_key,derivative_keys,status,created_at,updated_at) VALUES(?,?,?,?,?,'[]'::jsonb,'PENDING',?,?) ON CONFLICT(tenant_id,note_id,object_generation) DO NOTHING`, job.TenantID, identity.NewID(), job.NoteID, 1, job.ObjectKey, now, now)
		if deletion.Error != nil {
			return deletion.Error
		}
		return addTranscriptionOutbox(tx, job, "transcription.completed.v1", events.TranscriptionCompletedPayload{NoteID: job.NoteID, RevisionID: revisionForEvent, Generation: job.Generation, NoSpeech: noSpeech}, now)
	})
}

func (r postgresRepository) StageResult(ctx context.Context, job Job, callID, transcript string) error {
	if strings.TrimSpace(transcript) == "" || len([]rune(transcript)) > 12000 {
		return errors.New("invalid staged transcription result")
	}
	return r.deps.Within(ctx, job.TenantID, func(tx *gorm.DB) error {
		now, err := r.databaseNow(tx)
		if err != nil {
			return err
		}
		if err := r.checkFence(tx, job, "PROCESSING"); err != nil {
			return err
		}
		var existing identity.ID
		err = tx.Raw(`SELECT id FROM transcription.transcript_revisions WHERE tenant_id=? AND note_id=? AND call_id=?`, job.TenantID, job.NoteID, callID).Row().Scan(&existing)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var latest int
		if err := tx.Raw(`SELECT COALESCE(MAX(revision_number),0) FROM transcription.transcript_revisions WHERE tenant_id=? AND note_id=?`, job.TenantID, job.NoteID).Row().Scan(&latest); err != nil {
			return err
		}
		call, err := identity.ParseID(callID)
		if err != nil {
			return err
		}
		revision := database.TranscriptRevision{ID: identity.NewID(), TenantID: job.TenantID, NoteID: job.NoteID, RevisionNumber: latest + 1, Text: transcript, Kind: "MODEL", CallID: &call, CreatedAt: now}
		return tx.Create(&revision).Error
	})
}

func (r postgresRepository) StagedResult(ctx context.Context, job Job) (string, string, error) {
	var callID, transcript string
	err := r.deps.Within(ctx, job.TenantID, func(tx *gorm.DB) error {
		return tx.Raw(`SELECT r.call_id,r.text FROM transcription.transcript_revisions r JOIN usage.llm_calls c ON c.tenant_id=r.tenant_id AND c.call_id=r.call_id WHERE r.tenant_id=? AND r.note_id=? AND r.kind='MODEL' AND c.job_id=? AND c.replay_generation=? AND c.comparison_mode='TRANSCRIPTION' ORDER BY r.revision_number DESC LIMIT 1`, job.TenantID, job.NoteID, job.JobID, job.Generation).Row().Scan(&callID, &transcript)
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", nil
		}
		return "", "", err
	}
	return callID, transcript, nil
}

// audioNoteProjection targets the existing audio_notes table without widening the shared model.
type audioNoteProjection struct{}

func (audioNoteProjection) TableName() string { return "transcription.audio_notes" }

func (r postgresRepository) Fail(ctx context.Context, job Job, safeCode string, uncertain bool) error {
	if !validSafeFailureCode(safeCode) {
		return errors.New("invalid safe transcription failure code")
	}
	return r.deps.Within(ctx, job.TenantID, func(tx *gorm.DB) error {
		now, err := r.databaseNow(tx)
		if err != nil {
			return err
		}
		if err := r.checkFence(tx, job, "PROCESSING"); err != nil {
			return err
		}
		var attempts int
		if err := tx.Raw(`SELECT attempts FROM transcription.jobs WHERE tenant_id=? AND id=? AND generation=?`, job.TenantID, job.JobID, job.Generation).Row().Scan(&attempts); err != nil {
			return err
		}
		retryable := safeCode == "TRANSCRIPTION_FAILED" || safeCode == "SILENCE_DETECTOR_TIMEOUT" || safeCode == "AUDIO_UNAVAILABLE" || safeCode == "USAGE_UNAVAILABLE"
		retry := !uncertain && attempts < 3 && retryable
		status, noteStatus := "FAILED", "FAILED"
		var retryAt any
		if retry {
			status, noteStatus = "RETRY_WAIT", "RETRY_WAIT"
			retryAt = now.Add(backoff(attempts))
		}
		update := tx.Exec(`UPDATE transcription.jobs SET status=?,retry_at=?,safe_failure_code=?,lease_token=NULL,lease_deadline=NULL,updated_at=? WHERE tenant_id=? AND id=? AND generation=? AND status='PROCESSING' AND lease_token=?`, status, retryAt, safeCode, now, job.TenantID, job.JobID, job.Generation, job.LeaseToken)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return errors.New("transcription failure fence lost")
		}
		note := tx.Exec(`UPDATE transcription.audio_notes SET status=?,safe_failure_code=?,version=version+1,updated_at=? WHERE tenant_id=? AND id=? AND status='PROCESSING'`, noteStatus, safeCode, now, job.TenantID, job.NoteID)
		if note.Error != nil {
			return note.Error
		}
		if note.RowsAffected != 1 {
			return errors.New("transcription failure note update lost")
		}
		return addTranscriptionOutbox(tx, job, "transcription.failed.v1", events.TranscriptionFailedPayload{NoteID: job.NoteID, Generation: job.Generation, SafeCode: safeCode}, now)
	})
}

func (r postgresRepository) checkFence(tx *gorm.DB, job Job, status string) error {
	var found identity.ID
	now, err := r.databaseNow(tx)
	if err != nil {
		return err
	}
	if err := tx.Raw(`SELECT id FROM transcription.jobs WHERE tenant_id=? AND id=? AND generation=? AND status=? AND lease_token=? AND lease_deadline>? FOR UPDATE`, job.TenantID, job.JobID, job.Generation, status, job.LeaseToken, now).Row().Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStaleLease
		}
		return err
	}
	if found != job.JobID {
		return ErrStaleLease
	}
	return nil
}

func (r postgresRepository) terminalFailure(tx *gorm.DB, row claimedJob, safeCode string, now time.Time) error {
	jobUpdate := tx.Exec(`UPDATE transcription.jobs SET status='FAILED',retry_at=NULL,safe_failure_code=?,lease_token=NULL,lease_deadline=NULL,updated_at=? WHERE tenant_id=? AND id=? AND generation=?`, safeCode, now, row.TenantID, row.JobID, row.Generation)
	if jobUpdate.Error != nil {
		return jobUpdate.Error
	}
	noteUpdate := tx.Exec(`UPDATE transcription.audio_notes SET status='FAILED',safe_failure_code=?,version=version+1,updated_at=? WHERE tenant_id=? AND id=? AND status IN ('QUEUED','RETRY_WAIT','PROCESSING')`, safeCode, now, row.TenantID, row.NoteID)
	if noteUpdate.Error != nil {
		return noteUpdate.Error
	}
	job := Job{TenantID: row.TenantID, NoteID: row.NoteID, JobID: row.JobID, Generation: row.Generation}
	return addTranscriptionOutbox(tx, job, "transcription.failed.v1", events.TranscriptionFailedPayload{NoteID: row.NoteID, Generation: row.Generation, SafeCode: safeCode}, now)
}

func addTranscriptionOutbox(tx *gorm.DB, job Job, typ string, payload any, now time.Time) error {
	eventID := identity.NewID()
	envelope := events.Envelope[any]{ID: eventID, Type: typ, SchemaVersion: 1, OccurredAt: now, TenantID: job.TenantID, AggregateID: job.NoteID, CorrelationID: job.NoteID.String(), Payload: payload}
	data, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	if _, err := events.DefaultRegistry().Validate(data); err != nil {
		return fmt.Errorf("invalid transcription event contract: %w", err)
	}
	result := tx.Create(&database.OutboxIntent{ID: envelope.ID, TenantID: envelope.TenantID, Type: envelope.Type, SchemaVersion: envelope.SchemaVersion, Payload: data, CorrelationID: envelope.CorrelationID, Status: "PENDING", NextAttemptAt: now, CreatedAt: now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("transcription outbox insert lost")
	}
	return nil
}

func validSafeFailureCode(code string) bool {
	if code == "" || len(code) > 64 {
		return false
	}
	for _, ch := range code {
		if !(ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_') {
			return false
		}
	}
	return true
}

func backoff(attempt int) time.Duration {
	var base time.Duration
	if attempt <= 1 {
		base = 10 * time.Second
	} else {
		base = 30 * time.Second
	}
	return base + time.Duration(rand.Int63n(int64(base/4)))
}

func workerTenantScope(db *gorm.DB) func(context.Context, identity.ID, func(*gorm.DB) error) error {
	return func(ctx context.Context, tenantID identity.ID, fn func(*gorm.DB) error) error {
		if tenantID == (identity.ID{}) {
			return errors.New("missing tenant")
		}
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if tx.Dialector.Name() == "postgres" {
				if err := tx.Exec(`SELECT set_config('app.tenant_id', ?, true)`, tenantID.String()).Error; err != nil {
					return err
				}
			}
			return fn(tx.WithContext(ctx))
		})
	}
}
