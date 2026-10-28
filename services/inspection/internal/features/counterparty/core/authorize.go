package core

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

// InternalRoles are the Dashboard roles allowed to resolve contestations and resend invitations.
var InternalRoles = []string{auth.TenantAdmin, auth.Manager, auth.Employee}

// AuthorizeInvitationAsset checks the caller's role before revealing whether the
// invitation exists, then checks the caller's scope over the invitation's asset.
func (s Service) AuthorizeInvitationAsset(ctx context.Context, authorizer auth.Authorizer, tenantID, invitationID identity.ID) error {
	if _, err := authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: tenantID, Product: auth.DashboardProduct, Roles: InternalRoles, Mutate: true}); err != nil {
		return err
	}
	var assetID identity.ID
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var invitation database.CounterpartyInvitation
		if err := tx.Select("origin_version_id").Where("tenant_id=? AND id=?", tenantID, invitationID).First(&invitation).Error; err != nil {
			return err
		}
		var version database.OriginVersion
		if err := tx.Select("origin_id").Where("tenant_id=? AND id=?", tenantID, invitation.OriginVersionID).First(&version).Error; err != nil {
			return err
		}
		var origin database.Origin
		if err := tx.Select("asset_id").Where("tenant_id=? AND id=?", tenantID, version.OriginID).First(&origin).Error; err != nil {
			return err
		}
		assetID = origin.AssetID
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperror.New(apperror.NotFound, "counterpartyInvitationId", "invitation not found")
		}
		return apperror.Wrap(apperror.DependencyUnavailable, err)
	}
	_, err = authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: tenantID, Product: auth.DashboardProduct, Roles: InternalRoles, Mutate: true, Resource: &requestctx.Scope{Kind: "ASSET", ID: assetID}})
	return err
}
