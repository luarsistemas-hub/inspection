package create_business_unit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

type Command struct {
	TenantID                   identity.ID
	Code, Name, IdempotencyKey string
	ExpectedTenantVersion      int64
}
type Dependencies struct {
	DB       *gorm.DB
	Bus      *mediator.Bus
	MaxUnits int
}

func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil {
		return fmt.Errorf("slice tenancy/create_business_unit: missing dependency")
	}
	if deps.MaxUnits <= 0 {
		deps.MaxUnits = 1000
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) { return handle(ctx, deps, raw.(Command)) })
}

func handle(ctx context.Context, deps Dependencies, cmd Command) (database.BusinessUnit, error) {
	if cmd.TenantID == (identity.ID{}) {
		return database.BusinessUnit{}, apperror.New(apperror.InvalidInput, "tenantId", "tenant is required")
	}
	cmd.Name = strings.TrimSpace(cmd.Name)
	cmd.Code = strings.TrimSpace(cmd.Code)
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	if cmd.Name == "" || cmd.Code == "" || cmd.IdempotencyKey == "" {
		return database.BusinessUnit{}, apperror.New(apperror.InvalidInput, "businessUnit", "name, code, and idempotency key are required")
	}
	if cmd.ExpectedTenantVersion < 1 {
		return database.BusinessUnit{}, apperror.New(apperror.InvalidInput, "expectedTenantVersion", "expected tenant version is required")
	}
	unit := database.BusinessUnit{ID: identity.NewID(), TenantID: cmd.TenantID, Code: cmd.Code, Name: cmd.Name, Status: "ACTIVE", Version: 1, IdempotencyKey: cmd.IdempotencyKey, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	err := (tenanttx.Runner{DB: deps.DB}).Within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		var prior database.BusinessUnit
		if err := tx.Where("tenant_id = ? AND idempotency_key = ?", cmd.TenantID, cmd.IdempotencyKey).First(&prior).Error; err == nil {
			if prior.Code != cmd.Code || prior.Name != cmd.Name {
				return apperror.New(apperror.Conflict, "idempotencyKey", "idempotency key was already used with different input")
			}
			unit = prior
			return nil
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		// Creating a unit changes the tenant's administrative configuration. The
		// root version makes concurrent dashboards fail deterministically instead
		// of both accepting an out-of-date configuration.
		versionUpdate := tx.Model(&database.Tenant{}).
			Where("tenant_id = ? AND version = ?", cmd.TenantID, cmd.ExpectedTenantVersion).
			Updates(map[string]any{"version": cmd.ExpectedTenantVersion + 1, "updated_at": time.Now().UTC()})
		if versionUpdate.Error != nil {
			return versionUpdate.Error
		}
		if versionUpdate.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "stale tenant version")
		}
		var count int64
		if err := tx.Model(&database.BusinessUnit{}).Where("tenant_id = ? AND status <> ?", cmd.TenantID, "ARCHIVED").Count(&count).Error; err != nil {
			return err
		}
		if count >= int64(deps.MaxUnits) {
			return apperror.New(apperror.InvalidState, "businessUnits", "business unit limit reached")
		}
		return tx.Create(&unit).Error
	})
	if err != nil {
		return database.BusinessUnit{}, err
	}
	return unit, nil
}
