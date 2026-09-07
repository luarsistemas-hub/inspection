package disable_membership

import (
	"context"
	"testing"

	"inspection/libs/identity"
)

func TestHandleRejectsInvalidCommandBeforeDatabaseAccess(t *testing.T) {
	_, err := handle(context.Background(), Dependencies{}, Command{TenantID: identity.NewID(), MembershipID: identity.NewID()})
	if err == nil {
		t.Fatal("expected invalid version error")
	}
}
