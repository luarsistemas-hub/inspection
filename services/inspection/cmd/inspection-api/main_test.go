package main

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

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
