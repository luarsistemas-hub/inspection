package list_triage

import (
	"context"
	"inspection/libs/identity"
	dashboardcore "inspection/services/inspection/internal/features/dashboard/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

type Input struct {
	TenantID               identity.ID
	Classification, Status string
	After                  string
	Limit                  int
}

func Query(ctx context.Context, db *gorm.DB, in Input) ([]database.DashboardInspection, error) {
	limit := in.Limit
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	query := db.WithContext(ctx).Where("dashboard.inspections.tenant_id=? AND dashboard.inspections.invalidated=false", in.TenantID)
	if in.After != "" {
		bucket, updatedAt, inspectionID, err := dashboardcore.DecodeTriageCursor(in.After)
		if err != nil {
			legacyTime, legacyID, legacyErr := dashboardcore.DecodeCursor(in.After)
			if legacyErr != nil {
				return nil, err
			}
			bucket, updatedAt, inspectionID = 0, legacyTime, legacyID
		}
		prioritySQL := "CASE dashboard.inspections.classification WHEN 'CRITICAL' THEN 0 WHEN 'ATTENTION' THEN 1 ELSE 2 END"
		query = query.Where("("+prioritySQL+" > ?) OR ("+prioritySQL+" = ? AND ((dashboard.inspections.updated_at < ?) OR (dashboard.inspections.updated_at = ? AND dashboard.inspections.inspection_id < ?)))", bucket, bucket, updatedAt, updatedAt, inspectionID)
	}
	if in.Classification != "" {
		query = query.Where("dashboard.inspections.classification=?", in.Classification)
	}
	if in.Status != "" {
		query = query.Where("dashboard.inspections.status=?", in.Status)
	}
	var rows []database.DashboardInspection
	err := query.Order("CASE dashboard.inspections.classification WHEN 'CRITICAL' THEN 0 WHEN 'ATTENTION' THEN 1 ELSE 2 END ASC, dashboard.inspections.updated_at DESC, dashboard.inspections.inspection_id DESC").Limit(limit + 1).Find(&rows).Error
	return rows, err
}
