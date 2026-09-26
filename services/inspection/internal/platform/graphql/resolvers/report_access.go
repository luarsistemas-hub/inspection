package resolvers

import (
	"context"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func authorizeReportInspection(ctx context.Context, db *gorm.DB, meta requestctx.Metadata, inspectionID identity.ID) error {
	return withTask06Tenant(ctx, db, meta.TenantID, func(tx *gorm.DB) error {
		var inspection database.Inspection
		if err := tx.Where("id=?", inspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		if hasRole(meta, auth.TenantAdmin) {
			return nil
		}
		for _, scope := range meta.Principal.Scopes {
			if (scope.Kind == "BUSINESS_UNIT" && scope.ID == inspection.BusinessUnitID) ||
				(scope.Kind == "ASSET" && scope.ID == inspection.AssetID) ||
				(scope.Kind == "PROJECT" && inspection.ProjectID != nil && scope.ID == *inspection.ProjectID) ||
				(scope.Kind == "INSPECTION" && scope.ID == inspection.ID) {
				return nil
			}
		}
		return apperror.New(apperror.Forbidden, "", "access denied")
	})
}
