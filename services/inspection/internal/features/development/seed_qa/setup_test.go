package seed_qa

import (
	"context"
	"testing"

	"inspection/libs/identity"
)

func TestSetupRejectsMissingDependencies(t *testing.T) {
	if _, err := Setup(context.Background(), nil, "issuer", "http://localhost:3003"); err == nil {
		t.Fatal("nil database accepted")
	}
}

func TestStableIDsAreTenantScoped(t *testing.T) {
	tenantA, tenantB := identity.NewID(), identity.NewID()
	first, replay := stableIDs(tenantA), stableIDs(tenantA)
	other := stableIDs(tenantB)
	if first.participant != replay.participant || first.asset != replay.asset {
		t.Fatal("stable QA identities changed between replays")
	}
	if first.participant == other.participant || first.asset == other.asset {
		t.Fatal("stable QA identities leaked across tenants")
	}
}
