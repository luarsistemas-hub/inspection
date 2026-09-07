package archive_business_unit

import (
	"context"
	"testing"

	"inspection/libs/identity"
)

func TestSetupRequiresDependencies(t *testing.T) {
	if err := Setup(Dependencies{}); err == nil {
		t.Fatal("expected missing dependency error")
	}
}

func TestHandleRejectsInvalidCommandBeforeDatabaseAccess(t *testing.T) {
	_, err := handle(context.Background(), Dependencies{}, Command{TenantID: identity.NewID(), BusinessUnitID: identity.NewID()})
	if err == nil {
		t.Fatal("expected invalid version error")
	}
}
