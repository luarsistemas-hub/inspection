// Package schedule_expiry queues one retention.purge_due.v1 event per class
// and subject whose retention period elapsed: the whole inspection (evidence
// and reports), the capture location of an inspection (operational class) and
// the expired invitation/OTP/session secrets of a tenant (security class). It
// runs inside the tenant transaction so forced row-level security exposes only
// the tenant's rows.
package schedule_expiry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/tenanttx"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// DefaultBatchSize bounds how many due events one scheduler pass emits per
// tenant and class; the remainder is picked up by the next pass.
const DefaultBatchSize = 100

// DedupeWindow is how long a queued purge_due event suppresses a new one for
// the same subject. An inspection that did not purge (legal hold, extended
// policy, conflict) is queued again once the window passes.
const DedupeWindow = 24 * time.Hour

const (
	dueType = "retention.purge_due.v1"
	// DayLayout is the daily bucket of a deterministic event ID.
	DayLayout = "2006-01-02"
)

// EventID is the deterministic identity of the evidence purge_due event of one
// inspection on the UTC day of day, so replays and duplicate scheduler replicas
// within the day name the same event while the next day gets a fresh one that
// the consumer inbox does not mistake for a duplicate.
func EventID(tenantID, inspectionID identity.ID, day time.Time) identity.ID {
	return identity.ID(uuid.NewSHA1(uuid.Nil, []byte("retention:"+tenantID.String()+":"+inspectionID.String()+":"+day.UTC().Format(DayLayout))))
}

// OperationalEventID is EventID for the operational (location) class.
func OperationalEventID(tenantID, inspectionID identity.ID, day time.Time) identity.ID {
	return identity.ID(uuid.NewSHA1(uuid.Nil, []byte("retention-operational:"+tenantID.String()+":"+inspectionID.String()+":"+day.UTC().Format(DayLayout))))
}

// SecurityEventID is the daily event of the security (secrets) class of a tenant.
func SecurityEventID(tenantID identity.ID, day time.Time) identity.ID {
	return identity.ID(uuid.NewSHA1(uuid.Nil, []byte("retention-security:"+tenantID.String()+":"+day.UTC().Format(DayLayout))))
}

// CorrelationID is the correlation of the evidence purge_due event of an inspection.
func CorrelationID(inspectionID identity.ID) string { return "retention-" + inspectionID.String() }

// OperationalCorrelationID is the correlation of the operational purge_due event.
func OperationalCorrelationID(inspectionID identity.ID) string {
	return "retention-operational-" + inspectionID.String()
}

// SecurityCorrelationID is the correlation of the security purge_due event.
func SecurityCorrelationID(tenantID identity.ID) string {
	return "retention-security-" + tenantID.String()
}

// EmitDue queues purge_due events for up to limit due subjects per class of
// the tenant under its effective policy. Held, already purged and recently
// queued subjects are skipped. A subject whose event is older than DedupeWindow
// and that still is due is queued again, so work that did not complete is never
// orphaned. It returns how many events were queued.
func EmitDue(ctx context.Context, db *gorm.DB, tenantID identity.ID, now time.Time, limit int) (int, error) {
	if db == nil || tenantID == (identity.ID{}) {
		return 0, errors.New("retention schedule_expiry: missing dependency")
	}
	if limit <= 0 || limit > DefaultBatchSize {
		limit = DefaultBatchSize
	}
	now = now.UTC()
	emitted := 0
	err := (tenanttx.Runner{DB: db}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		tx = tx.WithContext(ctx)
		policy, err := retentioncore.LoadPolicy(ctx, tx, tenantID)
		if err != nil {
			return err
		}
		if err := policy.Validate(); err != nil {
			return fmt.Errorf("retention schedule_expiry: %w", err)
		}
		evidence, err := dueInspections(tx, tenantID, now, limit, policy.EvidenceReports, "retention-", false)
		if err != nil {
			return err
		}
		for _, inspectionID := range evidence {
			queued, err := queue(tx, events.Envelope[map[string]any]{ID: EventID(tenantID, inspectionID, now), Type: dueType, SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: inspectionID, CorrelationID: CorrelationID(inspectionID), Payload: map[string]any{"inspectionId": inspectionID, "class": retentioncore.EvidenceReports}})
			if err != nil {
				return err
			}
			emitted += queued
		}
		operational, err := dueInspections(tx, tenantID, now, limit, policy.Operational, "retention-operational-", true)
		if err != nil {
			return err
		}
		for _, inspectionID := range operational {
			queued, err := queue(tx, events.Envelope[map[string]any]{ID: OperationalEventID(tenantID, inspectionID, now), Type: dueType, SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: inspectionID, CorrelationID: OperationalCorrelationID(inspectionID), Payload: map[string]any{"inspectionId": inspectionID, "class": retentioncore.Operational}})
			if err != nil {
				return err
			}
			emitted += queued
		}
		secretsDue, err := secretsDue(tx, tenantID, now, policy)
		if err != nil {
			return err
		}
		if secretsDue {
			queued, err := queue(tx, events.Envelope[map[string]any]{ID: SecurityEventID(tenantID, now), Type: dueType, SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: tenantID, CorrelationID: SecurityCorrelationID(tenantID), Payload: map[string]any{"class": retentioncore.Secrets}})
			if err != nil {
				return err
			}
			emitted += queued
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return emitted, nil
}

// queue adds the event unless one with the same identity already exists, which
// a duplicate scheduler replica may have queued a moment earlier.
func queue(tx *gorm.DB, envelope events.Envelope[map[string]any]) (int, error) {
	added, err := messaging.AddOutboxOnce(tx, envelope)
	if err != nil || !added {
		return 0, err
	}
	return 1, nil
}

// dueInspections lists the terminal inspections whose class clock elapsed. The
// class period starts at the shared closure instant (retentioncore.ClosureSQL);
// legal holds, completed purges and events queued within DedupeWindow exclude
// an inspection. withLocation restricts the list to inspections that still
// store a capture location, which makes the operational job idempotent.
func dueInspections(tx *gorm.DB, tenantID identity.ID, now time.Time, limit int, period time.Duration, correlationPrefix string, withLocation bool) ([]identity.ID, error) {
	location := ""
	if withLocation {
		location = " AND EXISTS (SELECT 1 FROM inspections.responsibilities r JOIN media.media_objects m ON m.tenant_id = r.tenant_id AND m.responsibility_id = r.id WHERE r.tenant_id = i.tenant_id AND r.inspection_id = i.id AND " + retentioncore.LocationColumnsPresentSQL("m") + ")"
	}
	var rows []struct{ ID identity.ID }
	err := tx.Raw(`SELECT i.id AS id FROM inspections.inspections i
		WHERE i.tenant_id = @tenant AND i.status IN `+retentioncore.TerminalStatusesSQL+` AND `+retentioncore.ClosureSQL("i")+` <= @cutoff`+location+`
		AND NOT EXISTS (SELECT 1 FROM retention.legal_holds h WHERE h.tenant_id = i.tenant_id AND h.inspection_id = i.id AND h.active)
		AND NOT EXISTS (SELECT 1 FROM retention.purge_runs p WHERE p.tenant_id = i.tenant_id AND p.inspection_id = i.id)
		AND NOT EXISTS (SELECT 1 FROM messaging.outbox o WHERE o.tenant_id = i.tenant_id AND o.type = 'retention.purge_due.v1' AND o.correlation_id = @prefix || i.id::text AND o.created_at > @since)
		ORDER BY `+retentioncore.ClosureSQL("i")+`, i.id LIMIT @limit`,
		map[string]any{"tenant": tenantID, "cutoff": now.Add(-period), "prefix": correlationPrefix, "since": now.Add(-DedupeWindow), "limit": limit}).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	ids := make([]identity.ID, len(rows))
	for index, row := range rows {
		ids[index] = row.ID
	}
	return ids, nil
}

// secretsDue reports whether the tenant has invitation, OTP or session secrets
// past their own expiry plus the security grace period and no recent job.
func secretsDue(tx *gorm.DB, tenantID identity.ID, now time.Time, policy retentioncore.Policy) (bool, error) {
	var due bool
	err := tx.Raw(`SELECT (EXISTS (SELECT 1 FROM invitations.otp_challenges o WHERE o.tenant_id = @tenant AND o.expires_at <= @cutoff)
		OR EXISTS (SELECT 1 FROM invitations.external_sessions s WHERE s.tenant_id = @tenant AND s.expires_at <= @cutoff)
		OR EXISTS (SELECT 1 FROM invitations.invitations v WHERE v.tenant_id = @tenant AND v.secrets_purged_at IS NULL AND v.expires_at <= @cutoff))
		AND NOT EXISTS (SELECT 1 FROM messaging.outbox x WHERE x.tenant_id = @tenant AND x.type = 'retention.purge_due.v1' AND x.correlation_id = @correlation AND x.created_at > @since)`,
		map[string]any{"tenant": tenantID, "cutoff": retentioncore.SecretsCutoff(now, policy), "correlation": SecurityCorrelationID(tenantID), "since": now.Add(-DedupeWindow)}).Scan(&due).Error
	return due, err
}
