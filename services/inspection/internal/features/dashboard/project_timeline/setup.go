package project_timeline

import (
	"context"
	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
)

func Query(ctx context.Context, db *gorm.DB, tenantID, projectID identity.ID) ([]database.ProjectStage, error) {
	var rows []database.ProjectStage
	err := db.WithContext(ctx).Where("tenant_id=? AND project_id=?", tenantID, projectID).Order("position ASC").Find(&rows).Error
	return rows, err
}
