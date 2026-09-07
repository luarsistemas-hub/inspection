package configure_policy

import (
	"context"
	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"time"
)

type Input struct {
	TenantID                                    identity.ID
	EvidenceDays, OperationalDays, SecurityDays int
}

func Save(ctx context.Context, db *gorm.DB, in Input) (database.RetentionPolicy, error) {
	if in.EvidenceDays <= 0 || in.OperationalDays <= 0 || in.SecurityDays < 0 {
		return database.RetentionPolicy{}, gorm.ErrInvalidData
	}
	now := time.Now().UTC()
	row := database.RetentionPolicy{ID: identity.NewID(), TenantID: in.TenantID, EvidenceDays: in.EvidenceDays, OperationalDays: in.OperationalDays, SecurityDays: in.SecurityDays, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
		return database.RetentionPolicy{}, err
	}
	return row, nil
}
