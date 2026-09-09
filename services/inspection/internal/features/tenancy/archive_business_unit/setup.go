// Package archive_business_unit owns the reversible boundary that removes a unit from active selection.
package archive_business_unit

import (
	"context"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// Command archives one business unit using optimistic concurrency.
type Command struct {
	TenantID, BusinessUnitID identity.ID
	ExpectedVersion          int64
}

// Dependencies are the adapters required by this slice.
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
	Now        func() time.Time
}

// Setup registers this slice with the application mediator.
func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil {
		return fmt.Errorf("slice tenancy/archive_business_unit: missing dependency")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		return handle(ctx, deps, raw.(Command))
	})
}

func handle(ctx context.Context, deps Dependencies, cmd Command) (database.BusinessUnit, error) {
	if cmd.TenantID == (identity.ID{}) || cmd.BusinessUnitID == (identity.ID{}) || cmd.ExpectedVersion < 1 {
		return database.BusinessUnit{}, apperror.New(apperror.InvalidInput, "businessUnit", "business unit and expected version are required")
	}
	if _, err := deps.Authorizer.Authorize(ctx, cmd.TenantID, []string{auth.TenantAdmin, auth.OrganizationAdmin}, nil, true); err != nil {
		return database.BusinessUnit{}, err
	}
	var result database.BusinessUnit
	err := (tenanttx.Runner{DB: deps.DB}).Within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ? AND id = ?", cmd.TenantID, cmd.BusinessUnitID).First(&result).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return apperror.New(apperror.NotFound, "businessUnitId", "business unit not found")
			}
			return err
		}
		if result.Status == "ARCHIVED" {
			return nil
		}
		if result.Version != cmd.ExpectedVersion {
			return apperror.New(apperror.Conflict, "expectedVersion", "stale business unit version")
		}
		result.Status = "ARCHIVED"
		result.Version++
		result.UpdatedAt = deps.Now().UTC()
		return tx.Model(&database.BusinessUnit{}).Where("tenant_id = ? AND id = ? AND version = ?", cmd.TenantID, cmd.BusinessUnitID, cmd.ExpectedVersion).Updates(map[string]any{"status": result.Status, "version": result.Version, "updated_at": result.UpdatedAt}).Error
	})
	if err != nil {
		return database.BusinessUnit{}, err
	}
	return result, nil
}
