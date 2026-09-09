package auth

import (
	"context"
	"errors"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/requestctx"
	"testing"
	"time"

	"gorm.io/gorm"
)

type verifierFunc func(context.Context, string) (OIDCClaims, error)

func (f verifierFunc) Verify(c context.Context, s string) (OIDCClaims, error) { return f(c, s) }

type resolverFunc func(context.Context, string, string) (requestctx.Principal, error)

func (f resolverFunc) ResolveOIDC(c context.Context, i, s string) (requestctx.Principal, error) {
	return f(c, i, s)
}
func TestOIDCAuthenticationAndImmediateRevocation(t *testing.T) {
	now := time.Now()
	a := Authenticator{Audience: "inspection", Now: func() time.Time { return now }, Verifier: verifierFunc(func(context.Context, string) (OIDCClaims, error) {
		return OIDCClaims{Issuer: "issuer", Subject: "subject", Audience: "inspection", ExpiresAt: now.Add(time.Minute)}, nil
	}), Resolver: resolverFunc(func(context.Context, string, string) (requestctx.Principal, error) {
		return requestctx.Principal{IdentityID: identity.NewID()}, nil
	})}
	if _, err := a.Authenticate(context.Background(), "Bearer verified"); err != nil {
		t.Fatal(err)
	}
	a.Verifier = verifierFunc(func(context.Context, string) (OIDCClaims, error) { return OIDCClaims{}, errors.New("bad signature") })
	_, err := a.Authenticate(context.Background(), "Bearer secret")
	code, _, safe := apperror.Public(err)
	if code != apperror.Unauthenticated || safe == "secret" {
		t.Fatalf("unsafe error: %s %s", code, safe)
	}
}

func TestOIDCAuthenticationAllowsUnboundPrincipalOnlyForBootstrap(t *testing.T) {
	now := time.Now()
	a := Authenticator{Audience: "inspection", Now: func() time.Time { return now }, Verifier: verifierFunc(func(context.Context, string) (OIDCClaims, error) {
		return OIDCClaims{Issuer: "issuer", Subject: "subject", Audience: "inspection", ExpiresAt: now.Add(time.Minute)}, nil
	}), Resolver: resolverFunc(func(context.Context, string, string) (requestctx.Principal, error) {
		return requestctx.Principal{}, gorm.ErrRecordNotFound
	})}
	principal, err := a.Authenticate(context.Background(), "Bearer verified")
	if err != nil {
		t.Fatal(err)
	}
	if principal.Issuer != "issuer" || principal.Subject != "subject" || principal.IdentityID != (identity.ID{}) || len(principal.Roles) != 0 {
		t.Fatalf("unbound principal = %#v", principal)
	}
}
