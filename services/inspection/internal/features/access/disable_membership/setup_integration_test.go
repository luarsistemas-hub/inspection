package disable_membership

import (
	"sync"
	"testing"

	"inspection/services/inspection/internal/features/access/accesstest"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
)

type fixture struct {
	env  accesstest.Env
	h    accesstest.Hierarchy
	deps Dependencies
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	env := accesstest.Open(t)
	return fixture{env: env, h: env.SeedTenant(t), deps: Dependencies{DB: env.Runtime, Authorizer: env.Authorizer()}}
}

func (f fixture) admin(t *testing.T, role string, status string) database.Membership {
	t.Helper()
	return f.env.AddMembership(t, f.h.TenantID, role, status, auth.ProductsForRole(role))
}

func (f fixture) status(t *testing.T, id string) string {
	t.Helper()
	var status string
	if err := f.env.Admin.Raw("SELECT status FROM access.memberships WHERE tenant_id=? AND id=?", f.h.TenantID, id).Scan(&status).Error; err != nil {
		t.Fatal(err)
	}
	return status
}

func (f fixture) disable(t *testing.T, caller, target database.Membership) error {
	t.Helper()
	_, err := handle(accesstest.Context(caller, "inspection-admin"), f.deps, Command{TenantID: f.h.TenantID, MembershipID: target.ID, ExpectedVersion: target.Version})
	return err
}

func TestTFE_DisableMembershipHonorsDelegationAndBlocksSelfDisable(t *testing.T) {
	f := newFixture(t)
	tenantAdmin := f.admin(t, auth.TenantAdmin, "ACTIVE")
	accessAdmin := f.admin(t, auth.AccessAdmin, "ACTIVE")
	manager := f.admin(t, auth.Manager, "ACTIVE")
	// An access administrator cannot disable a tenant administrator.
	if err := f.disable(t, accessAdmin, tenantAdmin); apperrorCode(err) != apperror.Forbidden || f.status(t, tenantAdmin.ID.String()) != "ACTIVE" {
		t.Fatalf("access admin disabled a tenant admin: %v", err)
	}
	// Nobody disables their own access.
	for name, member := range map[string]database.Membership{"tenant admin": tenantAdmin, "access admin": accessAdmin} {
		if err := f.disable(t, member, member); apperrorCode(err) != apperror.Forbidden || f.status(t, member.ID.String()) != "ACTIVE" {
			t.Fatalf("%s disabled themself: %v", name, err)
		}
	}
	// A manager has no administrative authority at all.
	if err := f.disable(t, manager, accessAdmin); apperrorCode(err) != apperror.Forbidden {
		t.Fatalf("manager disabled a membership: %v", err)
	}
	// A legitimate delegated disable still works.
	employee := f.admin(t, auth.Employee, "ACTIVE")
	if err := f.disable(t, accessAdmin, employee); err != nil || f.status(t, employee.ID.String()) != "DISABLED" {
		t.Fatalf("legitimate disable failed: %v", err)
	}
}

func TestTFE_LastTenantAdministratorCannotBeDisabledEvenUnderConcurrency(t *testing.T) {
	f := newFixture(t)
	first, second := f.admin(t, auth.TenantAdmin, "ACTIVE"), f.admin(t, auth.TenantAdmin, "ACTIVE")
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for index, pair := range [][2]database.Membership{{first, second}, {second, first}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[index] = f.disable(t, pair[0], pair[1])
		}()
	}
	wg.Wait()
	active := f.env.Count(t, "access.memberships", f.h.TenantID, "role='TENANT_ADMIN' AND status='ACTIVE'")
	if active != 1 {
		t.Fatalf("active tenant administrators = %d (errors %v), want exactly one", active, errs)
	}
	failures := 0
	for _, err := range errs {
		if err != nil {
			failures++
			if code := apperrorCode(err); code != apperror.InvalidState && code != apperror.Forbidden {
				t.Fatalf("losing disable code=%s err=%v", code, err)
			}
		}
	}
	if failures != 1 {
		t.Fatalf("failures=%d, want exactly one losing request", failures)
	}
}

func apperrorCode(err error) apperror.Code {
	if err == nil {
		return ""
	}
	code, _, _ := apperror.Public(err)
	return code
}
