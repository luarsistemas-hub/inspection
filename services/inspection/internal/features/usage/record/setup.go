package record

import (
	"context"
	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
)

func Save(ctx context.Context, db *gorm.DB, row database.UsageRecord) error {
	return db.WithContext(ctx).Create(&row).Error
}
func ForJob(ctx context.Context, db *gorm.DB, tenantID, jobID identity.ID) (database.UsageRecord, error) {
	var row database.UsageRecord
	err := db.WithContext(ctx).Where("tenant_id=? AND job_id=?", tenantID, jobID).First(&row).Error
	return row, err
}
