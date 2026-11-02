package auth

import (
	"reflect"
	"testing"

	"inspection/services/inspection/internal/platform/apperror"
)

func TestTFE_UT_019RoleProductPolicyRejectsAdminForCustomerAndOperationalRoles(t *testing.T) {
	for _, role := range []string{CustomerViewer, Manager, Employee, Viewer} {
		if _, err := NormalizeProductEntitlements(role, []string{AdminProduct}); err == nil {
			t.Fatalf("%s accepted ADMIN", role)
		} else if code, field, _ := apperror.Public(err); code != apperror.InvalidInput || field != "productEntitlements" {
			t.Fatalf("%s error=%s field=%s", role, code, field)
		}
		if _, err := NormalizeProductEntitlements(role, []string{AdminProduct, DashboardProduct}); err == nil {
			t.Fatalf("%s accepted ADMIN together with DASHBOARD", role)
		}
	}
}

func TestTFE_IT_201IT_202ExplicitEntitlementsFollowADR003(t *testing.T) {
	cases := []struct {
		role      string
		requested []string
		want      []string
		invalid   bool
	}{
		{TenantAdmin, []string{DashboardProduct, AdminProduct, AdminProduct}, []string{AdminProduct, DashboardProduct}, false},
		{TenantAdmin, nil, []string{AdminProduct, DashboardProduct}, false},
		{TenantAdmin, []string{DashboardProduct}, []string{DashboardProduct}, false},
		{AccessAdmin, []string{AdminProduct}, []string{AdminProduct}, false},
		{AccessAdmin, []string{DashboardProduct}, nil, true},
		{Manager, nil, []string{DashboardProduct}, false},
		{Manager, []string{AdminProduct}, nil, true},
		{CustomerViewer, []string{DashboardProduct}, []string{DashboardProduct}, false},
		{CustomerViewer, []string{"BILLING"}, nil, true},
		{"ROOT", []string{DashboardProduct}, nil, true},
	}
	for _, tc := range cases {
		got, err := NormalizeProductEntitlements(tc.role, tc.requested)
		if tc.invalid {
			if err == nil {
				t.Fatalf("%s %v accepted", tc.role, tc.requested)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s %v = %v, %v; want %v", tc.role, tc.requested, got, err, tc.want)
		}
	}
}
