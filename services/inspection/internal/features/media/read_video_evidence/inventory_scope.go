package read_video_evidence

import (
	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

var inventoryReviewerRoles = []string{auth.TenantAdmin, auth.OrganizationAdmin, auth.Manager, auth.Employee}

// inventoryFrameVisible applies the inventory visibility of each role to one
// cited frame: reviewers read any version, viewers only versions that left
// draft, and customers only included items of the asset's active version.
func inventoryFrameVisible(roles []string, version database.OriginVersion, origin database.Origin, decisions []string) bool {
	for _, role := range inventoryReviewerRoles {
		if hasRole(roles, role) {
			return true
		}
	}
	if hasRole(roles, auth.Viewer) {
		return version.Status != "DRAFT"
	}
	if !hasRole(roles, auth.CustomerViewer) || origin.ActiveVersionID == nil || *origin.ActiveVersionID != version.ID {
		return false
	}
	for _, decision := range decisions {
		if decision == "ACCEPTED" || decision == "EDITED" || decision == "ADDED" {
			return true
		}
	}
	return false
}

// inventoryPrincipalCoversAsset applies the inventory scope rule to a
// retained frame: tenant administrators see every asset; everyone else needs
// a grant on the asset itself or on its business unit, from the token or from
// persisted resource scopes, exactly as the inventory resolvers do.
func inventoryPrincipalCoversAsset(tx *gorm.DB, tenant identity.ID, principal requestctx.Principal, asset database.Asset) (bool, error) {
	if hasRole(principal.Roles, auth.TenantAdmin) {
		return true, nil
	}
	for _, scope := range principal.Scopes {
		if coversAsset(scope.Kind, scope.ID, asset) {
			return true, nil
		}
	}
	if principal.MembershipID == (identity.ID{}) {
		return false, nil
	}
	var grants []database.ResourceScope
	if err := tx.Where("tenant_id=? AND membership_id=?", tenant, principal.MembershipID).Find(&grants).Error; err != nil {
		return false, err
	}
	for _, grant := range grants {
		if coversAsset(grant.Kind, grant.ResourceID, asset) {
			return true, nil
		}
	}
	return false, nil
}

func coversAsset(kind string, resource identity.ID, asset database.Asset) bool {
	return (kind == "ASSET" && resource == asset.ID) || (kind == "BUSINESS_UNIT" && resource == asset.BusinessUnitID)
}
