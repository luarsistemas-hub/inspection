package auth

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/requestctx"
)

const (
	TenantAdmin = "TENANT_ADMIN"
	Manager     = "MANAGER"
	Employee    = "EMPLOYEE"
	Viewer      = "VIEWER"
)

// MembershipStore resolves current local state on every protected action.
type MembershipStore interface {
	Resolve(context.Context, identity.ID, identity.ID) (requestctx.Principal, error)
}

// Authorizer enforces current role and hierarchical resource scope.
type Authorizer struct{ Store MembershipStore }

func (a Authorizer) Authorize(ctx context.Context, tenantID identity.ID, allowedRoles []string, scope *requestctx.Scope, mutate bool) (requestctx.Principal, error) {
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
		for _, allowed := range allowedRoles {
			if actual == allowed {
				roleAllowed = true
			}
		}
	}
	if !roleAllowed || (mutate && contains(principal.Roles, Viewer)) {
		return requestctx.Principal{}, apperror.New(apperror.Forbidden, "", "access denied")
	}
	if scope != nil && !contains(principal.Roles, TenantAdmin) {
		matched := false
		for _, assigned := range principal.Scopes {
			if assigned.Kind == scope.Kind && assigned.ID == scope.ID {
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

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
