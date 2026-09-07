package list_triage

import (
	"context"
	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
)

type Input struct {
	TenantID               identity.ID
	Classification, Status string
	Limit                  int
}

func Query(ctx context.Context, db *gorm.DB, in Input) ([]database.DashboardInspection, error) {
	limit := in.Limit
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	query := db.WithContext(ctx).Where("tenant_id=? AND invalidated=false", in.TenantID)
	if in.Classification != "" {
		query = query.Where("classification=?", in.Classification)
	}
	if in.Status != "" {
		query = query.Where("status=?", in.Status)
	}
	var rows []database.DashboardInspection
	err := query.Order("updated_at DESC, inspection_id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}
