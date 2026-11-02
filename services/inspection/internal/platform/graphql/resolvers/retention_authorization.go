package resolvers

import (
	"context"

	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

// authorizeRetention enforces who may use the retention policy operations
// (ADR-003 of graphql-frontend-capability-parity). The Admin product is
// required: the tenant administrator and the governance administrator may read
// and change the policy, and the auditor may only read it.
func (r *Resolver) authorizeRetention(ctx context.Context, meta requestctx.Metadata, mutate bool) (requestctx.Principal, error) {
	roles := []string{auth.TenantAdmin, auth.GovernanceAdmin}
	if !mutate {
		roles = append(roles, auth.Auditor)
	}
	return r.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: meta.TenantID, Product: auth.AdminProduct, Roles: roles, Mutate: mutate})
}
