package invite_internal_user

import (
	"context"
	"testing"

	"inspection/libs/identity"
)

func TestHandleRejectsUnknownRoleBeforeDatabaseAccess(t *testing.T) {
	_, err := handle(context.Background(), Dependencies{}, Command{TenantID: identity.NewID(), Issuer: "issuer", Subject: "subject", Role: "ROOT", ClientID: "mutation"})
	if err == nil {
		t.Fatal("expected unknown role error")
	}
}

func TestValidateScopesRejectsUnknownResource(t *testing.T) {
	err := validateScopes([]Scope{{Kind: "UNKNOWN", ResourceID: identity.NewID()}})
	if err == nil {
		t.Fatal("expected invalid scope error")
	}
}
