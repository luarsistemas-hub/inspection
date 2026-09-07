package auth

import (
	"context"

	"github.com/coreos/go-oidc/v3/oidc"
)

// RemoteVerifier verifies signed ID or access tokens against the configured
// OpenID Connect issuer discovery document and JWKS. The audience check is
// performed by the library before any claim reaches application code.
type RemoteVerifier struct {
	verifier *oidc.IDTokenVerifier
	audience string
}

// NewRemoteVerifier discovers issuer keys once during process startup. jwksURL
// is optional for a public browser issuer whose signing keys must be fetched
// through a private container address. The expected issuer remains unchanged.
func NewRemoteVerifier(ctx context.Context, issuer, audience, jwksURL string) (*RemoteVerifier, error) {
	if jwksURL != "" {
		keySet := oidc.NewRemoteKeySet(ctx, jwksURL)
		return &RemoteVerifier{verifier: oidc.NewVerifier(issuer, keySet, &oidc.Config{ClientID: audience}), audience: audience}, nil
	}
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	return &RemoteVerifier{verifier: provider.Verifier(&oidc.Config{ClientID: audience}), audience: audience}, nil
}

// Verify validates token signature, issuer, audience and expiry and exposes
// only the claims required by the authorization boundary.
func (v *RemoteVerifier) Verify(ctx context.Context, raw string) (OIDCClaims, error) {
	token, err := v.verifier.Verify(ctx, raw)
	if err != nil {
		return OIDCClaims{}, err
	}
	return OIDCClaims{Issuer: token.Issuer, Subject: token.Subject, Audience: v.audience, ExpiresAt: token.Expiry}, nil
}
