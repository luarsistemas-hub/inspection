package activate_version

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/origins/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

type Command struct{ TenantID, VersionID identity.ID }
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Service    core.Service
	Authorizer auth.Authorizer
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil || d.Service.DB == nil {
		return fmt.Errorf("slice origins/activate_version: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		var version database.OriginVersion
		if err := (tenanttx.Runner{DB: d.DB}).Within(ctx, command.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=?", command.TenantID, command.VersionID).First(&version).Error
		}); err != nil {
			return nil, apperror.New(apperror.NotFound, "versionId", "origin version not found")
		}
		var origin database.Origin
		if err := (tenanttx.Runner{DB: d.DB}).Within(ctx, command.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=?", command.TenantID, version.OriginID).First(&origin).Error
		}); err != nil {
			return nil, apperror.New(apperror.NotFound, "versionId", "origin version not found")
		}
		var asset database.Asset
		if err := (tenanttx.Runner{DB: d.DB}).Within(ctx, command.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=?", command.TenantID, origin.AssetID).First(&asset).Error
		}); err != nil {
			return nil, apperror.New(apperror.NotFound, "versionId", "origin version not found")
		}
		if _, err := d.Authorizer.Authorize(ctx, command.TenantID, []string{auth.TenantAdmin, auth.OrganizationAdmin, auth.Manager, auth.Employee}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: asset.BusinessUnitID}, true); err != nil {
			return nil, err
		}
		return d.Service.ActivateCompleted(ctx, command.TenantID, version.ResponsibilityID)
	})
}
