package main

import (
	"context"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func upsertTriageReviewCase(ctx context.Context, tx *gorm.DB, tenantID, inspectionID identity.ID, classification database.ClassificationRun, reportVersion int, now time.Time) error {
	if tx == nil {
		return fmt.Errorf("triage projection: missing database transaction")
	}
	tx = tx.WithContext(ctx)
	var triage database.TriageCase
	err := tx.Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).First(&triage).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	if err == gorm.ErrRecordNotFound && classification.Classification != "CRITICAL" && classification.Classification != "ATTENTION" {
		return nil
	}
	if err == nil && triage.Status == "ARCHIVED" {
		return nil
	}
	if err == gorm.ErrRecordNotFound {
		triage = database.TriageCase{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, Status: "NEW", Classification: classification.Classification, ReasonCodes: classification.ReasonCodes, ReportVersion: reportVersion, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&triage).Error; err != nil {
			return err
		}
		return tx.Create(&database.TriageCaseEvent{ID: identity.NewID(), TenantID: tenantID, CaseID: triage.ID, ActorID: tenantID, Kind: "RESULT_READY", Body: fmt.Sprintf("Resultado %s · laudo v%d", classification.Classification, reportVersion), CreatedAt: now}).Error
	}
	if reportVersion <= triage.ReportVersion {
		return nil
	}
	if err := tx.Model(&triage).Updates(map[string]any{"status": "IN_REVIEW", "classification": classification.Classification, "reason_codes": classification.ReasonCodes, "report_version": reportVersion, "version": triage.Version + 1, "updated_at": now}).Error; err != nil {
		return err
	}
	return tx.Create(&database.TriageCaseEvent{ID: identity.NewID(), TenantID: tenantID, CaseID: triage.ID, ActorID: tenantID, Kind: "RESULT_READY", Body: fmt.Sprintf("Novo resultado %s · laudo v%d; revisão retomada.", classification.Classification, reportVersion), CreatedAt: now}).Error
}
