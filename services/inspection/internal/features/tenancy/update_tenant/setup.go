// Package update_tenant owns the optimistic update of tenant-facing settings.
package update_tenant

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

// Command changes the mutable tenant configuration using its current version.
type Command struct {
	TenantID                 identity.ID
	Name, Language, Timezone string
	ExpectedVersion          int64
}

// Dependencies required by this vertical slice.
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
}

// Setup registers the update command.
func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil {
		return fmt.Errorf("slice tenancy/update_tenant: missing dependency")
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		return handle(ctx, deps, raw.(Command))
	})
}

func handle(ctx context.Context, deps Dependencies, cmd Command) (database.Tenant, error) {
	cmd.Name, cmd.Language, cmd.Timezone = strings.TrimSpace(cmd.Name), strings.TrimSpace(cmd.Language), strings.TrimSpace(cmd.Timezone)
	if cmd.TenantID == (identity.ID{}) || cmd.Name == "" || cmd.Language == "" || cmd.Timezone == "" || cmd.ExpectedVersion < 1 {
		return database.Tenant{}, apperror.New(apperror.InvalidInput, "tenant", "name, language, timezone, and expected version are required")
	}
	if len([]rune(cmd.Name)) > 200 {
		return database.Tenant{}, apperror.New(apperror.InvalidInput, "name", "name is too long")
	}
	if _, err := time.LoadLocation(cmd.Timezone); err != nil {
		return database.Tenant{}, apperror.New(apperror.InvalidInput, "timezone", "invalid timezone")
	}
	if _, err := deps.Authorizer.Authorize(ctx, cmd.TenantID, []string{auth.TenantAdmin, auth.OrganizationAdmin}, nil, true); err != nil {
		return database.Tenant{}, err
	}
	var result database.Tenant
	err := (tenanttx.Runner{DB: deps.DB}).Within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		update := tx.Model(&database.Tenant{}).Where("tenant_id=? AND version=?", cmd.TenantID, cmd.ExpectedVersion).Updates(map[string]any{
			"name": cmd.Name, "language": cmd.Language, "default_timezone": cmd.Timezone,
			"version": cmd.ExpectedVersion + 1, "updated_at": time.Now().UTC(),
		})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected == 1 {
			return tx.Where("tenant_id=?", cmd.TenantID).First(&result).Error
		}
		if err := tx.Where("tenant_id=?", cmd.TenantID).First(&result).Error; err != nil {
			return err
		}
		// Retrying a completed request with the old version is safe only when
		// the requested state is already the persisted state.
		if result.Name == cmd.Name && result.Language == cmd.Language && result.DefaultTimezone == cmd.Timezone {
			return nil
		}
		return apperror.New(apperror.Conflict, "version", "stale tenant version")
	})
	if err != nil {
		return database.Tenant{}, err
	}
	return result, nil
}
