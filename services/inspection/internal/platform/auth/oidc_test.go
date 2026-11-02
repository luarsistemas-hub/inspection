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

func multiAudienceAuthenticator(now time.Time, audience string) Authenticator {
	return Authenticator{Audiences: []string{"inspection-admin", "inspection-dashboard"}, Now: func() time.Time { return now }, Verifier: verifierFunc(func(context.Context, string) (OIDCClaims, error) {
		return OIDCClaims{Issuer: "issuer", Subject: "subject", Audience: audience, ExpiresAt: now.Add(time.Minute)}, nil
	}), Resolver: resolverFunc(func(context.Context, string, string) (requestctx.Principal, error) {
		return requestctx.Principal{IdentityID: identity.NewID()}, nil
	})}
}

func TestTFE_UT_050OIDCRejectsAudienceOutsideTheConfiguredSet(t *testing.T) {
	now := time.Now()
	for _, audience := range []string{"inspection-capture", "inspection", "", "inspection-admin ", "INSPECTION-ADMIN", "account"} {
		_, err := multiAudienceAuthenticator(now, audience).Authenticate(context.Background(), "Bearer verified")
		if code, _, _ := apperror.Public(err); code != apperror.Unauthenticated {
			t.Fatalf("audience %q: err=%v code=%s, want UNAUTHENTICATED", audience, err, code)
		}
	}
	// A missing audience configuration must never fall back to accepting everything.
	if _, err := (Authenticator{Now: func() time.Time { return now }, Verifier: verifierFunc(func(context.Context, string) (OIDCClaims, error) {
		return OIDCClaims{Issuer: "issuer", Subject: "subject", Audience: "inspection-admin", ExpiresAt: now.Add(time.Minute)}, nil
	}), Resolver: resolverFunc(func(context.Context, string, string) (requestctx.Principal, error) {
		return requestctx.Principal{}, nil
	})}).Authenticate(context.Background(), "Bearer verified"); err == nil {
		t.Fatal("an authenticator without a configured audience accepted a token")
	}
}

func TestTFE_UT_051OIDCPreservesVerifiedAudienceAndProductInRequestMetadata(t *testing.T) {
	now := time.Now()
	for audience, product := range map[string]string{"inspection-admin": AdminProduct, "inspection-dashboard": DashboardProduct} {
		principal, err := multiAudienceAuthenticator(now, audience).Authenticate(context.Background(), "Bearer verified")
		if err != nil || principal.Audience != audience || principal.Product != product || principal.Issuer != "issuer" || principal.Subject != "subject" {
			t.Fatalf("audience %q: principal=%+v err=%v", audience, principal, err)
		}
		// The verified audience survives the later current-state membership reload.
		ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: principal.TenantID, Principal: principal})
		metadata, ok := requestctx.FromContext(ctx)
		if !ok || metadata.Principal.Audience != audience || metadata.Principal.Product != product {
			t.Fatalf("request metadata lost the verified audience: %+v", metadata.Principal)
		}
	}
}
