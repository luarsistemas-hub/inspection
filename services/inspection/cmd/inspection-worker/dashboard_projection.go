package main

import (
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// upsertDashboardInspection preserves the projection identity on replay while
// refreshing its disposable view from the authoritative inspection state.
func upsertDashboardInspection(tx *gorm.DB, row database.DashboardInspection) error {
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "inspection_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"project_id", "asset_id", "classification", "status", "invalidated", "sequence", "updated_at",
		}),
	}).Create(&row).Error
}
