package summary

import (
	"context"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/dashboard/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

type Input struct {
	TenantID       identity.ID
	ProjectID      *identity.ID
	BusinessUnitID *identity.ID
}

func Query(ctx context.Context, db *gorm.DB, in Input) (core.Summary, error) {
	query := db.WithContext(ctx).Where("dashboard.inspections.tenant_id=?", in.TenantID)
	if in.ProjectID != nil {
		query = query.Where("dashboard.inspections.project_id=?", *in.ProjectID)
	}
	if in.BusinessUnitID != nil {
		query = query.Where("i.business_unit_id=?", *in.BusinessUnitID)
	}
	var rows []database.DashboardInspection
	if err := query.Find(&rows).Error; err != nil {
		return core.Summary{}, err
	}
	projections := make([]core.Projection, 0, len(rows))
	for _, row := range rows {
		projections = append(projections, core.Projection{InspectionID: row.InspectionID.String(), Classification: row.Classification, Status: row.Status, Invalidated: row.Invalidated})
	}
	return core.Summarize(projections), nil
}
