package auth

import (
	"inspection/services/inspection/internal/platform/apperror"
)

// IsAdministrativeRole reports whether role is tenant administration or one of
// the delegated administrative roles that ADR-003 entitles to the Admin product.
func IsAdministrativeRole(role string) bool {
	switch role {
	case TenantAdmin, OrganizationAdmin, AccessAdmin, ParticipationAdmin,
		InspectionConfigAdmin, GovernanceAdmin, Auditor:
		return true
	default:
		return false
	}
}

// RoleMayHoldProduct reports whether a membership with role may be entitled to
// product. Only tenant administrators may hold both products; delegated
// administrative roles are Admin-only; operational and customer roles are
// Dashboard-only, so a customer can never receive Admin authority.
func RoleMayHoldProduct(role, product string) bool {
	switch product {
	case AdminProduct:
		return IsAdministrativeRole(role)
	case DashboardProduct:
		return role == TenantAdmin || (IsKnownRole(role) && !IsAdministrativeRole(role))
	default:
		return false
	}
}

// NormalizeProductEntitlements validates the explicit product entitlements
// requested for role. An empty request selects the role default. The result is
// deduplicated in stable product order (ADMIN, DASHBOARD).
func NormalizeProductEntitlements(role string, requested []string) ([]string, error) {
	if !IsKnownRole(role) {
		return nil, apperror.New(apperror.InvalidInput, "role", "O perfil informado não é válido.")
	}
	if len(requested) == 0 {
		return ProductsForRole(role), nil
	}
	selected := map[string]bool{}
	for _, product := range requested {
		if product != AdminProduct && product != DashboardProduct {
			return nil, apperror.New(apperror.InvalidInput, "productEntitlements", "O produto informado não é válido.")
		}
		if !RoleMayHoldProduct(role, product) {
			return nil, apperror.New(apperror.InvalidInput, "productEntitlements", "Este perfil não pode receber acesso a esse produto.")
		}
		selected[product] = true
	}
	normalized := make([]string, 0, len(selected))
	for _, product := range []string{AdminProduct, DashboardProduct} {
		if selected[product] {
			normalized = append(normalized, product)
		}
	}
	return normalized, nil
}
