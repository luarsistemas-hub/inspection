package auth

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/requestctx"
)

// TestMetadataFromHeaders parses the explicit local integration principal.
// The API enables this parser only when INSPECTION_ENV=test and
// INSPECTION_TEST_AUTH_ENABLED=true; it is never an authentication path in
// production environments.
func TestMetadataFromHeaders(r *http.Request) (requestctx.Metadata, error) {
	if r == nil {
		return requestctx.Metadata{}, fmt.Errorf("test authentication: missing request")
	}
	tenantID, err := identity.ParseID(strings.TrimSpace(r.Header.Get("X-Inspection-Test-Tenant")))
	if err != nil {
		return requestctx.Metadata{}, fmt.Errorf("test authentication: invalid tenant")
	}
	identityID, err := identity.ParseID(strings.TrimSpace(r.Header.Get("X-Inspection-Test-Identity")))
	if err != nil {
		return requestctx.Metadata{}, fmt.Errorf("test authentication: invalid identity")
	}
	membershipID, err := identity.ParseID(strings.TrimSpace(r.Header.Get("X-Inspection-Test-Membership")))
	if err != nil {
		return requestctx.Metadata{}, fmt.Errorf("test authentication: invalid membership")
	}
	roles := splitHeader(r.Header.Get("X-Inspection-Test-Roles"))
	if len(roles) == 0 {
		return requestctx.Metadata{}, fmt.Errorf("test authentication: roles are required")
	}
	correlationID := strings.TrimSpace(r.Header.Get("X-Correlation-ID"))
	if correlationID == "" {
		correlationID = "test-" + identity.NewID().String()
	}
	return requestctx.Metadata{
		TenantID:      tenantID,
		CorrelationID: correlationID,
		StartedAt:     time.Now().UTC(),
		Principal: requestctx.Principal{
			IdentityID:        identityID,
			MembershipID:      membershipID,
			MembershipVersion: 1,
			TenantID:          tenantID,
			Issuer:            "task06-test",
			Subject:           identityID.String(),
			Roles:             roles,
		},
	}, nil
}

func splitHeader(value string) []string {
	parts := strings.Split(value, ",")
	roles := make([]string, 0, len(parts))
	for _, part := range parts {
		if role := strings.TrimSpace(part); role != "" {
			roles = append(roles, role)
		}
	}
	return roles
}
