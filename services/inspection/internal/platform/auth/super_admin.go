package auth

import (
	"context"
	"strings"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/requestctx"
)

// AuthorizeSuperAdmin checks the configured super-admin identity and its
// active administrative membership in the selected tenant context.
func AuthorizeSuperAdmin(ctx context.Context, authorizer Authorizer, tenantID identity.ID, issuer, subject string) (requestctx.Principal, error) {
	principal, err := authorizer.Authorize(ctx, AuthorizationRequest{
		TenantID: tenantID,
		Product:  AdminProduct,
		Roles:    []string{TenantAdmin, InspectionConfigAdmin},
	})
	if err != nil {
		return requestctx.Principal{}, err
	}
	meta, ok := requestctx.FromContext(ctx)
	if !ok || !strings.EqualFold(strings.TrimSpace(meta.Principal.Issuer), strings.TrimSpace(issuer)) || meta.Principal.Subject != subject {
		return requestctx.Principal{}, apperror.New(apperror.Forbidden, "", "access denied")
	}
	return principal, nil
}

// IsConfiguredSuperAdmin reports whether the authenticated OIDC identity is
// the configured super-admin identity. Membership and product authorization
// remain separate checks at protected operation boundaries.
func IsConfiguredSuperAdmin(ctx context.Context, issuer, subject string) bool {
	meta, ok := requestctx.FromContext(ctx)
	return ok && strings.EqualFold(strings.TrimSpace(meta.Principal.Issuer), strings.TrimSpace(issuer)) && meta.Principal.Subject == subject
}
