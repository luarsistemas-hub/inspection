package resolvers

import (
	"context"
	"errors"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

// authorizeReportInspection checks that the caller can read the inspection.
// Missing inspections return NOT_FOUND and out-of-scope ones FORBIDDEN, both on
// the inspectionId field; callers that must not reveal existence use
// authorizeReportResource instead.
func authorizeReportInspection(ctx context.Context, db *gorm.DB, meta requestctx.Metadata, inspectionID identity.ID) error {
	return withTask06Tenant(ctx, db, meta.TenantID, func(tx *gorm.DB) error {
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", meta.TenantID, inspectionID).Take(&inspection).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.NotFound, "inspectionId", "Vistoria não encontrada.")
			}
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
		return apperror.New(apperror.Forbidden, "inspectionId", "Você não tem acesso a esta vistoria.")
	})
}

// authorizeReportResource hides whether a report resource exists: missing and
// out-of-scope inspections both return the same NOT_FOUND error on the field of
// the argument the caller received (inspectionId, snapshotId, preparationId).
func authorizeReportResource(ctx context.Context, db *gorm.DB, meta requestctx.Metadata, inspectionID identity.ID, field, message string) error {
	err := authorizeReportInspection(ctx, db, meta, inspectionID)
	if apperror.Is(err, apperror.Forbidden) || apperror.Is(err, apperror.NotFound) {
		return apperror.New(apperror.NotFound, field, message)
	}
	return err
}
