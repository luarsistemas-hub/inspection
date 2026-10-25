// Package core executes inventory jobs with lease fencing and bounded retries.
package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultLease = 120 * time.Second
	maxAttempts  = 4
)

var ErrLeaseLost = errors.New("inventory job lease lost")

// Processor performs provider work outside a transaction, then uses fencedWrite
// for the brief transaction that persists the result.
type Processor interface {
	Process(context.Context, database.InventoryJob, func(func(*gorm.DB) error) error) error
	Failed(context.Context, *gorm.DB, database.InventoryJob, string) error
}

type completionHook interface {
	Succeeded(context.Context, *gorm.DB, database.InventoryJob) error
}

type Executor struct {
	DB, ClaimDB                 *gorm.DB
	Processor                   Processor
	Now                         func() time.Time
	LeaseFor, HeartbeatInterval time.Duration
}

type lease struct {
	job   database.InventoryJob
	token identity.ID
}

// RunDue claims and processes at most one due job.
func (e *Executor) RunDue(ctx context.Context) (bool, error) {
	if e == nil || e.DB == nil || e.ClaimDB == nil || e.Processor == nil {
		return false, fmt.Errorf("analysis/inventory: missing executor dependency")
	}
	now := e.now()
	r, found, err := e.reserve(ctx, now)
	if err != nil || !found {
		return found, err
	}
	workCtx, cancel := context.WithCancel(ctx)
	stopLease := e.maintainLease(workCtx, *r, cancel)
	processErr := e.Processor.Process(workCtx, r.job, func(write func(*gorm.DB) error) error { return e.fencedWrite(ctx, *r, write) })
	cancel()
	stopLease()
	if errors.Is(processErr, ErrLeaseLost) {
		return true, nil
	}
	if processErr != nil {
		if workCtx.Err() != nil && ctx.Err() == nil && errors.Is(processErr, context.Canceled) {
			// The heartbeat lost the lease and cancelled the work: another
			// executor owns the job now, so this is not a failed attempt.
			return true, nil
		}
		if err := e.fail(ctx, *r, processErr); err != nil && !errors.Is(err, ErrLeaseLost) {
			return true, err
		}
		return true, nil
	}
	if err := e.complete(ctx, *r); err != nil && !errors.Is(err, ErrLeaseLost) {
		return true, err
	}
	return true, nil
}

func (e *Executor) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}
func (e *Executor) leaseDuration() time.Duration {
	if e.LeaseFor > 0 {
		return e.LeaseFor
	}
	return defaultLease
}
func (e *Executor) heartbeatInterval() time.Duration {
	if e.HeartbeatInterval > 0 {
		return e.HeartbeatInterval
	}
	return 30 * time.Second
}

func (e *Executor) reserve(ctx context.Context, now time.Time) (*lease, bool, error) {
	var result *lease
	err := e.ClaimDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job database.InventoryJob
		err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("(status='PENDING' AND next_attempt_at<=?) OR (status='RUNNING' AND lease_until<=?)", now, now).Order("next_attempt_at ASC,created_at ASC,id ASC").Take(&job).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if job.Attempts >= maxAttempts {
			if err := setTenant(tx, job.TenantID); err != nil {
				return err
			}
			job.Status = "FAILED"
			code := "INVENTORY_ANALYSIS_FAILED"
			job.ErrorCode = &code
			updated := tx.Model(&database.InventoryJob{}).Where("tenant_id=? AND id=? AND status IN ('PENDING','RUNNING')", job.TenantID, job.ID).Updates(map[string]any{"status": job.Status, "error_code": job.ErrorCode, "lease_token": nil, "lease_until": nil, "updated_at": now})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return ErrLeaseLost
			}
			return e.Processor.Failed(ctx, tx, job, code)
		}
		token := identity.NewID()
		until := now.Add(e.leaseDuration())
		if err := tx.Model(&job).Updates(map[string]any{"status": "RUNNING", "attempts": gorm.Expr("attempts + 1"), "lease_token": token, "lease_until": until, "updated_at": now}).Error; err != nil {
			return err
		}
		job.Status, job.Attempts, job.LeaseToken, job.LeaseUntil = "RUNNING", job.Attempts+1, &token, &until
		result = &lease{job: job, token: token}
		return nil
	})
	return result, result != nil, err
}

func (e *Executor) maintainLease(ctx context.Context, r lease, cancel context.CancelFunc) func() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(e.heartbeatInterval())
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := e.now()
				var changed int64
				err := e.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
					if err := setTenant(tx, r.job.TenantID); err != nil {
						return err
					}
					result := tx.Model(&database.InventoryJob{}).Where("tenant_id=? AND id=? AND status='RUNNING' AND lease_token=?", r.job.TenantID, r.job.ID, r.token).Updates(map[string]any{"lease_until": now.Add(e.leaseDuration()), "updated_at": now})
					changed = result.RowsAffected
					return result.Error
				})
				if err != nil || changed != 1 {
					cancel()
					return
				}
			}
		}
	}()
	return func() { <-done }
}

func (e *Executor) fencedWrite(ctx context.Context, r lease, write func(*gorm.DB) error) error {
	return e.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := setTenant(tx, r.job.TenantID); err != nil {
			return err
		}
		var job database.InventoryJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=? AND status='RUNNING' AND lease_token=? AND lease_until> ?", r.job.TenantID, r.job.ID, r.token, e.now()).Take(&job).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrLeaseLost
			}
			return err
		}
		return write(tx)
	})
}

func (e *Executor) complete(ctx context.Context, r lease) error {
	now := e.now()
	return e.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := setTenant(tx, r.job.TenantID); err != nil {
			return err
		}
		var current database.InventoryJob
		if err := tx.Select("error_code").Where("tenant_id=? AND id=?", r.job.TenantID, r.job.ID).Take(&current).Error; err != nil {
			return err
		}
		updates := map[string]any{"status": "SUCCEEDED", "lease_token": nil, "lease_until": nil, "updated_at": now, "error_code": successfulErrorCode(current.ErrorCode)}
		result := tx.Model(&database.InventoryJob{}).Where("tenant_id=? AND id=? AND status='RUNNING' AND lease_token=?", r.job.TenantID, r.job.ID, r.token).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLeaseLost
		}
		if hook, ok := e.Processor.(completionHook); ok {
			return hook.Succeeded(ctx, tx, r.job)
		}
		return nil
	})
}

func (e *Executor) fail(ctx context.Context, r lease, cause error) error {
	now := e.now()
	code := safeJobError(cause)
	// Permanent input errors cannot succeed on retry: fail on the first attempt.
	if r.job.Attempts >= maxAttempts || errors.Is(cause, messaging.ErrPermanent) {
		err := e.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := setTenant(tx, r.job.TenantID); err != nil {
				return err
			}
			var job database.InventoryJob
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=? AND status='RUNNING' AND lease_token=?", r.job.TenantID, r.job.ID, r.token).Take(&job).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrLeaseLost
				}
				return err
			}
			if err := tx.Model(&job).Updates(map[string]any{"status": "FAILED", "error_code": code, "lease_token": nil, "lease_until": nil, "updated_at": now}).Error; err != nil {
				return err
			}
			return e.Processor.Failed(ctx, tx, r.job, code)
		})
		if errors.Is(err, ErrLeaseLost) {
			return nil
		}
		return err
	}
	delay := retryDelay(r.job.Attempts)
	return e.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := setTenant(tx, r.job.TenantID); err != nil {
			return err
		}
		result := tx.Model(&database.InventoryJob{}).Where("tenant_id=? AND id=? AND status='RUNNING' AND lease_token=?", r.job.TenantID, r.job.ID, r.token).Updates(map[string]any{"status": "PENDING", "next_attempt_at": now.Add(delay), "error_code": code, "lease_token": nil, "lease_until": nil, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLeaseLost
		}
		return nil
	})
}

// retryDelay is the backoff after a failed attempt: 30s·2^attempts, where
// attempts already counts the attempt that failed (UT-192).
func retryDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	return 30 * time.Second * time.Duration(1<<min(attempts, maxAttempts))
}

func successfulErrorCode(existing *string) any {
	if existing != nil && *existing == "TRUNCATED_ITEMS" {
		return *existing
	}
	return nil
}

func setTenant(tx *gorm.DB, tenantID identity.ID) error {
	if tx.Dialector.Name() == "sqlite" {
		return nil
	}
	return tx.Exec("SELECT set_config('app.tenant_id', ?, true)", tenantID.String()).Error
}

func safeJobError(err error) string {
	if coded, ok := err.(interface{ ErrorCode() string }); ok && coded.ErrorCode() != "" {
		return coded.ErrorCode()
	}
	return "INVENTORY_ANALYSIS_FAILED"
}

// LatestGeneration keeps, per kind and requirement, only the jobs of the most
// recent generation. Earlier generations remain stored for audit.
func LatestGeneration(jobs []database.InventoryJob) []database.InventoryJob {
	latest := map[string]int{}
	for _, job := range jobs {
		key := job.Kind + "\x00" + job.RequirementKey
		if job.Generation > latest[key] {
			latest[key] = job.Generation
		}
	}
	out := make([]database.InventoryJob, 0, len(jobs))
	for _, job := range jobs {
		if job.Generation == latest[job.Kind+"\x00"+job.RequirementKey] {
			out = append(out, job)
		}
	}
	return out
}
