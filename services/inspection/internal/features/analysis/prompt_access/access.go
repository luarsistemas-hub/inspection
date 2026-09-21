package prompt_access

import (
	"context"
	"strings"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

// Authorize applies the product role and the configured super-admin issuer and
// subject gate required by global prompt operations.
func Authorize(ctx context.Context, authorizer auth.Authorizer, tenantID identity.ID, issuer, subject string, mutate bool) (requestctx.Principal, error) {
	principal, err := authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: tenantID, Product: auth.AdminProduct, Roles: []string{auth.TenantAdmin, auth.InspectionConfigAdmin}, Mutate: mutate})
	if err != nil {
		return requestctx.Principal{}, err
	}
	meta, ok := requestctx.FromContext(ctx)
	if !ok || !strings.EqualFold(strings.TrimSpace(meta.Principal.Issuer), strings.TrimSpace(issuer)) || meta.Principal.Subject != subject {
		return requestctx.Principal{}, apperror.New(apperror.Forbidden, "", "access denied")
	}
	return principal, nil
}
