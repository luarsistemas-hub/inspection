package list_deliveries

import (
	"context"
	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
)

func Query(ctx context.Context, db *gorm.DB, tenantID identity.ID, limit int) ([]database.Delivery, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	var rows []database.Delivery
	err := db.WithContext(ctx).Where("tenant_id=?", tenantID).Order("created_at DESC").Limit(limit).Find(&rows).Error
	return rows, err
}
