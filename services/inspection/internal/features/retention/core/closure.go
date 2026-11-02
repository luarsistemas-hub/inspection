package core

import (
	"context"
	"errors"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DefaultEvidenceDays and DefaultOperationalDays mirror DefaultPolicy in whole
// days; the scheduler discovery function embeds the same defaults in SQL.
const (
	DefaultEvidenceDays    = 1825
	DefaultOperationalDays = 365
)

// TerminalStatusesSQL lists the inspection states that close the inspection
// itself. Only terminal inspections can enter any retention clock.
const TerminalStatusesSQL = "('COMPLETED','CANCELED','INVALIDATED')"

// ClosureSQL returns the SQL expression that yields the instant the retention
// clock starts for the inspection row aliased alias (a row of
// inspections.inspections). NULL means the inspection is not in retention (yet).
//
//   - An inspection that is not terminal (TerminalStatusesSQL) has no clock, so
//     a stale event or request never purges work that is still open.
//   - An inspection that belongs to a project only enters retention when the
//     project is CLOSED. It counts from the later of projects.closed_at (set by
//     Close, cleared by Reopen, so a reopened project recalculates its future
//     eligibility) and the inspection's own closure, so an inspection completed
//     after the project closed never starts early. GREATEST sits inside the
//     subquery because it ignores NULLs: a project that is not CLOSED yields no row.
//   - A standalone inspection counts from its own closure. The terminal
//     transition is the last business write of an inspection, so
//     inspections.updated_at is the authoritative closure instant (there is no
//     dedicated closed_at column on inspections).
//
// This single fragment is shared by purge_data, process_deletion_requests,
// schedule_expiry and platform.discover_scheduler_tenants (migration 113).
func ClosureSQL(alias string) string {
	return "(CASE WHEN " + alias + ".status NOT IN " + TerminalStatusesSQL + " THEN NULL WHEN " + alias + ".project_id IS NULL THEN " + alias + ".updated_at ELSE " +
		"(SELECT GREATEST(COALESCE(p.closed_at, p.updated_at), " + alias + ".updated_at) FROM projects.projects p WHERE p.tenant_id=" + alias + ".tenant_id AND p.id=" + alias + ".project_id AND p.status='CLOSED') END)"
}

// LockForRetention locks the inspection (FOR UPDATE) and then its project (FOR
// SHARE), in the same inspection-then-project order used by the stage
// completion, so the closure read afterwards cannot be changed by a concurrent
// project Reopen or inspection update until the transaction ends.
func LockForRetention(ctx context.Context, tx *gorm.DB, tenantID, inspectionID identity.ID) error {
	tx = tx.WithContext(ctx)
	var inspection database.Inspection
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, inspectionID).First(&inspection).Error; err != nil {
		return err
	}
	if inspection.ProjectID == nil {
		return nil
	}
	var project database.Project
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("tenant_id=? AND id=?", tenantID, *inspection.ProjectID).First(&project).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return nil
}

// Closure resolves the authoritative closure instant of one inspection through
// ClosureSQL. The boolean is false while the inspection is outside retention
// (not terminal, in a project that is not CLOSED, or unknown).
func Closure(ctx context.Context, tx *gorm.DB, tenantID, inspectionID identity.ID) (time.Time, bool, error) {
	var rows []struct{ ClosedAt *time.Time }
	if err := tx.WithContext(ctx).Raw("SELECT "+ClosureSQL("i")+" AS closed_at FROM inspections.inspections i WHERE i.tenant_id=? AND i.id=?", tenantID, inspectionID).Scan(&rows).Error; err != nil {
		return time.Time{}, false, err
	}
	if len(rows) != 1 || rows[0].ClosedAt == nil {
		return time.Time{}, false, nil
	}
	return rows[0].ClosedAt.UTC(), true, nil
}

// LoadPolicy returns the effective policy of the tenant: the platform default
// overlaid by the highest saved policy version. Security is the grace period
// after a secret's own expiry (zero keeps the platform security deadline).
func LoadPolicy(ctx context.Context, tx *gorm.DB, tenantID identity.ID) (Policy, error) {
	policy := DefaultPolicy()
	var configured database.RetentionPolicy
	if err := tx.WithContext(ctx).Where("tenant_id=?", tenantID).Order("version DESC").First(&configured).Error; err == nil {
		policy.EvidenceReports = time.Duration(configured.EvidenceDays) * 24 * time.Hour
		policy.Operational = time.Duration(configured.OperationalDays) * 24 * time.Hour
		policy.Security = time.Duration(configured.SecurityDays) * 24 * time.Hour
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Policy{}, err
	}
	return policy, nil
}
