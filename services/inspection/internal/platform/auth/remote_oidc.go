package auth

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
)

// RemoteVerifier verifies signed ID or access tokens against the configured
// OpenID Connect issuer discovery document and JWKS. The audience check is
// performed by the library before any claim reaches application code.
type RemoteVerifier struct {
	verifiers map[string]*oidc.IDTokenVerifier
}

// NewRemoteVerifier discovers issuer keys once during process startup. jwksURL
// is optional for a public browser issuer whose signing keys must be fetched
// through a private container address. The expected issuer remains unchanged.
func NewRemoteVerifier(ctx context.Context, issuer string, configured interface{}, jwksURL string) (*RemoteVerifier, error) {
	audiences := []string{}
	switch value := configured.(type) {
	case string:
		audiences = []string{value}
	case []string:
		audiences = append(audiences, value...)
	}
	if len(audiences) == 0 {
		return nil, fmt.Errorf("OIDC: missing audience")
	}
	verifiers := make(map[string]*oidc.IDTokenVerifier, len(audiences))
	if jwksURL != "" {
		keySet := oidc.NewRemoteKeySet(ctx, jwksURL)
		for _, audience := range audiences {
			verifiers[audience] = oidc.NewVerifier(issuer, keySet, &oidc.Config{ClientID: audience})
		}
		return &RemoteVerifier{verifiers: verifiers}, nil
	}
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	for _, audience := range audiences {
		verifiers[audience] = provider.Verifier(&oidc.Config{ClientID: audience})
	}
	return &RemoteVerifier{verifiers: verifiers}, nil
}

// Verify validates token signature, issuer, audience and expiry and exposes
// only the claims required by the authorization boundary.
func (v *RemoteVerifier) Verify(ctx context.Context, raw string) (OIDCClaims, error) {
	var last error
	for audience, verifier := range v.verifiers {
		token, err := verifier.Verify(ctx, raw)
		if err != nil {
			last = err
			continue
		}
		return OIDCClaims{Issuer: token.Issuer, Subject: token.Subject, Audience: audience, ExpiresAt: token.Expiry}, nil
	}
	return OIDCClaims{}, last
}
