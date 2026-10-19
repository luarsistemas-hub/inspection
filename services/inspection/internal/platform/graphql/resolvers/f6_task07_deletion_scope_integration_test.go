//go:build integration

package resolvers

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestVV_IT_328DeletionRequestMixingInspectionAndForeignSourceIsInvalidInput(t *testing.T) {
	fx := newAPI14RetentionFixture(t)
	ctx := context.Background()
	// Inspection X belongs to the same tenant; source C1 belongs to inspection Y.
	x := identity.NewID()
	if err := fx.h.WithinTenant(ctx, fx.tenantID, func(tx *gorm.DB) error {
		var y database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", fx.tenantID, fx.inspectionID).Take(&y).Error; err != nil {
			return err
		}
		other := y
		other.ID, other.SourceKey = x, "vv-it-328-x-"+x.String()
		return tx.Create(&other).Error
	}); err != nil {
		t.Fatal(err)
	}
	query := api14RetentionMutation("recordDeletionRequest", x.String(), fx.mediaID.String(), "", "vv-it-328")
	assertFixedOriginTransportError(t, executeAPI14Retention(t, fx, query, fx.admin, "", ""), "INVALID_INPUT")
	assertAPI14RetentionNoWrites(t, fx)
	if err := fx.h.WithinTenant(ctx, fx.tenantID, func(tx *gorm.DB) error {
		var source database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=?", fx.tenantID, fx.mediaID).Take(&source).Error; err != nil {
			return err
		}
		if source.Status != "RETAINED" || source.OriginalDeletedAt != nil || source.SourceExpiryAt == nil || !source.SourceExpiryAt.Equal(fx.sourceExpiry) {
			t.Errorf("Y source changed by rejected mixed-scope request: status=%s deleted=%v expiry=%v", source.Status, source.OriginalDeletedAt, source.SourceExpiryAt)
		}
		var inspections int64
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id IN ?", fx.tenantID, []identity.ID{x, fx.inspectionID}).Count(&inspections).Error; err != nil {
			return err
		}
		if inspections != 2 {
			t.Errorf("inspections X/Y after rejected request=%d, want 2", inspections)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
