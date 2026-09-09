package auth

import "testing"

func TestDelegatedAdministrativeRolesAreKnownAndEntitledToAdmin(t *testing.T) {
	roles := []string{OrganizationAdmin, AccessAdmin, ParticipationAdmin, InspectionConfigAdmin, GovernanceAdmin, Auditor}
	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			if !IsKnownRole(role) {
				t.Fatal("delegated administrative role was rejected")
			}
			products := ProductsForRole(role)
			if len(products) != 1 || products[0] != AdminProduct {
				t.Fatalf("unexpected product entitlement: %v", products)
			}
		})
	}
}

func TestCanDelegateRolePreventsDelegatedTenantAdministration(t *testing.T) {
	if CanDelegateRole([]string{AccessAdmin}, TenantAdmin) {
		t.Fatal("access administrator granted tenant administrator")
	}
	if !CanDelegateRole([]string{AccessAdmin}, GovernanceAdmin) {
		t.Fatal("access administrator could not grant delegated administrative role")
	}
	if !CanDelegateRole([]string{TenantAdmin}, TenantAdmin) {
		t.Fatal("tenant administrator could not grant tenant administrator")
	}
}
