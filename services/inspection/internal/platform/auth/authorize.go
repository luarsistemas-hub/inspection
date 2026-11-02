package auth

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/requestctx"
)

const (
	TenantAdmin           = "TENANT_ADMIN"
	Manager               = "MANAGER"
	Employee              = "EMPLOYEE"
	Viewer                = "VIEWER"
	CustomerViewer        = "CUSTOMER_VIEWER"
	OrganizationAdmin     = "ORGANIZATION_ADMIN"
	AccessAdmin           = "ACCESS_ADMIN"
	ParticipationAdmin    = "PARTICIPATION_ADMIN"
	InspectionConfigAdmin = "INSPECTION_CONFIG_ADMIN"
	GovernanceAdmin       = "GOVERNANCE_ADMIN"
	Auditor               = "AUDITOR"
	AdminProduct          = "ADMIN"
	DashboardProduct      = "DASHBOARD"
)

// IsKnownRole reports whether role is part of the supported membership model.
func IsKnownRole(role string) bool {
	switch role {
	case TenantAdmin, Manager, Employee, Viewer, CustomerViewer,
		OrganizationAdmin, AccessAdmin, ParticipationAdmin,
		InspectionConfigAdmin, GovernanceAdmin, Auditor:
		return true
	default:
		return false
	}
}

// ProductsForRole returns the products granted to a newly provisioned role.
func ProductsForRole(role string) []string {
	switch role {
	case TenantAdmin:
		return []string{AdminProduct, DashboardProduct}
	case OrganizationAdmin, AccessAdmin, ParticipationAdmin, InspectionConfigAdmin, GovernanceAdmin, Auditor:
		return []string{AdminProduct}
	default:
		return []string{DashboardProduct}
	}
}

// CanDelegateRole reports whether a current membership may grant candidate.
// Tenant administrators retain full authority; access administrators may grant
// every delegated and operational role but cannot create another tenant admin.
func CanDelegateRole(grantorRoles []string, candidate string) bool {
	if !IsKnownRole(candidate) {
		return false
	}
	if contains(grantorRoles, TenantAdmin) {
		return true
	}
	if contains(grantorRoles, AccessAdmin) {
		return candidate != TenantAdmin
	}
	if contains(grantorRoles, Manager) {
		return candidate == Manager || candidate == Employee || candidate == Viewer || candidate == CustomerViewer
	}
	return false
}

// AuthorizationRequest makes product, role, mutation, tenant, and resource
// policy explicit at each protected boundary.
type AuthorizationRequest struct {
	TenantID identity.ID
	Product  string
	Roles    []string
	Mutate   bool
	NewWork  bool
	Resource *requestctx.Scope
}

// ScopeDecision describes the explicit grant responsible for an effective
// resource decision.
type ScopeDecision struct {
	Allowed     bool
	DirectGrant requestctx.Scope
	Target      requestctx.Scope
}

type ScopeResolver interface {
	Resolve(context.Context, identity.ID, identity.ID, requestctx.Scope) (ScopeDecision, error)
}

type mutationTargetValidator interface {
	ValidateMutationTarget(context.Context, identity.ID, requestctx.Scope) (bool, error)
}

type newWorkScopeResolver interface {
	ResolveNewWork(context.Context, identity.ID, identity.ID, requestctx.Scope) (ScopeDecision, bool, error)
}

// ProductAuthorizer is the authorization boundary used by feature slices.
// The variadic compatibility shape permits existing slices to migrate without
// weakening the new AuthorizationRequest contract.
type ProductAuthorizer interface {
	Authorize(context.Context, interface{}, ...interface{}) (requestctx.Principal, error)
}

// MembershipStore resolves current local state on every protected action.
type MembershipStore interface {
	Resolve(context.Context, identity.ID, identity.ID) (requestctx.Principal, error)
}

// Authorizer enforces current role and hierarchical resource scope.
type Authorizer struct {
	Store  MembershipStore
	Scopes ScopeResolver
}

// Authorize accepts the new AuthorizationRequest and the legacy argument
// shape while existing feature slices migrate to explicit product metadata.
func (a Authorizer) Authorize(ctx context.Context, first interface{}, legacy ...interface{}) (requestctx.Principal, error) {
	req, err := authorizationRequest(first, legacy...)
	if err != nil {
		return requestctx.Principal{}, err
	}
	tenantID := req.TenantID
	metadata, ok := requestctx.FromContext(ctx)
	if !ok || metadata.Principal.IdentityID == (identity.ID{}) {
		return requestctx.Principal{}, apperror.New(apperror.Unauthenticated, "", "Autenticação necessária.")
	}
	if metadata.TenantID != tenantID || metadata.Principal.TenantID != tenantID {
		return requestctx.Principal{}, apperror.New(apperror.NotFound, "", "Recurso não encontrado.")
	}
	principal := metadata.Principal
	if a.Store != nil && !principal.Internal {
		var err error
		principal, err = a.Store.Resolve(ctx, tenantID, principal.IdentityID)
		if err != nil {
			return requestctx.Principal{}, fmt.Errorf("resolve membership: %w", err)
		}
		// Product and audience come from the verified token context. The store
		// reloads mutable membership state, but must not erase that boundary.
		principal.Audience = metadata.Principal.Audience
		principal.Product = metadata.Principal.Product
	}
	if principal.Disabled {
		return requestctx.Principal{}, apperror.New(apperror.Forbidden, "", "Acesso negado.")
	}
	roleAllowed := false
	for _, actual := range principal.Roles {
		for _, allowed := range req.Roles {
			if actual == allowed {
				roleAllowed = true
			}
		}
	}
	// A caller that does not name a product (legacy shape) is still bound to the
	// product of the verified token audience, so the entitlement behind that
	// audience is reloaded and enforced on every protected action. Requests
	// without an audience (internal orchestration, local integration auth)
	// keep their role-only contract.
	//
	// System principals are the exception: server-side orchestration (the
	// scheduler, onboarding and external-session reads) is marked Internal and
	// has no product to enter.
	entitledProduct := req.Product
	if entitledProduct == "" {
		entitledProduct = productForAudience(principal.Audience)
	}
	if !isSystemPrincipal(principal) {
		if entitledProduct != "" && !principal.ProductEntitled(entitledProduct) {
			return requestctx.Principal{}, apperror.New(apperror.Forbidden, "", "Acesso negado.")
		}
		if principal.Audience != "" && req.Product != "" && productForAudience(principal.Audience) != req.Product {
			return requestctx.Principal{}, apperror.New(apperror.Forbidden, "", "Acesso negado.")
		}
		if principal.Product != "" && req.Product != "" && principal.Product != req.Product {
			return requestctx.Principal{}, apperror.New(apperror.Forbidden, "", "Acesso negado.")
		}
	}
	if !roleAllowed || (req.Mutate && (contains(principal.Roles, Viewer) || contains(principal.Roles, CustomerViewer))) {
		return requestctx.Principal{}, apperror.New(apperror.Forbidden, "", "Acesso negado.")
	}
	product := req.Product
	if product == "" {
		product = productForAudience(principal.Audience)
	}
	legacyTenantAdmin := contains(principal.Roles, TenantAdmin) && (product == AdminProduct || product == DashboardProduct || metadata.Principal.Audience == "")
	var newWorkDecision *ScopeDecision
	if req.NewWork && req.Resource != nil && a.Scopes != nil {
		if resolver, ok := a.Scopes.(newWorkScopeResolver); ok && !legacyTenantAdmin {
			decision, archived, err := resolver.ResolveNewWork(ctx, tenantID, principal.MembershipID, *req.Resource)
			if err != nil {
				return requestctx.Principal{}, fmt.Errorf("resolve new-work scope: %w", err)
			}
			if archived || !decision.Allowed {
				return requestctx.Principal{}, apperror.New(apperror.NotFound, "", "Recurso não encontrado.")
			}
			newWorkDecision = &decision
		} else if validator, ok := a.Scopes.(mutationTargetValidator); ok {
			archived, err := validator.ValidateMutationTarget(ctx, tenantID, *req.Resource)
			if err != nil {
				return requestctx.Principal{}, fmt.Errorf("validate new-work scope: %w", err)
			}
			if archived {
				return requestctx.Principal{}, apperror.New(apperror.NotFound, "", "Recurso não encontrado.")
			}
		}
	}
	if req.Resource != nil && !legacyTenantAdmin {
		if a.Scopes != nil {
			if newWorkDecision != nil {
				return principal, nil
			}
			decision, err := a.Scopes.Resolve(ctx, tenantID, principal.MembershipID, *req.Resource)
			if err != nil {
				return requestctx.Principal{}, fmt.Errorf("resolve scope: %w", err)
			}
			if !decision.Allowed {
				return requestctx.Principal{}, apperror.New(apperror.NotFound, "", "Recurso não encontrado.")
			}
			return principal, nil
		}
		matched := false
		for _, assigned := range principal.Scopes {
			if assigned.Kind == req.Resource.Kind && assigned.ID == req.Resource.ID {
				matched = true
				break
			}
		}
		if !matched {
			return requestctx.Principal{}, apperror.New(apperror.NotFound, "", "Recurso não encontrado.")
		}
	}
	return principal, nil
}

// isSystemPrincipal reports a server-side orchestration principal. Only the
// explicit Internal marker, which is never derived from HTTP input, exempts a
// principal from product entitlement: a principal that merely lacks an
// audience, a membership and entitlements gets no exemption.
func isSystemPrincipal(principal requestctx.Principal) bool {
	return principal.Internal
}

func authorizationRequest(first interface{}, legacy ...interface{}) (AuthorizationRequest, error) {
	if request, ok := first.(AuthorizationRequest); ok {
		return request, nil
	}
	tenant, ok := first.(identity.ID)
	if !ok || len(legacy) != 3 {
		return AuthorizationRequest{}, fmt.Errorf("authorization: invalid request")
	}
	roles, ok := legacy[0].([]string)
	if !ok {
		return AuthorizationRequest{}, fmt.Errorf("authorization: invalid roles")
	}
	var scope *requestctx.Scope
	if legacy[1] != nil {
		value, valid := legacy[1].(*requestctx.Scope)
		if !valid {
			return AuthorizationRequest{}, fmt.Errorf("authorization: invalid scope")
		}
		scope = value
	}
	mutate, ok := legacy[2].(bool)
	if !ok {
		return AuthorizationRequest{}, fmt.Errorf("authorization: invalid mutation flag")
	}
	return AuthorizationRequest{TenantID: tenant, Roles: roles, Resource: scope, Mutate: mutate}, nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
