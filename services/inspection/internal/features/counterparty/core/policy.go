package core

import (
	"context"
	"errors"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DefaultDeadlineDays is the contestation window used until a tenant configures one.
const DefaultDeadlineDays = 5

// Policy returns the tenant setting or the product default when it has not been configured.
func (s Service) Policy(ctx context.Context, tenantID identity.ID) (database.BaselinePolicy, error) {
	row := database.BaselinePolicy{TenantID: tenantID, ContestationDeadlineDays: DefaultDeadlineDays}
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		err := tx.Where("tenant_id=?", tenantID).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	})
	return row, err
}

// UpdatePolicy upserts the tenant's immutable default for future invitations only.
func (s Service) UpdatePolicy(ctx context.Context, tenantID identity.ID, days int) (database.BaselinePolicy, error) {
	if err := validateDeadlineDays(days); err != nil {
		return database.BaselinePolicy{}, err
	}
	row := database.BaselinePolicy{TenantID: tenantID, ContestationDeadlineDays: days, Version: 1, UpdatedAt: s.now()}
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "tenant_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"contestation_deadline_days": days,
				"version":                    gorm.Expr("origins.baseline_policies.version + 1"),
				"updated_at":                 row.UpdatedAt,
			}),
		}).Create(&row).Error
	})
	if err == nil {
		err = s.within(ctx, tenantID, func(tx *gorm.DB) error { return tx.Where("tenant_id=?", tenantID).First(&row).Error })
	}
	return row, err
}

func validateDeadlineDays(days int) error {
	if days < 1 || days > 90 {
		return apperror.New(apperror.InvalidInput, "contestationDeadlineDays", "deadline must be between 1 and 90 days")
	}
	return nil
}
