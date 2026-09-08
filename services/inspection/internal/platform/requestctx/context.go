package requestctx

import (
	"context"
	"net/http"
	"time"

	"inspection/libs/identity"
)

// Principal is the authenticated local identity used for authorization.
type Principal struct {
	IdentityID          identity.ID
	MembershipID        identity.ID
	MembershipVersion   int64
	TenantID            identity.ID
	Issuer              string
	Subject             string
	Audience            string
	Product             string
	ProductEntitlements []string
	Roles               []string
	Scopes              []Scope
	Disabled            bool
}

// Scope limits a principal to one resource subtree.
type Scope struct {
	Kind string
	ID   identity.ID
}

// Metadata carries security and correlation state through every in-process call.
type Metadata struct {
	TenantID      identity.ID
	Principal     Principal
	CorrelationID string
	CausationID   string
	StartedAt     time.Time
}

// ProductEntitled reports whether the current membership may enter a product.
func (p Principal) ProductEntitled(product string) bool {
	for _, entitlement := range p.ProductEntitlements {
		if entitlement == product {
			return true
		}
	}
	return false
}

type key struct{}
type idempotencyKey struct{}
type responseWriterKey struct{}
type externalCredentialsKey struct{}

type ExternalCredentials struct {
	SessionToken string
	CSRFToken    string
}

// WithMetadata attaches request metadata without replacing cancellation or deadlines.
func WithMetadata(ctx context.Context, metadata Metadata) context.Context {
	return context.WithValue(ctx, key{}, metadata)
}

// FromContext returns request metadata when it was established at the boundary.
func FromContext(ctx context.Context) (Metadata, bool) {
	metadata, ok := ctx.Value(key{}).(Metadata)
	return metadata, ok
}

func WithIdempotencyKey(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, idempotencyKey{}, value)
}
func IdempotencyKey(ctx context.Context, fallback string) string {
	value, _ := ctx.Value(idempotencyKey{}).(string)
	if value != "" {
		return value
	}
	return fallback
}

func WithResponseWriter(ctx context.Context, writer http.ResponseWriter) context.Context {
	return context.WithValue(ctx, responseWriterKey{}, writer)
}
func ResponseWriter(ctx context.Context) (http.ResponseWriter, bool) {
	writer, ok := ctx.Value(responseWriterKey{}).(http.ResponseWriter)
	return writer, ok
}

func WithExternalCredentials(ctx context.Context, credentials ExternalCredentials) context.Context {
	return context.WithValue(ctx, externalCredentialsKey{}, credentials)
}

func ExternalCredentialsFromContext(ctx context.Context) (ExternalCredentials, bool) {
	credentials, ok := ctx.Value(externalCredentialsKey{}).(ExternalCredentials)
	return credentials, ok && credentials.SessionToken != ""
}
