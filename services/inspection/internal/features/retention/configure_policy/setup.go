// Package configure_policy saves the tenant retention policy. Each save is a
// new immutable version; the highest version is the one effective policy that
// the scheduler, deletion processor and purge worker evaluate.
package configure_policy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

const (
	// MinDays is the shortest accepted evidence or operational period.
	MinDays = 1
	// MaxDays keeps every period inside time.Duration arithmetic (about 100
	// years) so a saved policy can always be evaluated by the purge worker.
	MaxDays = 36500
)

// AuditAction names the audit event recorded for every policy change.
const AuditAction = "retention.policy_configured"

// Input is one policy change requested by a tenant administrator. ActorID is
// the authenticated identity recorded in the audit event of the change.
type Input struct {
	TenantID                                    identity.ID
	ActorID                                     identity.ID
	EvidenceDays, OperationalDays, SecurityDays int
}

// Validate rejects negative, zero, or implausibly long periods before any
// write. SecurityDays may be zero, which keeps the platform security deadline.
func Validate(in Input) error {
	if in.TenantID == (identity.ID{}) {
		return apperror.New(apperror.InvalidInput, "tenantId", "Organização inválida.")
	}
	if in.EvidenceDays < MinDays || in.EvidenceDays > MaxDays {
		return apperror.New(apperror.InvalidInput, "evidenceDays", periodMessage(MinDays))
	}
	if in.OperationalDays < MinDays || in.OperationalDays > MaxDays {
		return apperror.New(apperror.InvalidInput, "operationalDays", periodMessage(MinDays))
	}
	if in.SecurityDays < 0 || in.SecurityDays > MaxDays {
		return apperror.New(apperror.InvalidInput, "securityDays", periodMessage(0))
	}
	return nil
}

func periodMessage(minimum int) string {
	return fmt.Sprintf("Informe um período de retenção entre %d e %d dias.", minimum, MaxDays)
}

// Save appends the next policy version for the tenant and audits the change
// (actor, version and values) in the same transaction. Concurrent saves for one
// tenant are serialized so versions stay unique and strictly increasing; older
// versions remain as the visible history of the policy. Saving the values of the
// latest version again is an idempotent no-op: it returns that version without
// a new version or audit event.
func Save(ctx context.Context, db *gorm.DB, in Input) (database.RetentionPolicy, error) {
	if db == nil {
		return database.RetentionPolicy{}, gorm.ErrInvalidDB
	}
	if err := Validate(in); err != nil {
		return database.RetentionPolicy{}, err
	}
	var row database.RetentionPolicy
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tx = tx.Session(&gorm.Session{NewDB: true})
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "retention-policy:"+in.TenantID.String()).Error; err != nil {
				return err
			}
		}
		version := int64(1)
		var previous database.RetentionPolicy
		if err := tx.Where("tenant_id=?", in.TenantID).Order("version DESC").Take(&previous).Error; err == nil {
			if previous.EvidenceDays == in.EvidenceDays && previous.OperationalDays == in.OperationalDays && previous.SecurityDays == in.SecurityDays {
				row = previous
				return nil
			}
			version = previous.Version + 1
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		now := time.Now().UTC()
		row = database.RetentionPolicy{ID: identity.NewID(), TenantID: in.TenantID, EvidenceDays: in.EvidenceDays, OperationalDays: in.OperationalDays, SecurityDays: in.SecurityDays, Version: version, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if tx.Dialector.Name() != "postgres" {
			// The audit schema exists only on PostgreSQL; the sqlite transport
			// fixtures attach no audit database.
			return nil
		}
		metadata, _ := requestctx.FromContext(ctx)
		correlation := metadata.CorrelationID
		if correlation == "" {
			correlation = "retention-policy-" + row.ID.String()
		}
		reason := fmt.Sprintf("version=%d evidenceDays=%d operationalDays=%d securityDays=%d", row.Version, row.EvidenceDays, row.OperationalDays, row.SecurityDays)
		return tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: in.TenantID, ActorID: in.ActorID, Action: AuditAction, TargetType: "retention_policy", TargetID: row.ID.String(), Outcome: "COMPLETED", Reason: reason, CorrelationID: correlation, OccurredAt: now}).Error
	})
	if err != nil {
		return database.RetentionPolicy{}, err
	}
	return row, nil
}
