package resolvers

import (
	"context"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

func (r *queryResolver) customerPortfolio(ctx context.Context, filter *graphql1.CustomerPortfolioFilter, first *int, after *string) (*graphql1.CustomerPortfolioConnection, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	principal, err := r.Authorizer.Authorize(ctx, auth.AuthorizationRequest{
		TenantID: meta.TenantID, Product: auth.DashboardProduct,
		Roles: []string{auth.OrganizationAdmin, auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer, auth.CustomerViewer},
	})
	if err != nil {
		return nil, err
	}
	if !containsInventoryRole(principal.Roles, auth.CustomerViewer) && !containsInventoryRole(principal.Roles, auth.TenantAdmin) && !containsInventoryRole(principal.Roles, auth.OrganizationAdmin) {
		return nil, apperror.New(apperror.Forbidden, "", "customer portfolio is only available to customer viewers")
	}
	limit, err := pageLimit(first)
	if err != nil {
		return nil, err
	}
	var cursor identity.ID
	if after != nil && *after != "" {
		cursor, err = identity.ParseID(*after)
		if err != nil {
			return nil, graphql1Error("after")
		}
	}
	var requestedAsset identity.ID
	if filter != nil && filter.AssetID != nil && *filter.AssetID != "" {
		requestedAsset, err = identity.ParseID(*filter.AssetID)
		if err != nil {
			return nil, invalidID("assetId")
		}
	}
	search := ""
	if filter != nil && filter.Search != nil {
		search = strings.TrimSpace(*filter.Search)
	}
	var assets []database.Asset
	var more bool
	progressByAsset := make(map[identity.ID]int)
	type publishedReport struct {
		AssetID        identity.ID `gorm:"column:asset_id"`
		InspectionID   identity.ID `gorm:"column:inspection_id"`
		Classification string      `gorm:"column:classification"`
	}
	publishedByAsset := make(map[identity.ID]publishedReport)
	err = (tenanttx.Runner{DB: r.DB}).Within(ctx, meta.TenantID, func(tx *gorm.DB) error {
		query := tx.Model(&database.Asset{}).Where("assets.tenant_id = ? AND assets.status <> ?", meta.TenantID, "ARCHIVED").Where(`(
			EXISTS (SELECT 1 FROM origins.origins o JOIN origins.origin_versions ov ON ov.tenant_id=o.tenant_id AND ov.origin_id=o.id AND ov.id=o.active_version_id WHERE o.tenant_id=assets.tenant_id AND o.asset_id=assets.id AND ov.has_inventory=true AND ov.status='ACTIVE')
			OR EXISTS (SELECT 1 FROM reports.report_publications rp JOIN inspections.inspections i ON i.tenant_id=rp.tenant_id AND i.id=rp.inspection_id WHERE rp.tenant_id=assets.tenant_id AND i.asset_id=assets.id AND rp.status='PUBLISHED')
		)`)
		if containsInventoryRole(principal.Roles, auth.TenantAdmin) || containsInventoryRole(principal.Roles, auth.OrganizationAdmin) {
			// Tenant administrators can see every active asset in their tenant.
		} else {
			var grants []database.ResourceScope
			if err := tx.Where("tenant_id = ? AND membership_id = ?", meta.TenantID, principal.MembershipID).Find(&grants).Error; err != nil {
				return err
			}
			assetIDs := make([]identity.ID, 0, len(grants))
			unitIDs := make([]identity.ID, 0, len(grants))
			for _, grant := range grants {
				switch grant.Kind {
				case "ASSET":
					assetIDs = append(assetIDs, grant.ResourceID)
				case "BUSINESS_UNIT":
					unitIDs = append(unitIDs, grant.ResourceID)
				}
			}
			for _, scope := range principal.Scopes {
				if scope.Kind == "ASSET" {
					assetIDs = append(assetIDs, scope.ID)
				} else if scope.Kind == "BUSINESS_UNIT" {
					unitIDs = append(unitIDs, scope.ID)
				}
			}
			if len(assetIDs) == 0 && len(unitIDs) == 0 {
				return nil
			}
			if len(assetIDs) > 0 && len(unitIDs) > 0 {
				query = query.Where("(assets.id IN ? OR assets.business_unit_id IN ?)", assetIDs, unitIDs)
			} else if len(assetIDs) > 0 {
				query = query.Where("assets.id IN ?", assetIDs)
			} else {
				query = query.Where("assets.business_unit_id IN ?", unitIDs)
			}
		}
		if requestedAsset != (identity.ID{}) {
			query = query.Where("assets.id = ?", requestedAsset)
		}
		if cursor != (identity.ID{}) {
			query = query.Where("assets.id > ?", cursor)
		}
		if search != "" {
			like := "%" + strings.ToLower(search) + "%"
			query = query.Where("(LOWER(assets.name) LIKE ? OR LOWER(assets.external_key) LIKE ?)", like, like)
		}
		if filter != nil && filter.ProjectID != nil && *filter.ProjectID != "" {
			projectID, parseErr := identity.ParseID(*filter.ProjectID)
			if parseErr != nil {
				return invalidID("projectId")
			}
			query = query.Where("EXISTS (SELECT 1 FROM projects.projects WHERE projects.projects.tenant_id = assets.tenant_id AND projects.projects.asset_id = assets.id AND projects.projects.id = ?)", projectID)
		}
		if err := query.Order("assets.id ASC").Limit(limit + 1).Find(&assets).Error; err != nil {
			return err
		}
		type progressRow struct {
			AssetID  identity.ID `gorm:"column:asset_id"`
			Total    int64       `gorm:"column:total"`
			Assessed int64       `gorm:"column:assessed"`
		}
		var progress []progressRow
		assetIDs := make([]identity.ID, 0, len(assets))
		for _, asset := range assets {
			assetIDs = append(assetIDs, asset.ID)
		}
		if len(assetIDs) > 0 {
			if err := tx.Table("origins.origins AS o").
				Select("o.asset_id, COUNT(i.id) AS total, COUNT(i.id) FILTER (WHERE i.decision IN ('ACCEPTED','EDITED','ADDED')) AS assessed").
				Joins("JOIN origins.origin_versions ov ON ov.tenant_id=o.tenant_id AND ov.id=o.active_version_id AND ov.has_inventory=true AND ov.status='ACTIVE'").
				Joins("JOIN origins.inventory_environments e ON e.tenant_id=ov.tenant_id AND e.origin_version_id=ov.id AND e.decision<>'REJECTED'").
				Joins("JOIN origins.inventory_items i ON i.tenant_id=e.tenant_id AND i.origin_version_id=e.origin_version_id AND i.environment_id=e.id AND i.merged_into_id IS NULL AND i.decision<>'REJECTED'").
				Where("o.tenant_id=? AND o.asset_id IN ?", meta.TenantID, assetIDs).Group("o.asset_id").Scan(&progress).Error; err != nil {
				return err
			}
		}
		for _, row := range progress {
			if row.Total > 0 {
				progressByAsset[row.AssetID] = int(row.Assessed * 100 / row.Total)
			}
		}
		if len(assetIDs) > 0 {
			var reports []publishedReport
			if err := tx.Table("reports.report_publications rp").
				Select("i.asset_id, rp.inspection_id, rs.classification").
				Joins("JOIN inspections.inspections i ON i.tenant_id=rp.tenant_id AND i.id=rp.inspection_id").
				Joins("JOIN reports.report_snapshots rs ON rs.tenant_id=rp.tenant_id AND rs.id=rp.snapshot_id").
				Where("rp.tenant_id=? AND rp.status='PUBLISHED' AND i.asset_id IN ?", meta.TenantID, assetIDs).
				Order("COALESCE(rp.published_at, rp.updated_at) DESC, rp.id DESC").Scan(&reports).Error; err != nil {
				return err
			}
			for _, report := range reports {
				if _, exists := publishedByAsset[report.AssetID]; !exists {
					publishedByAsset[report.AssetID] = report
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(assets) > limit {
		assets = assets[:limit]
		more = true
	}
	nodes := make([]*graphql1.CustomerPortfolioItem, 0, len(assets))
	for _, asset := range assets {
		item := &graphql1.CustomerPortfolioItem{AssetID: asset.ID.String(), AssetName: asset.Name, AssetExternalKey: asset.ExternalKey, Status: asset.Status, Progress: progressByAsset[asset.ID], UpdatedAt: asset.UpdatedAt.UTC().Format(time.RFC3339Nano)}
		if report, exists := publishedByAsset[asset.ID]; exists {
			inspectionID := report.InspectionID.String()
			classification := report.Classification
			item.LatestPublishedInspectionID = &inspectionID
			item.PublishedClassification = &classification
		}
		nodes = append(nodes, item)
	}
	end := ""
	if len(assets) > 0 {
		end = assets[len(assets)-1].ID.String()
	}
	return &graphql1.CustomerPortfolioConnection{Nodes: nodes, PageInfo: pageInfo(end, more)}, nil
}
