package summary

import (
	"context"
	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/dashboard/core"
	"inspection/services/inspection/internal/platform/database"
)

type Input struct {
	TenantID  identity.ID
	ProjectID *identity.ID
}

func Query(ctx context.Context, db *gorm.DB, in Input) (core.Summary, error) {
	query := db.WithContext(ctx).Where("tenant_id=? AND invalidated=false", in.TenantID)
	if in.ProjectID != nil {
		query = query.Where("project_id=?", *in.ProjectID)
	}
	var rows []database.DashboardInspection
	if err := query.Find(&rows).Error; err != nil {
		return core.Summary{}, err
	}
	projections := make([]core.Projection, 0, len(rows))
	for _, row := range rows {
		projections = append(projections, core.Projection{InspectionID: row.InspectionID.String(), Classification: row.Classification, Invalidated: row.Invalidated})
	}
	return core.Summarize(projections), nil
}
