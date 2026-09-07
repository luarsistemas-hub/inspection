package list_versions

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

type Query struct {
	TenantID, AssetID identity.ID
	First             int
	After             string
}
type Result struct {
	Versions    []database.OriginVersion
	EndCursor   string
	HasNextPage bool
}
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice origins/list_versions: missing dependency")
	}
	return d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		query := raw.(Query)
		assetRaw, err := d.Bus.Ask(ctx, assetget.Query{TenantID: query.TenantID, AssetID: query.AssetID})
		if err != nil {
			return nil, err
		}
		asset := assetRaw.(assetcore.View)
		if _, err := d.Authorizer.Authorize(ctx, query.TenantID, []string{auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: asset.Asset.BusinessUnitID}, false); err != nil {
			return nil, err
		}
		first := query.First
		if first <= 0 {
			first = 25
		}
		if first > 100 {
			return nil, apperror.New(apperror.InvalidInput, "first", "page size must not exceed 100")
		}
		var rows []database.OriginVersion
		err = (tenanttx.Runner{DB: d.DB}).Within(ctx, query.TenantID, func(tx *gorm.DB) error {
			statement := tx.Model(&database.OriginVersion{}).Joins("JOIN origins.origins o ON o.id=origins.origin_versions.origin_id AND o.tenant_id=origins.origin_versions.tenant_id").Where("origins.origin_versions.tenant_id=? AND o.asset_id=?", query.TenantID, query.AssetID)
			if query.After != "" {
				cursorID, err := identity.ParseID(query.After)
				if err != nil {
					return apperror.New(apperror.InvalidInput, "after", "invalid origin cursor")
				}
				var cursor database.OriginVersion
				if err := statement.Session(&gorm.Session{}).Where("origins.origin_versions.id=?", cursorID).First(&cursor).Error; err != nil {
					return apperror.New(apperror.InvalidInput, "after", "origin cursor not found")
				}
				if cursor.ActivatedAt == nil {
					statement = statement.Where("origins.origin_versions.activated_at IS NULL AND (origins.origin_versions.created_at,origins.origin_versions.id) < (?,?)", cursor.CreatedAt, cursor.ID)
				} else {
					statement = statement.Where("origins.origin_versions.activated_at IS NULL OR (origins.origin_versions.activated_at,origins.origin_versions.id) < (?,?)", cursor.ActivatedAt, cursor.ID)
				}
			}
			return statement.Order("origins.origin_versions.activated_at DESC NULLS LAST, CASE WHEN origins.origin_versions.activated_at IS NULL THEN origins.origin_versions.created_at END DESC, origins.origin_versions.id DESC").Limit(first + 1).Find(&rows).Error
		})
		if err != nil {
			return nil, err
		}
		result := Result{Versions: rows}
		if len(rows) > first {
			result.HasNextPage = true
			result.Versions = rows[:first]
		}
		if len(result.Versions) > 0 {
			result.EndCursor = result.Versions[len(result.Versions)-1].ID.String()
		}
		return result, nil
	})
}
