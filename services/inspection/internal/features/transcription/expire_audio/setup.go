// Package expire_audio runs the scheduler scans for temporary voice-note audio:
// lease recovery, 24-hour expiry, due retry wakeups and durable deletion of
// original and derivative audio objects. It never calls the transcription
// gateway; recovery only republishes work for the dedicated worker.
package expire_audio

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

const (
	// DefaultBatchSize bounds the rows each scan locks for one tenant run.
	DefaultBatchSize = 100
	// DefaultLease bounds one storage cleanup claim before another scan may retry it.
	DefaultLease = 2 * time.Minute
	// AudioRetention is the lifetime of audio without a durable result, counted
	// from the completed upload, or from the upload intent when it was abandoned.
	AudioRetention = 24 * time.Hour
	// MaxProcessingAttempts mirrors the worker's bounded automatic attempts.
	MaxProcessingAttempts = 3
	// RequestRedelivery is how long a published retry wakeup waits before a
	// scan republishes the same idempotent MSG01 for a job that is still due.
	RequestRedelivery = 5 * time.Minute
)

// Deletion is one fenced claim over a durable audio-deletion obligation.
type Deletion struct {
	ID, NoteID     identity.ID
	ObjectKey      string
	UploadID       string
	DerivativeKeys []string
	LeaseToken     identity.ID
}

// Repository persists the scan state transitions. Every method uses database
// time, locks rows with SKIP LOCKED and commits before any storage call.
type Repository interface {
	// DueTenants lists tenants with any due voice-note scan work.
	DueTenants(ctx context.Context, retention time.Duration) ([]identity.ID, error)
	// RecoverLeases moves expired PROCESSING jobs back to RETRY_WAIT, or to
	// FAILED when the attempt limit is exhausted without a staged result.
	RecoverLeases(ctx context.Context, tenantID identity.ID, limit int, retention time.Duration) (recovered, exhausted int, err error)
	// ExpireNotes expires abandoned or unfinished audio after the retention window.
	ExpireNotes(ctx context.Context, tenantID identity.ID, limit int, retention time.Duration) (int, error)
	// RequeueDue publishes one MSG01 wakeup per due RETRY_WAIT job attempt.
	RequeueDue(ctx context.Context, tenantID identity.ID, limit int, redelivery time.Duration) (int, error)
	// ClaimDeletions leases due obligations of notes already in a terminal state.
	ClaimDeletions(ctx context.Context, tenantID identity.ID, limit int, lease time.Duration) ([]Deletion, error)
	// CompleteDeletion records a successful deletion; false means the fence was lost.
	CompleteDeletion(ctx context.Context, tenantID identity.ID, claim Deletion) (bool, error)
	// FailDeletion counts a failed attempt and schedules a backoff; false means the fence was lost.
	FailDeletion(ctx context.Context, tenantID identity.ID, claim Deletion) (bool, error)
}

// ObjectStore removes private audio objects; missing objects count as removed.
type ObjectStore interface {
	DeleteUpload(ctx context.Context, key, uploadID string) error
	Delete(ctx context.Context, key string) error
}

// Recorder receives PII-free scan counters.
type Recorder interface {
	TranscriptionSweep(scan, outcome string, count int)
}

// Dependencies are the database and private object storage required by the
// scheduler scans. Within must establish tenant RLS scope for every transaction.
// Repository replaces the PostgreSQL adapter in tests.
type Dependencies struct {
	DB         *gorm.DB
	Store      ObjectStore
	Within     func(context.Context, identity.ID, func(*gorm.DB) error) error
	Lease      time.Duration
	Repository Repository
	Metrics    Recorder
	// Logf receives PII-free diagnostics such as the class of a storage failure.
	// It defaults to the standard logger.
	Logf func(format string, args ...any)
}

// Counts reports work performed by one tenant scan without including PII.
type Counts struct {
	// Recovered counts expired leases returned to RETRY_WAIT.
	Recovered int
	// Exhausted counts expired leases closed as FAILED at the attempt limit.
	Exhausted int
	// Expired counts notes whose temporary audio reached the retention bound.
	Expired int
	// Requeued counts MSG01 wakeups written to the outbox.
	Requeued int
	// Deleted counts deletion obligations completed in storage.
	Deleted int
	// Failed counts deletion attempts that failed and were rescheduled.
	Failed int
}

// Total returns the number of items changed by the scan.
func (c Counts) Total() int {
	return c.Recovered + c.Exhausted + c.Expired + c.Requeued + c.Deleted + c.Failed
}

// Executor performs bounded retention, retry, lease and deletion scans.
type Executor struct {
	repo    Repository
	store   ObjectStore
	lease   time.Duration
	metrics Recorder
	logf    func(format string, args ...any)
}

// Setup validates and composes the scheduler slice dependencies.
func Setup(deps Dependencies) (*Executor, error) {
	if deps.Store == nil {
		return nil, errors.New("expire_audio: private object storage is required")
	}
	if store, ok := deps.Store.(objectstore.Store); ok && (store.Client == nil || store.Bucket == "") {
		return nil, errors.New("expire_audio: private object storage is required")
	}
	if deps.Lease <= 0 {
		deps.Lease = DefaultLease
	}
	repo := deps.Repository
	if repo == nil {
		if deps.DB == nil {
			return nil, errors.New("expire_audio: database is required")
		}
		within := deps.Within
		if within == nil {
			within = (tenanttx.Runner{DB: deps.DB}).Within
		}
		repo = postgresRepository{db: deps.DB, within: within}
	}
	logf := deps.Logf
	if logf == nil {
		logf = log.Printf
	}
	return &Executor{repo: repo, store: deps.Store, lease: deps.Lease, metrics: deps.Metrics, logf: logf}, nil
}

// DueTenants lists tenants that currently have voice-note scan work.
func (e *Executor) DueTenants(ctx context.Context) ([]identity.ID, error) {
	return e.repo.DueTenants(ctx, AudioRetention)
}

// RunDue executes all due scans for one tenant. Lease recovery runs first so
// abandoned work can expire in the same pass; cleanup runs last so storage is
// only touched after every state transition is committed. The four scans are
// independent: a failing scan never prevents the others from running, so a
// persistent fault in one of them cannot postpone audio deletion. Errors are
// aggregated with errors.Join and the counts keep only committed work.
func (e *Executor) RunDue(ctx context.Context, tenantID identity.ID, limit int) (Counts, error) {
	if tenantID == (identity.ID{}) || limit <= 0 {
		return Counts{}, errors.New("expire_audio: invalid tenant or batch size")
	}
	counts := Counts{}
	var errs []error
	var err error
	if counts.Recovered, counts.Exhausted, err = e.repo.RecoverLeases(ctx, tenantID, limit, AudioRetention); err != nil {
		counts.Recovered, counts.Exhausted = 0, 0
		errs = append(errs, fmt.Errorf("lease recovery: %w", err))
	}
	e.record("lease_recovery", "recovered", counts.Recovered)
	e.record("lease_recovery", "exhausted", counts.Exhausted)
	if counts.Expired, err = e.repo.ExpireNotes(ctx, tenantID, limit, AudioRetention); err != nil {
		counts.Expired = 0
		errs = append(errs, fmt.Errorf("audio expiry: %w", err))
	}
	e.record("expiry", "expired", counts.Expired)
	if counts.Requeued, err = e.repo.RequeueDue(ctx, tenantID, limit, RequestRedelivery); err != nil {
		counts.Requeued = 0
		errs = append(errs, fmt.Errorf("retry wakeup: %w", err))
	}
	e.record("retry_wakeup", "requeued", counts.Requeued)
	// Cleanup keeps the deletions it finished before an error: they are committed.
	if counts.Deleted, counts.Failed, err = e.cleanup(ctx, tenantID, limit); err != nil {
		errs = append(errs, fmt.Errorf("audio cleanup: %w", err))
	}
	e.record("cleanup", "deleted", counts.Deleted)
	e.record("cleanup", "failed", counts.Failed)
	return counts, errors.Join(errs...)
}

func (e *Executor) cleanup(ctx context.Context, tenantID identity.ID, limit int) (int, int, error) {
	claims, err := e.repo.ClaimDeletions(ctx, tenantID, limit, e.lease)
	if err != nil {
		return 0, 0, err
	}
	deleted, failed := 0, 0
	for _, claim := range claims {
		if err := ctx.Err(); err != nil {
			return deleted, failed, err
		}
		deleteErr := e.deleteObjects(ctx, claim)
		if deleteErr == nil {
			ok, err := e.repo.CompleteDeletion(ctx, tenantID, claim)
			if err != nil {
				return deleted, failed, err
			}
			if ok {
				deleted++
			}
			continue
		}
		// Only the failure class is logged: provider messages and wrapped
		// errors may carry the object key, which must stay out of logs.
		e.logf("expire_audio: audio object deletion failed tenant=%s deletion=%s class=%s", tenantID, claim.ID, storageErrorClass(deleteErr))
		ok, err := e.repo.FailDeletion(ctx, tenantID, claim)
		if err != nil {
			return deleted, failed, err
		}
		if ok {
			failed++
		}
	}
	return deleted, failed, nil
}

// deleteObjects keeps storage calls inside half of the claim lease so a slow
// provider cannot outlive the fence that protects the obligation.
func (e *Executor) deleteObjects(ctx context.Context, claim Deletion) error {
	storageCtx, cancel := context.WithTimeout(ctx, e.lease/2)
	defer cancel()
	if err := e.store.DeleteUpload(storageCtx, claim.ObjectKey, claim.UploadID); err != nil {
		return err
	}
	for _, key := range claim.DerivativeKeys {
		if key == "" {
			continue
		}
		if err := e.store.Delete(storageCtx, key); err != nil {
			return err
		}
	}
	return nil
}

// storageErrorClass reduces a storage failure to a bounded, key-free label.
func storageErrorClass(err error) string {
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, objectstore.ErrDenied):
		return "denied"
	case errors.Is(err, objectstore.ErrInvalid):
		return "invalid"
	case errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	case errors.As(err, &netErr):
		return "network"
	default:
		return "unavailable"
	}
}

func (e *Executor) record(scan, outcome string, count int) {
	if e.metrics != nil && count > 0 {
		e.metrics.TranscriptionSweep(scan, outcome, count)
	}
}

// deletionBackoff spaces cleanup retries; attempts are unbounded because the
// obligation must eventually remove the audio.
func deletionBackoff(attempt int) time.Duration {
	switch {
	case attempt <= 1:
		return time.Minute
	case attempt == 2:
		return 5 * time.Minute
	case attempt == 3:
		return 15 * time.Minute
	default:
		return time.Hour
	}
}
