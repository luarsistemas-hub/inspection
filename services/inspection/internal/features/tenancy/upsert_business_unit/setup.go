// Package upsert_business_unit owns creation and optimistic updates of business units.
package upsert_business_unit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// Command creates a unit when BusinessUnitID is nil and updates it otherwise.
type Command struct {
	TenantID, BusinessUnitID   identity.ID
	HasBusinessUnitID          bool
	Code, Name, IdempotencyKey string
	ExpectedVersion            int64
	ExpectedTenantVersion      int64
}

// Dependencies are the concrete adapters assembled by the composition root.
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
	MaxUnits   int
	Now        func() time.Time
}

// Setup registers this slice with the application mediator.
func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil {
		return fmt.Errorf("slice tenancy/upsert_business_unit: missing dependency")
	}
	if deps.MaxUnits <= 0 {
		deps.MaxUnits = 1000
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		return handle(ctx, deps, raw.(Command))
	})
}

func handle(ctx context.Context, deps Dependencies, cmd Command) (database.BusinessUnit, error) {
	cmd.Code = strings.TrimSpace(cmd.Code)
	cmd.Name = strings.TrimSpace(cmd.Name)
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	if cmd.TenantID == (identity.ID{}) || cmd.Code == "" || cmd.Name == "" || cmd.IdempotencyKey == "" {
		return database.BusinessUnit{}, apperror.New(apperror.InvalidInput, "businessUnit", "tenant, code, name, and client mutation id are required")
	}
	if len([]rune(cmd.Code)) > 200 || len([]rune(cmd.Name)) > 200 {
		return database.BusinessUnit{}, apperror.New(apperror.InvalidInput, "businessUnit", "code and name are too long")
	}
	if cmd.HasBusinessUnitID && cmd.BusinessUnitID == (identity.ID{}) {
		return database.BusinessUnit{}, apperror.New(apperror.InvalidInput, "businessUnitId", "invalid identifier")
	}
	if !cmd.HasBusinessUnitID && cmd.ExpectedTenantVersion < 1 {
		return database.BusinessUnit{}, apperror.New(apperror.InvalidInput, "expectedTenantVersion", "expected tenant version is required")
	}
	if cmd.HasBusinessUnitID && cmd.ExpectedVersion < 1 {
		return database.BusinessUnit{}, apperror.New(apperror.InvalidInput, "expectedVersion", "expected business unit version is required")
	}
	if _, err := deps.Authorizer.Authorize(ctx, cmd.TenantID, []string{auth.TenantAdmin, auth.OrganizationAdmin}, nil, true); err != nil {
		return database.BusinessUnit{}, err
	}

	var result database.BusinessUnit
	err := (tenanttx.Runner{DB: deps.DB}).Within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		if !cmd.HasBusinessUnitID {
			var prior database.BusinessUnit
			lookup := tx.Where("tenant_id = ? AND idempotency_key = ?", cmd.TenantID, cmd.IdempotencyKey).First(&prior)
			if lookup.Error == nil {
				if prior.Code != cmd.Code || prior.Name != cmd.Name {
					return apperror.New(apperror.Conflict, "clientMutationId", "mutation id was already used with different input")
				}
				result = prior
				return nil
			}
			if lookup.Error != gorm.ErrRecordNotFound {
				return lookup.Error
			}
			var activeCount int64
			if err := tx.Model(&database.BusinessUnit{}).Where("tenant_id = ? AND status <> ?", cmd.TenantID, "ARCHIVED").Count(&activeCount).Error; err != nil {
				return err
			}
			if activeCount >= int64(deps.MaxUnits) {
				return apperror.New(apperror.InvalidState, "businessUnits", "business unit limit reached")
			}
			version := tx.Model(&database.Tenant{}).Where("tenant_id = ? AND version = ?", cmd.TenantID, cmd.ExpectedTenantVersion).Updates(map[string]any{"version": cmd.ExpectedTenantVersion + 1, "updated_at": deps.Now().UTC()})
			if version.Error != nil {
				return version.Error
			}
			if version.RowsAffected != 1 {
				return apperror.New(apperror.Conflict, "expectedTenantVersion", "stale tenant version")
			}
			result = database.BusinessUnit{ID: identity.NewID(), TenantID: cmd.TenantID, Code: cmd.Code, Name: cmd.Name, Status: "ACTIVE", Version: 1, IdempotencyKey: cmd.IdempotencyKey, CreatedAt: deps.Now().UTC(), UpdatedAt: deps.Now().UTC()}
			return tx.Create(&result).Error
		}

		if err := tx.Where("tenant_id = ? AND id = ?", cmd.TenantID, cmd.BusinessUnitID).First(&result).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return apperror.New(apperror.NotFound, "businessUnitId", "business unit not found")
			}
			return err
		}
		if result.Status == "ARCHIVED" {
			return apperror.New(apperror.InvalidState, "businessUnitId", "archived business unit cannot be edited")
		}
		if result.Version != cmd.ExpectedVersion {
			if result.Code == cmd.Code && result.Name == cmd.Name {
				return nil
			}
			return apperror.New(apperror.Conflict, "expectedVersion", "stale business unit version")
		}
		if cmd.ExpectedTenantVersion > 0 {
			version := tx.Model(&database.Tenant{}).Where("tenant_id = ? AND version = ?", cmd.TenantID, cmd.ExpectedTenantVersion).Updates(map[string]any{"version": cmd.ExpectedTenantVersion + 1, "updated_at": deps.Now().UTC()})
			if version.Error != nil {
				return version.Error
			}
			if version.RowsAffected != 1 {
				return apperror.New(apperror.Conflict, "expectedTenantVersion", "stale tenant version")
			}
		}
		result.Code, result.Name = cmd.Code, cmd.Name
		result.Version++
		result.IdempotencyKey = cmd.IdempotencyKey
		result.UpdatedAt = deps.Now().UTC()
		return tx.Model(&database.BusinessUnit{}).Where("tenant_id = ? AND id = ? AND version = ?", cmd.TenantID, cmd.BusinessUnitID, cmd.ExpectedVersion).Updates(map[string]any{"code": result.Code, "name": result.Name, "version": result.Version, "idempotency_key": result.IdempotencyKey, "updated_at": result.UpdatedAt}).Error
	})
	if err != nil {
		return database.BusinessUnit{}, err
	}
	return result, nil
}
