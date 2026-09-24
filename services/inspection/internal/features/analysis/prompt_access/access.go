package prompt_access

import (
	"context"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

// Authorize applies the product role and the configured super-admin issuer and
// subject gate required by global prompt operations.
func Authorize(ctx context.Context, authorizer auth.Authorizer, tenantID identity.ID, issuer, subject string, mutate bool) (requestctx.Principal, error) {
	if mutate {
		principal, err := authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: tenantID, Product: auth.AdminProduct, Roles: []string{auth.TenantAdmin, auth.InspectionConfigAdmin}, Mutate: true})
		if err != nil {
			return requestctx.Principal{}, err
		}
		if !auth.IsConfiguredSuperAdmin(ctx, issuer, subject) {
			return requestctx.Principal{}, apperror.New(apperror.Forbidden, "", "access denied")
		}
		return principal, nil
	}
	return auth.AuthorizeSuperAdmin(ctx, authorizer, tenantID, issuer, subject)
}
