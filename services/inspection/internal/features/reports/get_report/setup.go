package get_report

import (
	"context"
	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
)

type Input struct {
	TenantID, InspectionID identity.ID
	Version                *int
}
type Dependencies struct{ DB *gorm.DB }

func Setup(deps Dependencies) (func(context.Context, Input) (database.ReportSnapshot, error), error) {
	if deps.DB == nil {
		return nil, gorm.ErrInvalidDB
	}
	return func(ctx context.Context, in Input) (database.ReportSnapshot, error) {
		query := deps.DB.WithContext(ctx).Where("tenant_id=? AND inspection_id=?", in.TenantID, in.InspectionID).Order("version_number DESC")
		if in.Version != nil {
			query = query.Where("version_number=?", *in.Version)
		}
		var row database.ReportSnapshot
		err := query.First(&row).Error
		return row, err
	}, nil
}
