// Package process_inventory_job runs task-keyed catalog and comparison calls.
package process_inventory_job

import (
	"context"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	inventoryexecutor "inspection/services/inspection/internal/features/analysis/inventory/core"
	"inspection/services/inspection/internal/features/analysis/prompt"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/observability"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Dependencies are the adapters used by inventory job execution.
type Dependencies struct {
	DB      *gorm.DB
	Store   objectstore.Store
	Gateway llm.Gateway
	Now     func() time.Time
	// Within opens the tenant-scoped read transaction. It defaults to the
	// runtime-role tenant runner over DB.
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
}

// Processor executes one leased inventory job. It reads its input in a short
// tenant transaction, calls the gateway with no transaction open and persists
// the result through the executor's fenced write.
type Processor struct{ deps Dependencies }

// Setup validates the processor dependencies.
func Setup(deps Dependencies) (*Processor, error) {
	if deps.DB == nil || deps.Store.Client == nil || deps.Gateway == nil {
		return nil, fmt.Errorf("analysis/process_inventory_job: missing dependency")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Processor{deps: deps}, nil
}

// Process runs a CATALOG or COMPARE job.
func (p *Processor) Process(ctx context.Context, job database.InventoryJob, fenced func(func(*gorm.DB) error) error) error {
	ctx = withJobCorrelation(ctx, job)
	in, err := p.load(ctx, job)
	if err != nil {
		return err
	}
	// Persist the capture version before any fallible media/model work. A
	// terminal failure still needs to identify which submitted room state it
	// attempted, so a later pairing revision can detect a newer recapture.
	if job.Kind == "COMPARE" && in.strict {
		if err := fenced(func(tx *gorm.DB) error { return recordAnswerRef(tx, job, in) }); err != nil {
			return err
		}
	}
	in.current, err = loadEvidenceImages(ctx, p.deps.Store, in.current)
	if err != nil {
		return err
	}
	in.origin, err = loadEvidenceImages(ctx, p.deps.Store, in.origin)
	if err != nil {
		return err
	}
	definition, err := prompt.ParsePinnedTask(in.snapshot.Task, in.snapshot.DefinitionJSON, in.snapshot.CanonicalDigest, in.snapshot.SchemaVersion)
	if err != nil {
		return messaging.ErrPermanent
	}
	if job.SchemaVersion > 0 && definition.SchemaVersion > 0 && job.SchemaVersion != definition.SchemaVersion {
		return messaging.ErrPermanent
	}
	if job.Kind == "CATALOG" {
		return p.catalog(ctx, job, in, definition, fenced)
	}
	if in.impossible || len(in.current) == 0 {
		// The environment was not captured: every baseline item is NOT_OBSERVED
		// and the model is never called. A strict comparison keeps "no basis"
		// apart from "not observed" and keeps any staff pairing on record.
		var basis map[identity.ID]bool
		if in.strict {
			basis = baselineBasis(in)
		}
		results := make([]database.InventoryItemResult, 0, len(in.items))
		for _, item := range in.items {
			reason := "ENVIRONMENT_NOT_CAPTURED"
			if in.strict && in.unmatched {
				reason = reasonEnvironmentUnmatched
			}
			if in.strict && !basis[item.IdentityID] {
				reason = reasonNoBaselineView
			}
			results = append(results, notObserved(job, item, reason, p.deps.Now()))
		}
		return fenced(func(tx *gorm.DB) error {
			if err := replaceResults(tx, job, in.staffPairs, results); err != nil {
				return err
			}
			return recordAnswerRef(tx, job, in)
		})
	}
	return p.compare(ctx, job, in, definition, fenced)
}

// Failed records the terminal outcome of a job whose attempts are exhausted.
func (p *Processor) Failed(ctx context.Context, tx *gorm.DB, job database.InventoryJob, _ string) error {
	switch job.Kind {
	case "CATALOG":
		var current database.InventoryJob
		if err := tx.WithContext(ctx).Select("status").Where("tenant_id=? AND id=?", job.TenantID, job.ID).Take(&current).Error; err != nil {
			return err
		}
		if current.Status == "CANCELED" {
			return nil
		}
		var version database.OriginVersion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", job.TenantID, job.OriginVersionID).Take(&version).Error; err != nil {
			return err
		}
		if version.Status != "DRAFT" {
			return tx.WithContext(ctx).Model(&database.InventoryJob{}).Where("tenant_id=? AND id=? AND status='FAILED'", job.TenantID, job.ID).Updates(map[string]any{"status": "CANCELED", "lease_token": nil, "lease_until": nil, "updated_at": p.deps.Now().UTC()}).Error
		}
		var pending int64
		if err := tx.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND kind='CATALOG' AND id<>? AND status NOT IN ('SUCCEEDED','FAILED','CANCELED')", job.TenantID, job.OriginVersionID, job.ID).Count(&pending).Error; err != nil {
			return err
		}
		if pending == 0 {
			return tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=? AND status='DRAFT' AND draft_state='PREPARING'", job.TenantID, job.OriginVersionID).Update("draft_state", "FAILED").Error
		}
	case "COMPARE":
		var environment database.InventoryEnvironment
		err := tx.Where("tenant_id=? AND origin_version_id=? AND identity_id=?", job.TenantID, job.OriginVersionID, job.EnvironmentIdentityID).Take(&environment).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		if err == nil {
			var items []database.InventoryItem
			if err := tx.Where("tenant_id=? AND origin_version_id=? AND environment_id=? AND decision IN ('ACCEPTED','EDITED','ADDED')", job.TenantID, job.OriginVersionID, environment.ID).Find(&items).Error; err != nil {
				return err
			}
			// A failed pairing revision fails only the revised items: the
			// rest of the room keeps the previous generation's results.
			revised, err := failedRevision(ctx, tx, job)
			if err != nil {
				return err
			}
			// A strict comparison never reports a failure for an item the
			// baseline does not show: it had no basis to be compared.
			basis, strict, err := failedJobBasis(ctx, tx, job, items)
			if err != nil {
				return err
			}
			for _, item := range items {
				if revised.carry != nil && !revised.carry.rejudge[item.IdentityID] {
					continue
				}
				reason := "ANALYSIS_FAILED"
				if strict && !basis[item.IdentityID] {
					reason = reasonNoBaselineView
				}
				row := notObserved(job, item, reason, p.deps.Now())
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
					return err
				}
			}
			if revised.carry != nil {
				results, pairs := carriedResults(job, revised.carry, p.deps.Now().UTC())
				for i := range pairs {
					if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&pairs[i]).Error; err != nil {
						return err
					}
				}
				for i := range results {
					if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&results[i]).Error; err != nil {
						return err
					}
				}
			}
		}
		return p.emitCompareCompletion(ctx, tx, job)
	}
	return nil
}

// Succeeded finalizes the draft after the last CATALOG job and wakes the
// classifier after the last COMPARE job of the inspection.
func (p *Processor) Succeeded(ctx context.Context, tx *gorm.DB, job database.InventoryJob) error {
	if job.Kind == "COMPARE" {
		return p.emitCompareCompletion(ctx, tx, job)
	}
	if job.Kind != "CATALOG" {
		return nil
	}
	var current database.InventoryJob
	if err := tx.WithContext(ctx).Select("status").Where("tenant_id=? AND id=?", job.TenantID, job.ID).Take(&current).Error; err != nil {
		return err
	}
	if current.Status == "CANCELED" {
		return nil
	}
	var version database.OriginVersion
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", job.TenantID, job.OriginVersionID).Take(&version).Error; err != nil {
		return err
	}
	if version.Status != "DRAFT" {
		return tx.WithContext(ctx).Model(&database.InventoryJob{}).Where("tenant_id=? AND id=? AND status='SUCCEEDED'", job.TenantID, job.ID).Updates(map[string]any{"status": "CANCELED", "lease_token": nil, "lease_until": nil, "updated_at": p.deps.Now().UTC()}).Error
	}
	var pending int64
	if err := tx.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND kind='CATALOG' AND status NOT IN ('SUCCEEDED','FAILED','CANCELED')", job.TenantID, job.OriginVersionID).Count(&pending).Error; err != nil {
		return err
	}
	if pending > 0 {
		return nil
	}
	var failed int64
	if err := tx.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND kind='CATALOG' AND status='FAILED'", job.TenantID, job.OriginVersionID).Count(&failed).Error; err != nil {
		return err
	}
	state := "READY"
	if failed > 0 {
		state = "FAILED"
	}
	result := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=? AND status='DRAFT' AND draft_state='PREPARING'", job.TenantID, job.OriginVersionID).Update("draft_state", state)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 || state != "READY" {
		return nil
	}
	return inventorycore.EmitDraftReady(tx, job.TenantID, job.OriginVersionID, job.InspectionID, p.deps.Now())
}

func (p *Processor) emitCompareCompletion(ctx context.Context, tx *gorm.DB, job database.InventoryJob) error {
	// Serialize sibling completions on the inspection row. Without it, two jobs
	// finishing together each count the other as RUNNING and neither emits.
	var inspection database.Inspection
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("tenant_id=? AND id=?", job.TenantID, job.InspectionID).Take(&inspection).Error; err != nil {
		return err
	}
	var pending int64
	if err := tx.WithContext(ctx).Model(&database.InventoryJob{}).
		Where("tenant_id=? AND inspection_id=? AND kind='COMPARE' AND id<>? AND status NOT IN ('SUCCEEDED','FAILED','CANCELED')", job.TenantID, job.InspectionID, job.ID).
		Count(&pending).Error; err != nil {
		return err
	}
	if pending != 0 {
		return nil
	}
	now := p.deps.Now().UTC()
	return messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: identity.NewID(), Type: "analysis.comparison_completed.v1", SchemaVersion: 1, OccurredAt: now, TenantID: job.TenantID, AggregateID: job.InspectionID, CorrelationID: job.InspectionID.String(), Payload: map[string]any{"inspectionId": job.InspectionID}})
}

// withJobCorrelation supplies the identifiers the observed gateway records in
// the usage ledger. Inventory jobs are not driven by a broker delivery, so the
// job id also stands for the causing event.
func withJobCorrelation(ctx context.Context, job database.InventoryJob) context.Context {
	correlation := observability.CorrelationFromContext(ctx)
	correlation.TenantID = job.TenantID.String()
	correlation.JobID = job.ID.String()
	correlation.InspectionID = job.InspectionID.String()
	if correlation.EventID == "" {
		correlation.EventID = job.ID.String()
	}
	if correlation.CorrelationID == "" {
		correlation.CorrelationID = job.InspectionID.String()
	}
	if correlation.ExecutionID == "" {
		correlation.ExecutionID = identity.NewID().String()
	}
	correlation.Attempt = job.Attempts
	return observability.WithCorrelation(ctx, correlation)
}

func notObserved(job database.InventoryJob, item database.InventoryItem, reason string, now time.Time) database.InventoryItemResult {
	rid := item.IdentityID
	before := item.Condition
	return database.InventoryItemResult{ID: identity.NewID(), TenantID: job.TenantID, JobID: job.ID, InspectionID: job.InspectionID, ItemIdentityID: &rid, Status: "NOT_OBSERVED", ConditionBefore: &before, ReasonCode: &reason, CreatedAt: now.UTC()}
}

// recordAnswerRef stores, on a strict COMPARE job, which submitted answer of
// its room it read, so a later pairing revision knows whether the room's
// capture changed since.
func recordAnswerRef(tx *gorm.DB, job database.InventoryJob, in jobInput) error {
	if !in.strict || in.answerRef == "" {
		return nil
	}
	return tx.Model(&database.InventoryJob{}).Where("tenant_id=? AND id=?", job.TenantID, job.ID).Update("capture_answer_ref", in.answerRef).Error
}

// replaceResults rewrites the pairs and item results of one job. A replay after
// a committed write therefore converges to one result set.
func replaceResults(tx *gorm.DB, job database.InventoryJob, pairs []database.InventoryEvidencePair, results []database.InventoryItemResult) error {
	if err := tx.Where("tenant_id=? AND job_id=?", job.TenantID, job.ID).Delete(&database.InventoryEvidencePair{}).Error; err != nil {
		return err
	}
	if err := tx.Where("tenant_id=? AND job_id=?", job.TenantID, job.ID).Delete(&database.InventoryItemResult{}).Error; err != nil {
		return err
	}
	for i := range pairs {
		if err := tx.Create(&pairs[i]).Error; err != nil {
			return err
		}
	}
	for i := range results {
		if err := tx.Create(&results[i]).Error; err != nil {
			return err
		}
	}
	return nil
}

var _ inventoryexecutor.Processor = (*Processor)(nil)
