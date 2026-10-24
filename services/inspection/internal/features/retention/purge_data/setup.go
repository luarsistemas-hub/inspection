// Package purge_data reconciles every tenant-owned artifact for one eligible
// inspection. Purge is idempotent and never treats a partial cleanup as done.
package purge_data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/objectstore"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// HandleDueEvent validates and processes one retention.purge_due.v1 event.
// It is shared by the worker consumer and integration tests so the tested
// handler is the same production path.
func HandleDueEvent(ctx context.Context, tx *gorm.DB, store objectstore.Store, envelope events.RawEnvelope) error {
	var payload struct {
		InspectionID identity.ID `json:"inspectionId"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.InspectionID == (identity.ID{}) {
		return messaging.ErrPermanent
	}
	var hold database.LegalHold
	if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=? AND active", envelope.TenantID, payload.InspectionID).First(&hold).Error; err == nil {
		return nil
	} else if err != gorm.ErrRecordNotFound {
		return err
	}
	// Never trust publication as proof that the retention clock elapsed.
	var inspection database.Inspection
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, payload.InspectionID).First(&inspection).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return messaging.ErrPermanent
		}
		return err
	}
	policy := retentioncore.DefaultPolicy()
	var configured database.RetentionPolicy
	if err := tx.WithContext(ctx).Where("tenant_id=?", envelope.TenantID).Order("version DESC").First(&configured).Error; err == nil {
		policy.EvidenceReports = time.Duration(configured.EvidenceDays) * 24 * time.Hour
		policy.Operational = time.Duration(configured.OperationalDays) * 24 * time.Hour
	} else if err != gorm.ErrRecordNotFound {
		return err
	}
	clock, err := retentioncore.NewClock(retentioncore.EvidenceReports, inspection.UpdatedAt, time.Time{}, policy)
	if err != nil {
		return err
	}
	if !clock.Eligible(time.Now().UTC()) {
		return gorm.ErrInvalidData
	}
	purge, err := PurgeWithStore(ctx, tx, store, envelope.TenantID, payload.InspectionID, clock)
	if err != nil {
		return err
	}
	return messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: identity.NewID(), Type: "retention.purged.v1", SchemaVersion: 1, OccurredAt: purge.CreatedAt, TenantID: envelope.TenantID, AggregateID: payload.InspectionID, CorrelationID: envelope.CorrelationID, CausationID: envelope.ID.String(), Payload: map[string]any{"classes": []string{"originals", "parts", "derivatives", "reports", "associations"}}})
}

// HandlePurgedEvent validates the informational retention.purged.v1 payload.
func HandlePurgedEvent(_ context.Context, _ *gorm.DB, envelope events.RawEnvelope) error {
	var payload map[string]any
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return messaging.ErrPermanent
	}
	return nil
}

// Purge keeps the original public seam for callers that only need database
// reconciliation. Object-store cleanup is added by PurgeWithStore.
func Purge(ctx context.Context, db *gorm.DB, tenantID, inspectionID identity.ID, clock retentioncore.Clock) (database.PurgeRun, error) {
	return PurgeWithStore(ctx, db, objectstore.Store{}, tenantID, inspectionID, clock)
}

// PurgeWithStore deletes private blobs and all personal associations before
// recording a completed manifest. A completed PurgeRun is returned unchanged.
func PurgeWithStore(ctx context.Context, db *gorm.DB, store objectstore.Store, tenantID, inspectionID identity.ID, clock retentioncore.Clock) (database.PurgeRun, error) {
	if db == nil || tenantID == (identity.ID{}) || inspectionID == (identity.ID{}) {
		return database.PurgeRun{}, fmt.Errorf("retention purge: missing identity")
	}
	if !clock.Eligible(time.Now().UTC()) {
		return database.PurgeRun{}, apperror.New(apperror.InvalidState, "retention", "retention period has not elapsed or a legal hold is active")
	}
	var completed database.PurgeRun
	if err := db.WithContext(ctx).Where("tenant_id=? AND inspection_id=? AND status='COMPLETED'", tenantID, inspectionID).First(&completed).Error; err == nil {
		return completed, nil
	} else if err != gorm.ErrRecordNotFound {
		return database.PurgeRun{}, err
	}
	var result database.PurgeRun
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SELECT set_config('app.retention_purge', 'on', true)").Error; err != nil {
				return err
			}
		}
		if err := lockInspectionAndCheckLegalHold(tx, tenantID, inspectionID); err != nil {
			return err
		}
		var responsibilityIDs []identity.ID
		if err := tx.Model(&database.Responsibility{}).Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Pluck("id", &responsibilityIDs).Error; err != nil {
			return err
		}
		var media []database.MediaObject
		var mediaIDs []identity.ID
		if len(responsibilityIDs) > 0 {
			if err := tx.Where("tenant_id=? AND responsibility_id IN ?", tenantID, responsibilityIDs).Find(&media).Error; err != nil {
				return err
			}
			for _, item := range media {
				mediaIDs = append(mediaIDs, item.ID)
			}
		}
		if err := lockVideoSourcesAndCheckHolds(tx, tenantID, mediaIDs); err != nil {
			return err
		}
		var protectedOriginResponsibilities []identity.ID
		protectedInventoryMediaIDs := make(map[identity.ID]struct{})
		if len(responsibilityIDs) > 0 {
			var versions []database.OriginVersion
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id IN ? AND media_kind='VIDEO'", tenantID, responsibilityIDs).Find(&versions).Error; err != nil {
				return err
			}
			for _, version := range versions {
				var references, holds int64
				if err := tx.Model(&database.VideoEvidenceReference{}).Where("tenant_id=? AND owner_type='ORIGIN_VERSION' AND owner_id=?", tenantID, version.ID).Count(&references).Error; err != nil {
					return err
				}
				if err := tx.Model(&database.LegalHold{}).Where("tenant_id=? AND origin_version_id=? AND active=true", tenantID, version.ID).Count(&holds).Error; err != nil {
					return err
				}
				if references > 0 || holds > 0 {
					if version.ResponsibilityID != nil {
						protectedOriginResponsibilities = append(protectedOriginResponsibilities, *version.ResponsibilityID)
					}
				}
			}
		}
		var inventoryMediaIDs []identity.ID
		if len(mediaIDs) > 0 {
			if err := tx.Raw(`SELECT DISTINCT e.media_id
				FROM origins.inventory_item_evidence e
				JOIN origins.inventory_items i ON i.tenant_id=e.tenant_id AND i.id=e.item_id
				JOIN origins.origin_versions v ON v.tenant_id=i.tenant_id AND v.id=i.origin_version_id
				WHERE e.tenant_id=? AND v.status<>'DISCARDED' AND e.media_id IN ?`, tenantID, mediaIDs).Scan(&inventoryMediaIDs).Error; err != nil {
				return err
			}
		}
		if len(inventoryMediaIDs) > 0 {
			for _, mediaID := range inventoryMediaIDs {
				protectedInventoryMediaIDs[mediaID] = struct{}{}
				for _, item := range media {
					if item.ID == mediaID {
						protectedOriginResponsibilities = append(protectedOriginResponsibilities, item.ResponsibilityID)
						break
					}
				}
			}
		}
		if len(mediaIDs) > 0 {
			if err := tx.Exec(`DELETE FROM origins.inventory_item_evidence e
				USING origins.inventory_items i, origins.origin_versions v
				WHERE e.tenant_id=? AND e.media_id IN ?
				AND i.tenant_id=e.tenant_id AND i.id=e.item_id
				AND v.tenant_id=i.tenant_id AND v.id=i.origin_version_id
				AND v.status='DISCARDED'`, tenantID, mediaIDs).Error; err != nil {
				return err
			}
		}
		var uploadIDs []identity.ID
		if len(mediaIDs) > 0 {
			var uploads []database.MultipartUpload
			if err := tx.Where("tenant_id=? AND media_id IN ?", tenantID, mediaIDs).Find(&uploads).Error; err != nil {
				return err
			}
			for _, upload := range uploads {
				uploadIDs = append(uploadIDs, upload.ID)
			}
		}
		if err := deletePurgedVideoPlanFrameLinks(tx, tenantID, inspectionID); err != nil {
			return err
		}
		if err := deletePurgedVideoPlanPreparationLinks(tx, tenantID, inspectionID); err != nil {
			return err
		}
		if err := releasePurgedReportReferences(tx, tenantID, inspectionID); err != nil {
			return err
		}
		if store.Client != nil {
			if err := purgeUnreferencedVideoFrames(ctx, tx, store, tenantID, mediaIDs); err != nil {
				return err
			}
			protectedVideoMediaIDs, err := retainedVideoMediaIDs(tx, tenantID, mediaIDs)
			if err != nil {
				return err
			}
			for _, item := range media {
				_, protectedVideo := protectedVideoMediaIDs[item.ID]
				_, protectedInventory := protectedInventoryMediaIDs[item.ID]
				if !protectedVideo && !protectedInventory {
					if err := deleteObject(ctx, store, item.ObjectKey); err != nil {
						return err
					}
				}
				if protectedVideo {
					continue
				}
				var derivatives []database.MediaDerivative
				if err := tx.Where("tenant_id=? AND media_id=?", tenantID, item.ID).Find(&derivatives).Error; err != nil {
					return err
				}
				for _, derivative := range derivatives {
					if err := deleteObject(ctx, store, derivative.ObjectKey); err != nil {
						return err
					}
				}
			}
			var artifacts []database.ReportArtifact
			if err := tx.Table("reports.report_artifacts AS a").Select("a.*").Joins("JOIN reports.report_snapshots s ON s.id=a.snapshot_id AND s.tenant_id=a.tenant_id").Where("a.tenant_id=? AND s.inspection_id=?", tenantID, inspectionID).Find(&artifacts).Error; err != nil {
				return err
			}
			for _, artifact := range artifacts {
				if err := deleteObject(ctx, store, artifact.ObjectKey); err != nil {
					return err
				}
			}
			var pendingKeys []string
			if err := tx.Model(&database.ReportPDFJob{}).Where("tenant_id=? AND pending_object_key<>'' AND snapshot_id IN (SELECT id FROM reports.report_snapshots WHERE tenant_id=? AND inspection_id=?)", tenantID, tenantID, inspectionID).Pluck("pending_object_key", &pendingKeys).Error; err != nil {
				return err
			}
			for _, key := range pendingKeys {
				if err := deleteObject(ctx, store, key); err != nil {
					return err
				}
			}
		}
		deleteWhere := func(model any, query string, args ...any) error {
			return tx.Where(query, args...).Delete(model).Error
		}
		if err := deleteWhere(&database.ReportPDFJob{}, "tenant_id=? AND snapshot_id IN (SELECT id FROM reports.report_snapshots WHERE tenant_id=? AND inspection_id=?)", tenantID, tenantID, inspectionID); err != nil {
			return err
		}
		if err := deleteWhere(&database.ReportArtifact{}, "tenant_id=? AND snapshot_id IN (SELECT id FROM reports.report_snapshots WHERE tenant_id=? AND inspection_id=?)", tenantID, tenantID, inspectionID); err != nil {
			return err
		}
		for _, model := range []any{&database.ReportSnapshot{}, &database.FindingRecord{}, &database.AnalysisRun{}, &database.ComparisonJob{}, &database.ClassificationRun{}} {
			var err error
			switch model.(type) {
			case *database.ReportSnapshot:
				err = deleteWhere(model, "tenant_id=? AND inspection_id=?", tenantID, inspectionID)
			case *database.FindingRecord:
				err = deleteWhere(model, "tenant_id=? AND analysis_run_id IN (SELECT id FROM analysis.analysis_runs WHERE tenant_id=? AND job_id IN (SELECT id FROM analysis.comparison_jobs WHERE tenant_id=? AND inspection_id=?))", tenantID, tenantID, tenantID, inspectionID)
			case *database.AnalysisRun:
				err = deleteWhere(model, "tenant_id=? AND job_id IN (SELECT id FROM analysis.comparison_jobs WHERE tenant_id=? AND inspection_id=?)", tenantID, tenantID, inspectionID)
			case *database.ComparisonJob:
				err = deleteWhere(model, "tenant_id=? AND inspection_id=?", tenantID, inspectionID)
			case *database.ClassificationRun:
				err = deleteWhere(model, "tenant_id=? AND inspection_id=?", tenantID, inspectionID)
			}
			if err != nil {
				return err
			}
		}
		if err := purgeInventoryAnalysis(tx, tenantID, inspectionID); err != nil {
			return err
		}
		if err := deletePurgedVideoPlans(tx, tenantID, inspectionID); err != nil {
			return err
		}
		if store.Client != nil {
			if err := deleteOrphanVideoPreparations(tx, tenantID, mediaIDs); err != nil {
				return err
			}
		}
		if err := detachRetainedVideoPreparations(tx, tenantID, mediaIDs); err != nil {
			return err
		}
		for _, item := range []any{
			&database.UsageRecord{}, &database.LLMCallRecord{}, &database.DashboardInspection{}, &database.ReferenceSnapshot{}, &database.PolicySnapshot{},
			&database.DeletionRequest{},
		} {
			var err error
			switch item.(type) {
			case *database.UsageRecord, *database.LLMCallRecord:
				err = deleteWhere(item, "tenant_id=? AND inspection_id=?", tenantID, inspectionID)
			case *database.DashboardInspection, *database.ReferenceSnapshot, *database.PolicySnapshot, *database.DeletionRequest:
				err = deleteWhere(item, "tenant_id=? AND inspection_id=?", tenantID, inspectionID)
			}
			if err != nil {
				return err
			}
		}
		if len(mediaIDs) > 0 {
			if err := deleteWhere(&database.MediaDerivative{}, "tenant_id=? AND media_id IN ?", tenantID, mediaIDs); err != nil {
				return err
			}
			if err := deleteWhere(&database.ScreeningRun{}, "tenant_id=? AND media_id IN ?", tenantID, mediaIDs); err != nil {
				return err
			}
		}
		if len(uploadIDs) > 0 {
			if err := deleteWhere(&database.UploadPart{}, "tenant_id=? AND upload_id IN ?", tenantID, uploadIDs); err != nil {
				return err
			}
			if err := deleteWhere(&database.MultipartUpload{}, "tenant_id=? AND id IN ?", tenantID, uploadIDs); err != nil {
				return err
			}
		}
		criticalIntent := identity.ID(uuid.NewSHA1(uuid.Nil, []byte("critical:"+tenantID.String()+":"+inspectionID.String()+":CRITICAL")))
		if err := deleteWhere(&database.NotificationPayload{}, "tenant_id=? AND delivery_id IN (SELECT id FROM notifications.deliveries WHERE tenant_id=? AND (intent_id=? OR inspection_id=?))", tenantID, tenantID, criticalIntent, inspectionID); err != nil {
			return err
		}
		if err := deleteWhere(&database.ChannelAttempt{}, "tenant_id=? AND delivery_id IN (SELECT id FROM notifications.deliveries WHERE tenant_id=? AND intent_id=?)", tenantID, tenantID, criticalIntent); err != nil {
			return err
		}
		if err := deleteWhere(&database.ChannelAttempt{}, "tenant_id=? AND delivery_id IN (SELECT id FROM notifications.deliveries WHERE tenant_id=? AND inspection_id=?)", tenantID, tenantID, inspectionID); err != nil {
			return err
		}
		if err := deleteWhere(&database.Delivery{}, "tenant_id=? AND intent_id=?", tenantID, criticalIntent); err != nil {
			return err
		}
		if err := deleteWhere(&database.Delivery{}, "tenant_id=? AND inspection_id=?", tenantID, inspectionID); err != nil {
			return err
		}
		if err := deleteWhere(&database.VideoReplacement{}, "tenant_id=? AND inspection_id=?", tenantID, inspectionID); err != nil {
			return err
		}
		if len(responsibilityIDs) > 0 {
			for _, model := range []any{&database.RequirementAnswer{}, &database.SubmissionVersion{}, &database.CaptureDraft{}, &database.RecaptureRequirement{}, &database.RecaptureRequest{}, &database.OriginEvidence{}, &database.OriginVersion{}, &database.ProcessingAcceptance{}, &database.ExternalSession{}, &database.OTPChallenge{}, &database.Invitation{}} {
				if err := deleteByResponsibilities(tx, model, tenantID, responsibilityIDs); err != nil {
					return err
				}
			}
			if len(protectedInventoryMediaIDs) > 0 {
				protectedIDs := make([]identity.ID, 0, len(protectedInventoryMediaIDs))
				for id := range protectedInventoryMediaIDs {
					protectedIDs = append(protectedIDs, id)
				}
				if err := deleteWhere(&database.MediaObject{}, "tenant_id=? AND responsibility_id IN ? AND id NOT IN (SELECT media_id FROM media.video_preparation_jobs WHERE tenant_id=?) AND id NOT IN ?", tenantID, responsibilityIDs, tenantID, protectedIDs); err != nil {
					return err
				}
			} else if err := deleteWhere(&database.MediaObject{}, "tenant_id=? AND responsibility_id IN ? AND id NOT IN (SELECT media_id FROM media.video_preparation_jobs WHERE tenant_id=?)", tenantID, responsibilityIDs, tenantID); err != nil {
				return err
			}
			if len(protectedOriginResponsibilities) > 0 {
				if err := tx.Model(&database.Responsibility{}).Where("tenant_id=? AND id IN ?", tenantID, protectedOriginResponsibilities).
					Updates(map[string]any{"inspection_id": identity.ID{}, "participant_id": identity.ID{}, "status": "EXPIRED", "updated_at": time.Now().UTC()}).Error; err != nil {
					return err
				}
				if err := deleteWhere(&database.Responsibility{}, "tenant_id=? AND id IN ? AND id NOT IN ?", tenantID, responsibilityIDs, protectedOriginResponsibilities); err != nil {
					return err
				}
			} else if err := deleteWhere(&database.Responsibility{}, "tenant_id=? AND id IN ?", tenantID, responsibilityIDs); err != nil {
				return err
			}
		}
		if err := deleteWhere(&database.Inspection{}, "tenant_id=? AND id=?", tenantID, inspectionID); err != nil {
			return err
		}
		if err := tx.Model(&database.AuditEvent{}).Where("tenant_id=? AND target_id=?", tenantID, inspectionID.String()).Updates(map[string]any{"actor_id": identity.ID{}, "target_id": "purged", "reason": "deidentified after retention purge"}).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: tenantID, ActorID: identity.ID{}, Action: "retention.purged", TargetType: "inspection", TargetID: "purged", Outcome: "COMPLETED", Reason: "deidentified after retention purge", CorrelationID: "retention-" + inspectionID.String(), OccurredAt: now}).Error; err != nil {
			return err
		}
		manifest, _ := json.Marshal(retentioncore.Manifest{Originals: true, Parts: true, Derivatives: true, Reports: true, Associations: true})
		result = database.PurgeRun{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, Status: "COMPLETED", Manifest: manifest, CompletedAt: &now, CreatedAt: now}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "inspection_id"}}, DoNothing: true}).Create(&result).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return database.PurgeRun{}, err
	}
	return result, nil
}

func releasePurgedReportReferences(tx *gorm.DB, tenantID, inspectionID identity.ID) error {
	var snapshotIDs []identity.ID
	if err := tx.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Pluck("id", &snapshotIDs).Error; err != nil {
		return err
	}
	if len(snapshotIDs) > 0 {
		if err := tx.Where("tenant_id=? AND owner_type='REPORT_SNAPSHOT' AND owner_id IN ?", tenantID, snapshotIDs).Delete(&database.VideoEvidenceReference{}).Error; err != nil {
			return err
		}
	}
	return tx.Exec(`DELETE FROM media.video_evidence_references
		WHERE tenant_id = ? AND owner_type = 'VIDEO_PLAN' AND owner_id IN (
			SELECT id FROM analysis.video_plans WHERE tenant_id = ? AND inspection_id = ?
		)`, tenantID, tenantID, inspectionID).Error
}

func deletePurgedVideoPlanFrameLinks(tx *gorm.DB, tenantID, inspectionID identity.ID) error {
	return tx.Exec(`DELETE FROM analysis.video_plan_item_frames
		WHERE tenant_id = ? AND plan_item_id IN (
			SELECT i.id FROM analysis.video_plan_items i
			JOIN analysis.video_plans p ON p.tenant_id=i.tenant_id AND p.id=i.plan_id
			WHERE p.tenant_id = ? AND p.inspection_id = ?
		)`, tenantID, tenantID, inspectionID).Error
}

func deletePurgedVideoPlanPreparationLinks(tx *gorm.DB, tenantID, inspectionID identity.ID) error {
	return tx.Exec(`DELETE FROM analysis.video_plan_preparations
		WHERE tenant_id = ? AND plan_id IN (
			SELECT id FROM analysis.video_plans WHERE tenant_id = ? AND inspection_id = ?
		)`, tenantID, tenantID, inspectionID).Error
}

// purgeInventoryAnalysis removes the inspection's inventory analysis rows and
// detaches baseline versions created from it: an activated baseline outlives
// the inspection that cataloged it.
func purgeInventoryAnalysis(tx *gorm.DB, tenantID, inspectionID identity.ID) error {
	jobs := "SELECT id FROM analysis.inventory_jobs WHERE tenant_id = ? AND inspection_id = ?"
	for _, statement := range []string{
		"DELETE FROM analysis.inventory_item_results WHERE tenant_id = ? AND job_id IN (" + jobs + ")",
		"DELETE FROM analysis.inventory_evidence_pairs WHERE tenant_id = ? AND job_id IN (" + jobs + ")",
		"DELETE FROM analysis.findings WHERE tenant_id = ? AND inventory_job_id IN (" + jobs + ")",
	} {
		if err := tx.Exec(statement, tenantID, tenantID, inspectionID).Error; err != nil {
			return err
		}
	}
	if err := tx.Exec(`DELETE FROM analysis.inventory_item_results WHERE tenant_id = ? AND inspection_id = ?`, tenantID, inspectionID).Error; err != nil {
		return err
	}
	if err := tx.Exec(`DELETE FROM analysis.inventory_jobs WHERE tenant_id = ? AND inspection_id = ?`, tenantID, inspectionID).Error; err != nil {
		return err
	}
	return tx.Exec(`UPDATE origins.origin_versions SET source_inspection_id = NULL
		WHERE tenant_id = ? AND source_inspection_id = ?`, tenantID, inspectionID).Error
}

func deletePurgedVideoPlans(tx *gorm.DB, tenantID, inspectionID identity.ID) error {
	if err := tx.Exec(`UPDATE inspections.inspections SET active_video_plan_id = NULL
		WHERE tenant_id = ? AND id = ?`, tenantID, inspectionID).Error; err != nil {
		return err
	}
	if err := tx.Exec(`UPDATE origins.origin_versions SET video_plan_id = NULL
		WHERE tenant_id = ? AND video_plan_id IN (
			SELECT id FROM analysis.video_plans WHERE tenant_id = ? AND inspection_id = ?
		)`, tenantID, tenantID, inspectionID).Error; err != nil {
		return err
	}
	if err := tx.Exec(`DELETE FROM analysis.video_plan_preparations
		WHERE tenant_id = ? AND plan_id IN (
			SELECT id FROM analysis.video_plans WHERE tenant_id = ? AND inspection_id = ?
		)`, tenantID, tenantID, inspectionID).Error; err != nil {
		return err
	}
	if err := tx.Exec(`DELETE FROM analysis.video_plan_items
		WHERE tenant_id = ? AND plan_id IN (
			SELECT id FROM analysis.video_plans WHERE tenant_id = ? AND inspection_id = ?
		)`, tenantID, tenantID, inspectionID).Error; err != nil {
		return err
	}
	return tx.Exec("DELETE FROM analysis.video_plans WHERE tenant_id = ? AND inspection_id = ?", tenantID, inspectionID).Error
}

func purgeUnreferencedVideoFrames(ctx context.Context, tx *gorm.DB, store objectstore.Store, tenantID identity.ID, mediaIDs []identity.ID) error {
	if len(mediaIDs) == 0 {
		return nil
	}
	var preparations []database.VideoPreparationJob
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND media_id IN ?", tenantID, mediaIDs).Find(&preparations).Error; err != nil {
		return err
	}
	for _, preparation := range preparations {
		var references, retainedOwners, heldOrigins int64
		if err := tx.Model(&database.VideoEvidenceReference{}).Where("tenant_id=? AND preparation_id=?", tenantID, preparation.ID).Count(&references).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoEvidenceReference{}).Where("tenant_id=? AND preparation_id=? AND owner_type IN ('VIDEO_PLAN','REPORT_SNAPSHOT','ACTIVE_WORK')", tenantID, preparation.ID).Count(&retainedOwners).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.LegalHold{}).Where("tenant_id=? AND active=true AND origin_version_id IN (SELECT id FROM origins.origin_versions WHERE tenant_id=? AND video_preparation_id=?)", tenantID, tenantID, preparation.ID).Count(&heldOrigins).Error; err != nil {
			return err
		}
		if retainedOwners > 0 {
			return apperror.New(apperror.InvalidState, "preparationId", "retained video evidence still depends on this frame bank")
		}
		if references > 0 || heldOrigins > 0 {
			continue
		}
		var frames []database.VideoFrame
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND preparation_id=?", tenantID, preparation.ID).Find(&frames).Error; err != nil {
			return err
		}
		for _, frame := range frames {
			if err := deleteObject(ctx, store, frame.ObjectKey); err != nil {
				return err
			}
		}
		if err := tx.Where("tenant_id=? AND preparation_id=?", tenantID, preparation.ID).Delete(&database.VideoScopeItem{}).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND preparation_id=?", tenantID, preparation.ID).Delete(&database.VideoFrame{}).Error; err != nil {
			return err
		}
	}
	return nil
}

func deleteOrphanVideoPreparations(tx *gorm.DB, tenantID identity.ID, mediaIDs []identity.ID) error {
	if len(mediaIDs) == 0 {
		return nil
	}
	return tx.Where(`tenant_id=? AND media_id IN ?
		AND NOT EXISTS (SELECT 1 FROM media.video_evidence_references r WHERE r.tenant_id=media.video_preparation_jobs.tenant_id AND r.preparation_id=media.video_preparation_jobs.id)
		AND NOT EXISTS (SELECT 1 FROM origins.origin_versions ov WHERE ov.tenant_id=media.video_preparation_jobs.tenant_id AND ov.video_preparation_id=media.video_preparation_jobs.id)`, tenantID, mediaIDs).Delete(&database.VideoPreparationJob{}).Error
}

// detachRetainedVideoPreparations unlinks frame banks kept for an origin or
// another owner from the submission being purged, so the submission can be
// deleted while the protected bank survives.
func detachRetainedVideoPreparations(tx *gorm.DB, tenantID identity.ID, mediaIDs []identity.ID) error {
	if len(mediaIDs) == 0 {
		return nil
	}
	return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND media_id IN ? AND submission_id IS NOT NULL", tenantID, mediaIDs).Update("submission_id", nil).Error
}

func retainedVideoMediaIDs(tx *gorm.DB, tenantID identity.ID, mediaIDs []identity.ID) (map[identity.ID]struct{}, error) {
	retained := make(map[identity.ID]struct{})
	if len(mediaIDs) == 0 {
		return retained, nil
	}
	var ids []identity.ID
	if err := tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND media_id IN ?", tenantID, mediaIDs).Distinct("media_id").Pluck("media_id", &ids).Error; err != nil {
		return nil, err
	}
	for _, id := range ids {
		retained[id] = struct{}{}
	}
	return retained, nil
}

// lockInspectionAndCheckLegalHold serializes purge with legal-hold changes.
// Hold creation and release take the same inspection-row lock, so a hold
// committed concurrently with purge is observed before any data is deleted.
func lockInspectionAndCheckLegalHold(tx *gorm.DB, tenantID, inspectionID identity.ID) error {
	var inspection database.Inspection
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, inspectionID).First(&inspection).Error; err != nil {
		return err
	}
	var activeHolds int64
	if err := tx.Model(&database.LegalHold{}).Where("tenant_id=? AND inspection_id=? AND active=true", tenantID, inspectionID).Count(&activeHolds).Error; err != nil {
		return err
	}
	if activeHolds > 0 {
		return apperror.New(apperror.InvalidState, "inspectionId", "legal hold prevents retention purge")
	}
	return nil
}

func lockVideoSourcesAndCheckHolds(tx *gorm.DB, tenantID identity.ID, mediaIDs []identity.ID) error {
	if len(mediaIDs) == 0 {
		return nil
	}
	var sources []database.VideoSource
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND media_id IN ?", tenantID, mediaIDs).Order("media_id").Find(&sources).Error; err != nil {
		return err
	}
	for _, source := range sources {
		if source.Status == "DELETING" {
			return apperror.New(apperror.InvalidState, "sourceMediaId", "source deletion is already in progress")
		}
		held, err := retentioncore.EffectiveSourceHeld(tx, tenantID, source.MediaID)
		if err != nil {
			return err
		}
		if held {
			return apperror.New(apperror.InvalidState, "sourceMediaId", "legal hold prevents retention purge")
		}
	}
	return nil
}

func deleteObject(ctx context.Context, store objectstore.Store, key string) error {
	if key == "" {
		return nil
	}
	err := store.Delete(ctx, key)
	if errors.Is(err, objectstore.ErrNotFound) || errors.Is(err, objectstore.ErrInvalid) {
		return nil
	}
	return err
}

func deleteByResponsibilities(tx *gorm.DB, model any, tenantID identity.ID, responsibilityIDs []identity.ID) error {
	switch model.(type) {
	case *database.OriginEvidence:
		return tx.Where("tenant_id=? AND origin_version_id IN (SELECT id FROM origins.origin_versions WHERE tenant_id=? AND responsibility_id IN ?)", tenantID, tenantID, responsibilityIDs).Delete(model).Error
	case *database.OriginVersion:
		return tx.Where("tenant_id=? AND responsibility_id IN ? AND id NOT IN (SELECT owner_id FROM media.video_evidence_references WHERE tenant_id=? AND owner_type='ORIGIN_VERSION')", tenantID, responsibilityIDs, tenantID).Delete(model).Error
	case *database.RecaptureRequirement:
		return tx.Where("tenant_id=? AND request_id IN (SELECT id FROM recapture.requests WHERE tenant_id=? AND responsibility_id IN ?)", tenantID, tenantID, responsibilityIDs).Delete(model).Error
	case *database.RecaptureRequest:
		return tx.Where("tenant_id=? AND responsibility_id IN ?", tenantID, responsibilityIDs).Delete(model).Error
	case *database.Invitation:
		return tx.Where("tenant_id=? AND responsibility_id IN ?", tenantID, responsibilityIDs).Delete(model).Error
	case *database.OTPChallenge:
		return tx.Where("tenant_id=? AND invitation_id IN (SELECT id FROM invitations.invitations WHERE tenant_id=? AND responsibility_id IN ?)", tenantID, tenantID, responsibilityIDs).Delete(model).Error
	case *database.ProcessingAcceptance, *database.ExternalSession, *database.CaptureDraft, *database.RequirementAnswer, *database.SubmissionVersion:
		if _, ok := model.(*database.RequirementAnswer); ok {
			return tx.Where("tenant_id=? AND draft_id IN (SELECT id FROM capture.capture_drafts WHERE tenant_id=? AND responsibility_id IN ?)", tenantID, tenantID, responsibilityIDs).Delete(model).Error
		}
		return tx.Where("tenant_id=? AND responsibility_id IN ?", tenantID, responsibilityIDs).Delete(model).Error
	default:
		return fmt.Errorf("retention purge: unsupported association %T", model)
	}
}
