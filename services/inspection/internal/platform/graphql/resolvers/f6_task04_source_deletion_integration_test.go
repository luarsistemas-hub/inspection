//go:build integration

package resolvers

import (
	"context"
	"testing"

	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestVV_IT_316OrdinaryViewerSourceDeletionIsForbidden(t *testing.T) {
	fx := newAPI14RetentionFixture(t)
	viewer := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.tenantID, Principal: requestctx.Principal{IdentityID: fx.identityID, TenantID: fx.tenantID, Roles: []string{auth.Viewer}}, CorrelationID: "vv-it-316"})
	query := api14RetentionMutation("recordDeletionRequest", "", fx.mediaID.String(), "", "vv-it-316-delete")
	assertFixedOriginTransportError(t, executeAPI14Retention(t, fx, query, viewer, "", ""), "FORBIDDEN")
	if err := fx.h.WithinTenant(context.Background(), fx.tenantID, func(tx *gorm.DB) error {
		var requests int64
		if err := tx.Model(&database.DeletionRequest{}).Where("tenant_id=?", fx.tenantID).Count(&requests).Error; err != nil {
			return err
		}
		var source database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=?", fx.tenantID, fx.mediaID).Take(&source).Error; err != nil {
			return err
		}
		if requests != 0 || source.Status != "RETAINED" || source.SourceExpiryAt == nil || !source.SourceExpiryAt.Equal(fx.sourceExpiry) {
			t.Errorf("forbidden deletion changed state: requests=%d source=%s expiry=%v", requests, source.Status, source.SourceExpiryAt)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestVV_IT_620LegalHoldWithInspectionAndSourceIsInvalidInput(t *testing.T) {
	fx := newAPI14RetentionFixture(t)
	query := `mutation { applyLegalHold(input:{inspectionId:"` + fx.inspectionID.String() + `",sourceMediaId:"` + fx.mediaID.String() + `",reason:"IT-620",clientMutationId:"vv-it-620"}) { status clientMutationId } }`
	assertFixedOriginTransportError(t, executeAPI14Retention(t, fx, query, fx.admin, "", ""), "INVALID_INPUT")
	assertAPI14RetentionNoWrites(t, fx)
}
