package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientIPFromRequestUsesForwardedAddressOnlyForTrustedProxy(t *testing.T) {
	trustedCIDRs := []string{"10.0.0.0/8", "2001:db8:1::/48"}
	untrusted := httptest.NewRequest(http.MethodGet, "/", nil)
	untrusted.RemoteAddr = "198.51.100.4:1234"
	untrusted.Header.Set("X-Forwarded-For", "203.0.113.20")
	if got := clientIPFromRequest(untrusted, trustedCIDRs); got != "198.51.100.4" {
		t.Fatalf("untrusted forwarded IP=%q; want socket peer", got)
	}

	trusted := httptest.NewRequest(http.MethodGet, "/", nil)
	trusted.RemoteAddr = "10.1.2.3:1234"
	trusted.Header.Set("X-Forwarded-For", "203.0.113.20, 10.2.3.4")
	if got := clientIPFromRequest(trusted, trustedCIDRs); got != "203.0.113.20" {
		t.Fatalf("trusted proxy client IP=%q; want first untrusted hop", got)
	}

	trusted.Header.Set("X-Forwarded-For", "not-an-ip")
	if got := clientIPFromRequest(trusted, trustedCIDRs); got != "10.1.2.3" {
		t.Fatalf("malformed forwarded IP=%q; want trusted socket peer", got)
	}
}

func TestMembershipOptionalRequestAllowsOnlyIdentitySelectionAndBootstrap(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		allowed bool
	}{
		{
			name:    "identity selector",
			payload: `{"query":"query Identity { me { memberships { id tenantId role status } } tenant { id } }"}`,
			allowed: true,
		},
		{
			name:    "identity selector fragment",
			payload: `{"query":"query Identity { ...IdentityFields } fragment IdentityFields on Query { current: me { identityId } }"}`,
			allowed: true,
		},
		{
			name:    "tenant bootstrap",
			payload: `{"query":"mutation Bootstrap { createTenant(input: {name: \"Tenant\", businessUnitCode: \"HQ\", businessUnitName: \"HQ\", clientMutationId: \"1\"}) { clientMutationId } }"}`,
			allowed: true,
		},
		{
			name:    "public user invitation activation",
			payload: `{"query":"mutation Activate { requestInternalUserActivationOtp(input: {invitationToken: \"token\", clientMutationId: \"1\"}) { clientMutationId } }"}`,
			allowed: true,
		},
		{
			name:    "invitation retry is protected",
			payload: `{"query":"mutation Retry { resendInternalUserInvitation(input: {membershipId: \"id\", clientMutationId: \"1\"}) { clientMutationId } }"}`,
		},
		{
			name:    "protected query",
			payload: `{"query":"query Protected { businessUnits { nodes { id } } }"}`,
		},
		{
			name:    "mixed selector and protected query",
			payload: `{"query":"query Mixed { me { identityId } businessUnits { nodes { id } } }"}`,
		},
		{
			name:    "operation name selects protected query",
			payload: `{"query":"query Identity { me { identityId } } query Protected { memberships { nodes { id } } }","operationName":"Protected"}`,
		},
		{
			name:    "operation name required for multiple operations",
			payload: `{"query":"query Identity { me { identityId } } query AlsoIdentity { tenant { id } }"}`,
		},
		{
			name:    "bootstrap text in variables cannot bypass the boundary",
			payload: `{"query":"query Protected($value: String!) { businessUnits { nodes { id } } }","variables":{"value":"createTenant"}}`,
		},
		{
			name:    "invalid json",
			payload: `{`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/graphql", strings.NewReader(test.payload))
			if got := isMembershipOptionalRequest(request); got != test.allowed {
				t.Fatalf("allowed=%v, want %v", got, test.allowed)
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != test.payload {
				t.Fatalf("request body was not restored: %q", body)
			}
		})
	}
}
