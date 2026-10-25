package auth

import (
	"context"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

// ReviewerMembershipMatchesScopes validates the shared role and scope policy for
// capture reviewers. A tenant administrator is global; other accepted roles
// must match at least one resource scope supplied by the caller.
func ReviewerMembershipMatchesScopes(ctx context.Context, tx *gorm.DB, tenantID, membershipID identity.ID, allowed []requestctx.Scope) (bool, error) {
	var membership database.Membership
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND status='ACTIVE'", tenantID, membershipID).Take(&membership).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, nil
		}
		return false, err
	}
	if membership.Role != Employee && membership.Role != Manager && membership.Role != TenantAdmin {
		return false, nil
	}
	if membership.Role == TenantAdmin {
		return true, nil
	}
	var scopes []database.ResourceScope
	if err := tx.WithContext(ctx).Where("tenant_id=? AND membership_id=?", tenantID, membershipID).Find(&scopes).Error; err != nil {
		return false, err
	}
	for _, actual := range scopes {
		for _, candidate := range allowed {
			if actual.Kind == candidate.Kind && actual.ResourceID == candidate.ID {
				return true, nil
			}
		}
	}
	return false, nil
}
