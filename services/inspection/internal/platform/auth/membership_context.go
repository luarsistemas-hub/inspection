package auth

import (
	"context"
	"net/http"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/requestctx"
)

const MembershipHeader = "X-Inspection-Membership-ID"

// MembershipContextResolver validates one explicit membership selection.
type MembershipContextResolver interface {
	ResolveMembership(context.Context, string, string, identity.ID) (requestctx.Principal, error)
}

// RequireMembershipContext derives tenant metadata only from a membership
// selected by the verified subject. It deliberately returns one denial for
// malformed, foreign, disabled and inactive selections.
func RequireMembershipContext(next http.Handler, resolver MembershipContextResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadata, ok := requestctx.FromContext(r.Context())
		if !ok || metadata.Principal.Issuer == "" || metadata.Principal.Subject == "" || resolver == nil {
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}
		membershipID, err := identity.ParseID(strings.TrimSpace(r.Header.Get(MembershipHeader)))
		if err != nil {
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}
		principal, err := resolver.ResolveMembership(r.Context(), metadata.Principal.Issuer, metadata.Principal.Subject, membershipID)
		if err != nil || principal.Disabled {
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}
		metadata.TenantID = principal.TenantID
		metadata.Principal = principal
		if metadata.StartedAt.IsZero() {
			metadata.StartedAt = time.Now().UTC()
		}
		next.ServeHTTP(w, r.WithContext(requestctx.WithMetadata(r.Context(), metadata)))
	})
}
