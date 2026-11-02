// Package publication owns the audited report visibility ledger: policy
// configuration, publication with supersession, and invalidation.
package publication

import (
	"context"
	"errors"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	publicationcore "inspection/services/inspection/internal/features/reports/publication/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	auditTargetType        = "REPORT_PUBLICATION"
	auditPublished         = "report.published"
	auditSuperseded        = "report.publication_superseded"
	auditInvalidated       = "report.publication_invalidated"
	auditOutcome           = "SUCCESS"
	maxInvalidationReason  = 2000
	eventPublished         = "report.published.v1"
	eventPublicationVoided = "report.publication_invalidated.v1"
)

// Service coordinates publication use cases over the tenant-scoped database.
type Service struct {
	DB  *gorm.DB
	Now func() time.Time
}

// ConfigureInput changes the tenant publication policy.
type ConfigureInput struct {
	TenantID        identity.ID
	Mode            string
	ExpectedVersion int64
}

// PublishInput releases one final snapshot to customers. CorrelationID is
// optional; it ties the audit trail and outbox event to the originating request.
type PublishInput struct {
	TenantID, InspectionID, SnapshotID, ActorID identity.ID
	ClientMutationID                            string
	CorrelationID                               string
	Final                                       bool
}

// InvalidateInput withdraws a published report from customers.
type InvalidateInput struct {
	TenantID, PublicationID, ActorID identity.ID
	Reason                           string
	ExpectedVersion                  int64
	CorrelationID                    string
}

func (s Service) clock() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Configure stores the tenant policy under optimistic versioning.
func (s Service) Configure(ctx context.Context, in ConfigureInput) (database.PublicationPolicy, error) {
	if s.DB == nil || in.TenantID == (identity.ID{}) {
		return database.PublicationPolicy{}, apperror.New(apperror.InvalidInput, "tenantId", "Organização não informada.")
	}
	var out database.PublicationPolicy
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		var row database.PublicationPolicy
		err := tx.Where("tenant_id = ?", in.TenantID).First(&row).Error
		current := int64(0)
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		if err == nil {
			current = row.Version
		}
		policy, err := publicationcore.Configure(in.Mode, in.ExpectedVersion, current)
		if err != nil {
			return err
		}
		if row.ID == (identity.ID{}) {
			row.ID = identity.NewID()
			row.TenantID = in.TenantID
			row.CreatedAt = s.clock()
		}
		row.Mode = policy.Mode
		row.Version = policy.Version
		row.UpdatedAt = s.clock()
		out = row
		return tx.Save(&row).Error
	})
	return out, err
}

// Publish releases a snapshot in its own tenant transaction.
func (s Service) Publish(ctx context.Context, in PublishInput) (database.ReportPublication, error) {
	if s.DB == nil || in.TenantID == (identity.ID{}) || in.SnapshotID == (identity.ID{}) || strings.TrimSpace(in.ClientMutationID) == "" {
		return database.ReportPublication{}, apperror.New(apperror.InvalidInput, "clientMutationId", "Dados da publicação são obrigatórios.")
	}
	if in.CorrelationID == "" {
		if meta, ok := requestctx.FromContext(ctx); ok {
			in.CorrelationID = meta.CorrelationID
		}
	}
	var out database.ReportPublication
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		return tx.Transaction(func(tx *gorm.DB) error {
			published, err := PublishTx(ctx, tx, in, s.clock())
			out = published
			return err
		})
	})
	return out, err
}

// PublishTx releases a snapshot inside the caller's tenant-scoped transaction,
// so report finalization can publish atomically with the snapshot. Repeating a
// mutation key or publishing an already released snapshot returns the existing
// publication without a second audit record or event. A previous active
// publication of the same inspection becomes SUPERSEDED.
func PublishTx(ctx context.Context, tx *gorm.DB, in PublishInput, now time.Time) (database.ReportPublication, error) {
	var out database.ReportPublication
	if tx == nil || in.TenantID == (identity.ID{}) || in.SnapshotID == (identity.ID{}) || strings.TrimSpace(in.ClientMutationID) == "" {
		return out, apperror.New(apperror.InvalidInput, "clientMutationId", "Dados da publicação são obrigatórios.")
	}
	tx = tx.WithContext(ctx)
	now = now.UTC()
	var snap database.ReportSnapshot
	if err := tx.Where("tenant_id=? AND id=? AND inspection_id=?", in.TenantID, in.SnapshotID, in.InspectionID).First(&snap).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return out, apperror.New(apperror.NotFound, "snapshotId", "Laudo não encontrado.")
	} else if err != nil {
		return out, err
	}
	// Serialize releases of this inspection before any idempotency or state
	// check, so a concurrent release is observed instead of raced.
	if err := lockInspectionPublications(tx, in.TenantID, snap.InspectionID); err != nil {
		return database.ReportPublication{}, err
	}
	if err := tx.Where("tenant_id=? AND client_mutation_id=?", in.TenantID, in.ClientMutationID).First(&out).Error; err == nil {
		// A mutation key identifies one release; reusing it for another
		// snapshot must not answer with the unrelated earlier publication.
		if out.SnapshotID != in.SnapshotID {
			return database.ReportPublication{}, apperror.New(apperror.Conflict, "clientMutationId", "Esta chave de mutação já foi usada para publicar outro laudo.")
		}
		return out, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, err
	}
	var prior database.ReportPublication
	if err := tx.Where("tenant_id=? AND snapshot_id=?", in.TenantID, in.SnapshotID).First(&prior).Error; err == nil {
		if prior.Status != publicationcore.Published {
			return database.ReportPublication{}, publicationcore.CanPublish(in.Final, prior.Status)
		}
		// A snapshot already released remains the authoritative release for
		// this version; a different mutation key must not create a duplicate.
		return prior, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, err
	}
	if err := publicationcore.CanPublish(in.Final, ""); err != nil {
		return database.ReportPublication{}, err
	}
	actor := in.ActorID
	if actor == (identity.ID{}) {
		actor = in.TenantID
	}
	correlation := in.CorrelationID
	if correlation == "" {
		correlation = "publication:" + in.SnapshotID.String()
	}
	out = database.ReportPublication{ID: identity.NewID(), TenantID: in.TenantID, SnapshotID: snap.ID, InspectionID: snap.InspectionID, PolicyVersion: snap.PublicationPolicyVersion, ClientMutationID: in.ClientMutationID, Status: publicationcore.Published, ActorID: actor, Version: 1, PublishedAt: &now, CreatedAt: now, UpdatedAt: now}
	var previous database.ReportPublication
	previousQuery := lockForUpdate(tx.Where("tenant_id=? AND inspection_id=? AND status=?", in.TenantID, snap.InspectionID, publicationcore.Published))
	if err := previousQuery.First(&previous).Error; err == nil {
		// Only the supersession columns are written, guarded by the version and
		// status that were just read under lock, so a concurrent invalidation
		// can never be overwritten with stale values.
		update := tx.Model(&database.ReportPublication{}).
			Where("tenant_id=? AND id=? AND version=? AND status=?", in.TenantID, previous.ID, previous.Version, publicationcore.Published).
			Updates(map[string]any{"status": publicationcore.Superseded, "version": previous.Version + 1, "superseded_by": out.ID, "updated_at": now})
		if update.Error != nil {
			return database.ReportPublication{}, update.Error
		}
		if update.RowsAffected != 1 {
			return database.ReportPublication{}, apperror.New(apperror.Conflict, "publication", "A publicação ativa foi alterada por outra operação; tente novamente.")
		}
		if err := recordAudit(tx, in.TenantID, actor, auditSuperseded, previous.ID, "", correlation, now); err != nil {
			return database.ReportPublication{}, err
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return database.ReportPublication{}, err
	}
	if err := tx.Create(&out).Error; err != nil {
		return database.ReportPublication{}, err
	}
	if err := recordAudit(tx, in.TenantID, actor, auditPublished, out.ID, "", correlation, now); err != nil {
		return database.ReportPublication{}, err
	}
	if err := emit(tx, eventPublished, out, correlation, now, events.ReportPublishedPayload{PublicationID: out.ID, SnapshotID: out.SnapshotID, InspectionID: out.InspectionID}); err != nil {
		return database.ReportPublication{}, err
	}
	return out, nil
}

// Invalidate withdraws a publication from customers. It records the reason and
// actor, is idempotent for an already invalidated publication, and emits one
// outbox event only for the actual transition.
func (s Service) Invalidate(ctx context.Context, in InvalidateInput) (database.ReportPublication, error) {
	var out database.ReportPublication
	if s.DB == nil {
		return out, apperror.New(apperror.Internal, "", "Erro interno. Tente novamente.")
	}
	if in.TenantID == (identity.ID{}) || in.PublicationID == (identity.ID{}) {
		return out, apperror.New(apperror.InvalidInput, "publicationId", "Publicação não informada.")
	}
	if len([]rune(strings.TrimSpace(in.Reason))) > maxInvalidationReason {
		return out, apperror.New(apperror.InvalidInput, "reason", "O motivo deve ter no máximo 2000 caracteres.")
	}
	if in.CorrelationID == "" {
		if meta, ok := requestctx.FromContext(ctx); ok {
			in.CorrelationID = meta.CorrelationID
		}
	}
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		tx = tx.WithContext(ctx)
		// Lock order is inspection, then publication, the same as PublishTx, so a
		// release and an invalidation of one inspection serialize instead of
		// overwriting each other. The unlocked read only resolves the inspection.
		var located database.ReportPublication
		if err := tx.Select("id", "inspection_id").Where("tenant_id=? AND id=?", in.TenantID, in.PublicationID).Take(&located).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return apperror.New(apperror.NotFound, "publicationId", "Publicação não encontrada.")
		} else if err != nil {
			return err
		}
		if err := lockInspectionPublications(tx, in.TenantID, located.InspectionID); err != nil {
			return err
		}
		if err := lockForUpdate(tx.Where("tenant_id=? AND id=?", in.TenantID, in.PublicationID)).First(&out).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return apperror.New(apperror.NotFound, "publicationId", "Publicação não encontrada.")
		} else if err != nil {
			return err
		}
		status, at, err := publicationcore.Invalidate(out.Status, in.Reason, in.ExpectedVersion, out.Version, s.clock())
		if err != nil {
			return err
		}
		if status == out.Status {
			return nil
		}
		actor := in.ActorID
		if actor == (identity.ID{}) {
			actor = in.TenantID
		}
		correlation := in.CorrelationID
		if correlation == "" {
			correlation = "publication:" + out.ID.String()
		}
		now := s.clock()
		reason := strings.TrimSpace(in.Reason)
		update := tx.Model(&database.ReportPublication{}).
			Where("tenant_id=? AND id=? AND version=?", in.TenantID, out.ID, out.Version).
			Updates(map[string]any{"status": status, "invalidated_at": at, "invalidated_by": actor, "reason": reason, "version": out.Version + 1, "updated_at": now})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "publication", "A publicação foi alterada por outra operação; tente novamente.")
		}
		out.Status = status
		out.InvalidatedAt = at
		out.InvalidatedBy = &actor
		out.Reason = reason
		out.Version++
		out.UpdatedAt = now
		if err := recordAudit(tx, in.TenantID, actor, auditInvalidated, out.ID, reason, correlation, now); err != nil {
			return err
		}
		return emit(tx, eventPublicationVoided, out, correlation, now, events.ReportPublicationInvalidatedPayload{PublicationID: out.ID, SnapshotID: out.SnapshotID, InspectionID: out.InspectionID})
	})
	return out, err
}

// lockForUpdate adds FOR UPDATE to a ledger query on PostgreSQL. SQLite (unit
// tests) has a single writer and no row locks.
func lockForUpdate(query *gorm.DB) *gorm.DB {
	if query.Dialector.Name() != "postgres" {
		return query
	}
	return query.Clauses(clause.Locking{Strength: "UPDATE"})
}

// lockInspectionPublications serializes publications and invalidations of one
// inspection so the single-active-publication invariant yields supersession,
// not a unique-index failure, under concurrent releases. It takes FOR NO KEY
// UPDATE, which excludes other publishers and the FOR UPDATE of the state
// handlers. Finalization publishes inside the consumer's own transaction (see
// generate_snapshot.CreateInTx), so the lock is taken by the transaction that
// already holds the inspection and never waits on a lock held by its caller.
func lockInspectionPublications(tx *gorm.DB, tenantID, inspectionID identity.ID) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	var inspection database.Inspection
	err := tx.Clauses(clause.Locking{Strength: "NO KEY UPDATE"}).Select("id").Where("tenant_id=? AND id=?", tenantID, inspectionID).Take(&inspection).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

func recordAudit(tx *gorm.DB, tenantID, actorID identity.ID, action string, publicationID identity.ID, reason, correlationID string, at time.Time) error {
	return tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: tenantID, ActorID: actorID, Action: action, TargetType: auditTargetType, TargetID: publicationID.String(), Outcome: auditOutcome, Reason: reason, CorrelationID: correlationID, OccurredAt: at}).Error
}

func emit[T any](tx *gorm.DB, eventType string, row database.ReportPublication, correlationID string, at time.Time, payload T) error {
	return messaging.AddOutbox(tx, events.Envelope[T]{ID: identity.NewID(), Type: eventType, SchemaVersion: 1, OccurredAt: at, TenantID: row.TenantID, AggregateID: row.InspectionID, CorrelationID: correlationID, Payload: payload})
}
