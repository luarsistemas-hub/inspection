package auth

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/requestctx"
)

const (
	TenantAdmin      = "TENANT_ADMIN"
	Manager          = "MANAGER"
	Employee         = "EMPLOYEE"
	Viewer           = "VIEWER"
	CustomerViewer   = "CUSTOMER_VIEWER"
	AdminProduct     = "ADMIN"
	DashboardProduct = "DASHBOARD"
)

// AuthorizationRequest makes product, role, mutation, tenant, and resource
// policy explicit at each protected boundary.
type AuthorizationRequest struct {
	TenantID identity.ID
	Product  string
	Roles    []string
	Mutate   bool
	Resource *requestctx.Scope
	legacy   bool
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
		return requestctx.Principal{}, apperror.New(apperror.Unauthenticated, "", "authentication required")
	}
	if metadata.TenantID != tenantID || metadata.Principal.TenantID != tenantID {
		return requestctx.Principal{}, apperror.New(apperror.NotFound, "", "resource not found")
	}
	principal := metadata.Principal
	if a.Store != nil {
		var err error
		principal, err = a.Store.Resolve(ctx, tenantID, principal.IdentityID)
		if err != nil {
			return requestctx.Principal{}, fmt.Errorf("resolve membership: %w", err)
		}
	}
	if principal.Disabled {
		return requestctx.Principal{}, apperror.New(apperror.Forbidden, "", "access denied")
	}
	roleAllowed := false
	for _, actual := range principal.Roles {
		for _, allowed := range req.Roles {
			if actual == allowed {
				roleAllowed = true
			}
		}
	}
	if req.Product != "" && !principal.ProductEntitled(req.Product) && !contains(principal.ProductEntitlements, req.Product) {
		return requestctx.Principal{}, apperror.New(apperror.Forbidden, "", "access denied")
	}
	if principal.Product != "" && req.Product != "" && principal.Product != req.Product {
		return requestctx.Principal{}, apperror.New(apperror.Forbidden, "", "access denied")
	}
	if !roleAllowed || (req.Mutate && (contains(principal.Roles, Viewer) || contains(principal.Roles, CustomerViewer))) {
		return requestctx.Principal{}, apperror.New(apperror.Forbidden, "", "access denied")
	}
	if req.Resource != nil && (!contains(principal.Roles, TenantAdmin) || (!req.legacy && req.Product == AdminProduct)) {
		if a.Scopes != nil {
			decision, err := a.Scopes.Resolve(ctx, tenantID, principal.MembershipID, *req.Resource)
			if err != nil {
				return requestctx.Principal{}, fmt.Errorf("resolve scope: %w", err)
			}
			if !decision.Allowed {
				return requestctx.Principal{}, apperror.New(apperror.Forbidden, "", "access denied")
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
			return requestctx.Principal{}, apperror.New(apperror.Forbidden, "", "access denied")
		}
	}
	return principal, nil
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
	return AuthorizationRequest{TenantID: tenant, Roles: roles, Resource: scope, Mutate: mutate, legacy: true}, nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
