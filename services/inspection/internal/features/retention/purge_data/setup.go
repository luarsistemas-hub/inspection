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
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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
		return database.PurgeRun{}, gorm.ErrInvalidData
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
		if store.Client != nil {
			for _, item := range media {
				if err := deleteObject(ctx, store, item.ObjectKey); err != nil {
					return err
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
			if err := tx.Table("reports.report_artifacts a").Select("a.*").Joins("JOIN reports.report_snapshots s ON s.id=a.snapshot_id AND s.tenant_id=a.tenant_id").Where("a.tenant_id=? AND s.inspection_id=?", tenantID, inspectionID).Find(&artifacts).Error; err != nil {
				return err
			}
			for _, artifact := range artifacts {
				if err := deleteObject(ctx, store, artifact.ObjectKey); err != nil {
					return err
				}
			}
		}
		deleteWhere := func(model any, query string, args ...any) error {
			return tx.Where(query, args...).Delete(model).Error
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
		for _, item := range []any{
			&database.UsageRecord{}, &database.DashboardInspection{}, &database.ReferenceSnapshot{}, &database.PolicySnapshot{},
			&database.DeletionRequest{}, &database.LegalHold{},
		} {
			var err error
			switch item.(type) {
			case *database.UsageRecord:
				err = deleteWhere(item, "tenant_id=? AND inspection_id=?", tenantID, inspectionID)
			case *database.DashboardInspection, *database.ReferenceSnapshot, *database.PolicySnapshot, *database.DeletionRequest, *database.LegalHold:
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
		if len(responsibilityIDs) > 0 {
			for _, model := range []any{&database.RequirementAnswer{}, &database.SubmissionVersion{}, &database.CaptureDraft{}, &database.RecaptureRequirement{}, &database.RecaptureRequest{}, &database.OriginEvidence{}, &database.OriginVersion{}, &database.ProcessingAcceptance{}, &database.ExternalSession{}, &database.OTPChallenge{}, &database.Invitation{}} {
				if err := deleteByResponsibilities(tx, model, tenantID, responsibilityIDs); err != nil {
					return err
				}
			}
			if err := deleteWhere(&database.MediaObject{}, "tenant_id=? AND responsibility_id IN ?", tenantID, responsibilityIDs); err != nil {
				return err
			}
			if err := deleteWhere(&database.Responsibility{}, "tenant_id=? AND id IN ?", tenantID, responsibilityIDs); err != nil {
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
		return tx.Where("tenant_id=? AND responsibility_id IN ?", tenantID, responsibilityIDs).Delete(model).Error
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
