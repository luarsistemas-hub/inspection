package promote_inspection_to_origin

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// Reconcile resumes ready or terminal drafts and stale ACTIVATING operations after worker restarts.
func Reconcile(ctx context.Context, db *gorm.DB, deps ProcessDependencies) error {
	now := time.Now().UTC()
	if deps.Now != nil {
		now = deps.Now().UTC()
	}
	var tenants []identity.ID
	var batchErr error
	if err := db.WithContext(ctx).Raw("SELECT discovered.tenant_id FROM platform.discover_inspection_origin_promotion_tenants(?) AS discovered(tenant_id)", now.Add(-2*time.Minute)).Scan(&tenants).Error; err != nil {
		return err
	}
	for _, tenant := range tenants {
		var ids []identity.ID
		err := (tenanttx.Runner{DB: db}).Within(ctx, tenant, func(tx *gorm.DB) error {
			return tx.Model(&database.InspectionOriginPromotion{}).Where("tenant_id=? AND ((status='PREPARING' AND draft_version_id IN (SELECT id FROM origins.origin_versions WHERE tenant_id=? AND (status='DISCARDED' OR (status='DRAFT' AND draft_state IN ('READY','FAILED'))))) OR (status='ACTIVATING' AND updated_at<=?))", tenant, tenant, now.Add(-2*time.Minute)).Pluck("id", &ids).Error
		})
		if err != nil {
			// One unreachable tenant must not stall reconciliation for the others.
			slog.ErrorContext(ctx, "inspection origin promotion discovery failed", "tenantId", tenant.String(), "error", err)
			batchErr = errors.Join(batchErr, err)
			continue
		}
		for _, id := range ids {
			if err := Process(ctx, db, deps, tenant, id); err != nil {
				slog.ErrorContext(ctx, "inspection origin promotion reconciliation failed", "promotionId", id.String(), "tenantId", tenant.String(), "error", err)
				batchErr = errors.Join(batchErr, err)
			}
		}
	}
	return batchErr
}
