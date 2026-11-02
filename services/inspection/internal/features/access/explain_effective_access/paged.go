package explain_effective_access

import (
	"context"
	"database/sql"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

// EffectiveAccessQuery lists the explicit grants of one membership and a
// cursor-paginated page of the descendants they currently reach.
type EffectiveAccessQuery struct {
	TenantID, MembershipID identity.ID
	First                  int
	After                  string
}

// EffectiveAccessResult is the Admin explanation of one membership's access.
type EffectiveAccessResult struct {
	MembershipID        identity.ID
	Role                string
	Status              string
	Version             int64
	ProductEntitlements []string
	// TenantWide is true for the tenant administrator role, whose authority
	// comes from the role and needs no explicit grant.
	TenantWide  bool
	Grants      []Resource
	Descendants Page
}

// ScopePreviewQuery previews what a new grant on one resource would reach.
type ScopePreviewQuery struct {
	TenantID   identity.ID
	Kind       string
	ResourceID identity.ID
	First      int
	After      string
}

// ScopePreviewResult is the grant target and a page of its current descendants.
type ScopePreviewResult struct {
	Target      Resource
	Descendants Page
}

var effectiveAccessRoles = []string{auth.TenantAdmin, auth.AccessAdmin, auth.Auditor}

func requestScope(scope Scope) requestctx.Scope {
	return requestctx.Scope{Kind: scope.Kind, ID: scope.ID}
}

func handleEffectiveAccess(ctx context.Context, deps Dependencies, query EffectiveAccessQuery) (EffectiveAccessResult, error) {
	if query.TenantID == (identity.ID{}) || query.MembershipID == (identity.ID{}) {
		return EffectiveAccessResult{}, apperror.New(apperror.InvalidInput, "membershipId", "Informe o acesso que será consultado.")
	}
	first, err := normalizePage(query.First)
	if err != nil {
		return EffectiveAccessResult{}, err
	}
	if _, _, err := decodeAfter(query.After); err != nil {
		return EffectiveAccessResult{}, err
	}
	if _, err := deps.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: query.TenantID, Product: auth.AdminProduct, Roles: effectiveAccessRoles}); err != nil {
		return EffectiveAccessResult{}, err
	}
	var result EffectiveAccessResult
	err = within(ctx, deps.DB, query.TenantID, func(tx *gorm.DB) error {
		var membership database.Membership
		if err := tx.Where("tenant_id = ? AND id = ?", query.TenantID, query.MembershipID).First(&membership).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return apperror.New(apperror.NotFound, "membershipId", "Acesso não encontrado.")
			}
			return err
		}
		result = EffectiveAccessResult{MembershipID: membership.ID, Role: membership.Role, Status: membership.Status, Version: membership.Version, TenantWide: membership.Role == auth.TenantAdmin}
		var entitlements []database.ProductEntitlement
		if err := tx.Where("tenant_id = ? AND membership_id = ?", query.TenantID, membership.ID).Order("product").Find(&entitlements).Error; err != nil {
			return err
		}
		result.ProductEntitlements = make([]string, 0, len(entitlements))
		for _, entitlement := range entitlements {
			result.ProductEntitlements = append(result.ProductEntitlements, entitlement.Product)
		}
		var grants []database.ResourceScope
		if err := tx.Where("tenant_id = ? AND membership_id = ?", query.TenantID, membership.ID).Order("kind, resource_id").Find(&grants).Error; err != nil {
			return err
		}
		result.Grants = make([]Resource, 0, len(grants))
		for _, grant := range grants {
			resource, found, err := loadResource(tx, query.TenantID, Scope{Kind: grant.Kind, ID: grant.ResourceID})
			if err != nil {
				return err
			}
			if !found {
				// The target no longer exists: keep the grant visible so it can be removed.
				resource = Resource{Kind: grant.Kind, ResourceID: grant.ResourceID, Status: "MISSING", Historical: true}
			}
			result.Grants = append(result.Grants, resource)
		}
		page, err := pageDescendants(tx, query.TenantID,
			`SELECT kind, resource_id FROM access.resource_scopes WHERE tenant_id=@tenant AND membership_id=@membership`,
			[]any{sql.Named("membership", membership.ID)}, first, query.After)
		if err != nil {
			return err
		}
		result.Descendants = page
		return nil
	})
	if err != nil {
		return EffectiveAccessResult{}, err
	}
	return result, nil
}

func handleScopePreview(ctx context.Context, deps Dependencies, query ScopePreviewQuery) (ScopePreviewResult, error) {
	if query.TenantID == (identity.ID{}) || query.ResourceID == (identity.ID{}) || !knownScope(query.Kind) {
		return ScopePreviewResult{}, apperror.New(apperror.InvalidInput, "kind", "O tipo de recurso informado não é válido.")
	}
	first, err := normalizePage(query.First)
	if err != nil {
		return ScopePreviewResult{}, err
	}
	if _, _, err := decodeAfter(query.After); err != nil {
		return ScopePreviewResult{}, err
	}
	if _, err := deps.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: query.TenantID, Product: auth.AdminProduct, Roles: []string{auth.TenantAdmin, auth.AccessAdmin}}); err != nil {
		return ScopePreviewResult{}, err
	}
	var result ScopePreviewResult
	err = within(ctx, deps.DB, query.TenantID, func(tx *gorm.DB) error {
		target, found, err := loadResource(tx, query.TenantID, Scope{Kind: query.Kind, ID: query.ResourceID})
		if err != nil {
			return err
		}
		if !found {
			return apperror.New(apperror.NotFound, "resourceId", "Recurso não encontrado.")
		}
		result.Target = target
		page, err := pageDescendants(tx, query.TenantID,
			`SELECT CAST(@grant_kind AS text) AS kind, CAST(@grant_id AS uuid) AS resource_id`,
			[]any{sql.Named("grant_kind", query.Kind), sql.Named("grant_id", query.ResourceID.String())}, first, query.After)
		if err != nil {
			return err
		}
		result.Descendants = page
		return nil
	})
	if err != nil {
		return ScopePreviewResult{}, err
	}
	return result, nil
}
