package auth

import (
	"context"
	"errors"
	"fmt"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/requestctx"
	"strings"
	"time"

	"gorm.io/gorm"
)

// OIDCClaims are accepted only after cryptographic verification by TokenVerifier.
type OIDCClaims struct {
	Issuer, Subject, Audience string
	ExpiresAt                 time.Time
}

// TokenVerifier is implemented by the configured OIDC adapter.
type TokenVerifier interface {
	Verify(context.Context, string) (OIDCClaims, error)
}

// OIDCResolver maps verified external identity to current local authorization.
type OIDCResolver interface {
	ResolveOIDC(context.Context, string, string) (requestctx.Principal, error)
}
type Authenticator struct {
	Verifier TokenVerifier
	Resolver OIDCResolver
	Audience string
	Now      func() time.Time
}

// Authenticate verifies the bearer token and reloads local membership state.
func (a Authenticator) Authenticate(ctx context.Context, authorization string) (requestctx.Principal, error) {
	if a.Verifier == nil || a.Resolver == nil {
		return requestctx.Principal{}, fmt.Errorf("OIDC: missing dependency")
	}
	parts := strings.SplitN(authorization, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
		return requestctx.Principal{}, apperror.New(apperror.Unauthenticated, "", "authentication required")
	}
	claims, err := a.Verifier.Verify(ctx, parts[1])
	if err != nil {
		return requestctx.Principal{}, apperror.New(apperror.Unauthenticated, "", "invalid authentication")
	}
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}
	if claims.Issuer == "" || claims.Subject == "" || claims.Audience != a.Audience || !claims.ExpiresAt.After(now) {
		return requestctx.Principal{}, apperror.New(apperror.Unauthenticated, "", "invalid authentication")
	}
	principal, err := a.Resolver.ResolveOIDC(ctx, claims.Issuer, claims.Subject)
	if err != nil {
		// A valid OIDC principal without a local membership may execute only the
		// tenant-bootstrap mutation. It has no tenant, roles, scopes, or identity
		// ID, so every ordinary tenant operation remains denied by its authorizer.
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return requestctx.Principal{Issuer: claims.Issuer, Subject: claims.Subject}, nil
		}
		return requestctx.Principal{}, apperror.New(apperror.Unauthenticated, "", "invalid authentication")
	}
	if principal.Disabled {
		return requestctx.Principal{}, apperror.New(apperror.Forbidden, "", "access denied")
	}
	return principal, nil
}
