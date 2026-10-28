package update_policy

import (
	"testing"

	"inspection/libs/identity"
	counterpartycore "inspection/services/inspection/internal/features/counterparty/core"
	"inspection/services/inspection/internal/features/counterparty/internal/slicetest"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
)

func TestUnit_UpdatePolicySetupRejectsMissingDependencies(t *testing.T) {
	db := slicetest.DB(t)
	authorizer := slicetest.Authorizer([]string{auth.TenantAdmin}, []string{auth.AdminProduct})
	for name, deps := range map[string]Dependencies{
		"bus":        {Service: slicetest.Service(db), Authorizer: authorizer},
		"database":   {Bus: mediator.New(), Authorizer: authorizer},
		"authorizer": {Bus: mediator.New(), Service: slicetest.Service(db)},
	} {
		if err := Setup(deps); err == nil {
			t.Fatalf("Setup accepted missing %s", name)
		}
	}
}

func TestUnit_UT303_UpdateBaselinePolicyForbidsManager(t *testing.T) {
	for name, products := range map[string][]string{
		"UT-303 manager with dashboard product": {auth.DashboardProduct},
		"UT-303 manager with admin product":     {auth.AdminProduct, auth.DashboardProduct},
	} {
		t.Run(name, func(t *testing.T) {
			db := slicetest.DB(t)
			bus := mediator.New()
			if err := Setup(Dependencies{Bus: bus, Service: slicetest.Service(db), Authorizer: slicetest.Authorizer([]string{auth.Manager}, products)}); err != nil {
				t.Fatal(err)
			}
			tenantID := identity.NewID()
			_, err := bus.Send(slicetest.Context(tenantID), Command{TenantID: tenantID, Days: 10})
			if code, _, _ := apperror.Public(err); code != apperror.Forbidden {
				t.Fatalf("UT-303 MANAGER policy update error=%v code=%s, want FORBIDDEN", err, code)
			}
			var count int64
			if err := db.Model(&database.BaselinePolicy{}).Where("tenant_id=?", tenantID).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("UT-303 forbidden update persisted policy rows=%d err=%v", count, err)
			}
		})
	}
}

func TestUnit_UT302_UpdateBaselinePolicyAllowsConfigurationAdmins(t *testing.T) {
	db := slicetest.DB(t)
	tenantID := identity.NewID()
	for i, tc := range []struct {
		role string
		days int
	}{
		{role: auth.TenantAdmin, days: 90},
		{role: auth.InspectionConfigAdmin, days: 1},
	} {
		bus := mediator.New()
		if err := Setup(Dependencies{Bus: bus, Service: slicetest.Service(db), Authorizer: slicetest.Authorizer([]string{tc.role}, []string{auth.AdminProduct})}); err != nil {
			t.Fatal(err)
		}
		result, err := bus.Send(slicetest.Context(tenantID), Command{TenantID: tenantID, Days: tc.days})
		if err != nil {
			t.Fatalf("UT-302 %s update error=%v", tc.role, err)
		}
		policy := result.(database.BaselinePolicy)
		if policy.ContestationDeadlineDays != tc.days || policy.Version != int64(i+1) {
			t.Fatalf("UT-302 %s policy=%+v, want %d days version %d", tc.role, policy, tc.days, i+1)
		}
		_, err = bus.Send(slicetest.Context(tenantID), Command{TenantID: tenantID, Days: 91})
		if code, field, _ := apperror.Public(err); code != apperror.InvalidInput || field != "contestationDeadlineDays" {
			t.Fatalf("UT-302 %s 91 days error=%v code=%s field=%q", tc.role, err, code, field)
		}
	}
}

func TestUnit_BaselinePolicyQueryDefaultsAndReturnsConfiguredValue(t *testing.T) {
	db := slicetest.DB(t)
	tenantID := identity.NewID()
	bus := mediator.New()
	if err := Setup(Dependencies{Bus: bus, Service: slicetest.Service(db), Authorizer: slicetest.Authorizer([]string{auth.TenantAdmin}, []string{auth.AdminProduct})}); err != nil {
		t.Fatal(err)
	}
	read := func() database.BaselinePolicy {
		t.Helper()
		result, err := bus.Ask(slicetest.Context(tenantID), Query{TenantID: tenantID})
		if err != nil {
			t.Fatal(err)
		}
		return result.(database.BaselinePolicy)
	}
	if policy := read(); policy.ContestationDeadlineDays != counterpartycore.DefaultDeadlineDays || policy.Version != 0 {
		t.Fatalf("unconfigured policy=%+v, want default %d and version 0", policy, counterpartycore.DefaultDeadlineDays)
	}
	if _, err := bus.Send(slicetest.Context(tenantID), Command{TenantID: tenantID, Days: 7}); err != nil {
		t.Fatal(err)
	}
	if policy := read(); policy.ContestationDeadlineDays != 7 || policy.Version != 1 {
		t.Fatalf("configured policy=%+v, want 7 days and version 1", policy)
	}
}

func TestUnit_BaselinePolicyQueryRequiresAdminProduct(t *testing.T) {
	tenantID := identity.NewID()
	for name, products := range map[string][]string{
		"dashboard entitlement": {auth.DashboardProduct},
		"admin entitlement":     {auth.AdminProduct},
	} {
		t.Run(name, func(t *testing.T) {
			bus := mediator.New()
			if err := Setup(Dependencies{Bus: bus, Service: slicetest.Service(slicetest.DB(t)), Authorizer: slicetest.Authorizer([]string{auth.TenantAdmin}, products)}); err != nil {
				t.Fatal(err)
			}
			_, err := bus.Ask(slicetest.Context(tenantID), Query{TenantID: tenantID})
			if name == "dashboard entitlement" {
				if code, _, _ := apperror.Public(err); code != apperror.Forbidden {
					t.Fatalf("Dashboard token query error=%v code=%s, want FORBIDDEN", err, code)
				}
			} else if err != nil {
				t.Fatalf("Admin token query error=%v", err)
			}
		})
	}
}
