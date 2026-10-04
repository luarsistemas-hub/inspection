package resolvers

import (
	"context"
	"errors"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func reportPDFJobStatus(ctx context.Context, db *gorm.DB, tenantID, snapshotID identity.ID, audience string) (string, error) {
	status := "PENDING"
	err := withTask06Tenant(ctx, db, tenantID, func(tx *gorm.DB) error {
		var err error
		status, err = reportPDFJobStatusTx(tx, tenantID, snapshotID, audience)
		return err
	})
	return status, err
}

func reportPDFJobStatusTx(tx *gorm.DB, tenantID, snapshotID identity.ID, audience string) (string, error) {
	var job database.ReportPDFJob
	if err := tx.Where("tenant_id=? AND snapshot_id=? AND audience=?", tenantID, snapshotID, audience).First(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "PENDING", nil
		}
		return "", err
	}
	switch job.Status {
	case "READY":
		return "READY", nil
	case "PROCESSING":
		return "PROCESSING", nil
	case "FAILED", "CANCELED":
		return "FAILED", nil
	default:
		return "PENDING", nil
	}
}
