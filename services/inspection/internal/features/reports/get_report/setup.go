// Package get_report reads one immutable report snapshot of an inspection.
package get_report

import (
	"context"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// Input selects a report. Version nil means the newest version; a requested
// version that does not exist is reported as not found.
type Input struct {
	TenantID, InspectionID identity.ID
	Version                *int
}

// Dependencies carries the database handle. The slice always filters by the
// tenant in Input, so it stays tenant safe with or without row-level security.
type Dependencies struct{ DB *gorm.DB }

// Setup returns the query. A missing or foreign report is gorm.ErrRecordNotFound,
// which callers map to the same stable "not found" answer so one tenant can
// never learn whether another tenant's report exists.
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
