package auth

import (
	"net/http/httptest"
	"testing"

	"inspection/libs/identity"
)

func TestTestMetadataFromHeaders(t *testing.T) {
	tenantID, identityID, membershipID := identity.NewID(), identity.NewID(), identity.NewID()
	req := httptest.NewRequest("POST", "/graphql", nil)
	req.Header.Set("X-Inspection-Test-Tenant", tenantID.String())
	req.Header.Set("X-Inspection-Test-Identity", identityID.String())
	req.Header.Set("X-Inspection-Test-Membership", membershipID.String())
	req.Header.Set("X-Inspection-Test-Roles", "TENANT_ADMIN, VIEWER")
	metadata, err := TestMetadataFromHeaders(req)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.TenantID != tenantID || metadata.Principal.IdentityID != identityID || metadata.Principal.MembershipID != membershipID {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
	if len(metadata.Principal.Roles) != 2 || metadata.Principal.Roles[1] != "VIEWER" {
		t.Fatalf("unexpected roles: %#v", metadata.Principal.Roles)
	}
}

func TestTestMetadataFromHeadersRejectsMissingRole(t *testing.T) {
	req := httptest.NewRequest("POST", "/graphql", nil)
	req.Header.Set("X-Inspection-Test-Tenant", identity.NewID().String())
	req.Header.Set("X-Inspection-Test-Identity", identity.NewID().String())
	req.Header.Set("X-Inspection-Test-Membership", identity.NewID().String())
	if _, err := TestMetadataFromHeaders(req); err == nil {
		t.Fatal("expected missing role error")
	}
}
