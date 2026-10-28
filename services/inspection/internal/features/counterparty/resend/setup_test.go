package resend

import (
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/counterparty/internal/slicetest"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/mediator"
)

func TestUnit_ResendSetupRejectsMissingDependencies(t *testing.T) {
	db := slicetest.DB(t)
	authorizer := slicetest.Authorizer([]string{auth.TenantAdmin}, []string{auth.DashboardProduct})
	for name, mutate := range map[string]func(*Dependencies){
		"bus":           func(d *Dependencies) { d.Bus = nil },
		"database":      func(d *Dependencies) { d.Service.DB = nil },
		"notifications": func(d *Dependencies) { d.Service.Notifications = nil },
		"capture url":   func(d *Dependencies) { d.Service.CaptureBaseURL = "" },
		"authorizer":    func(d *Dependencies) { d.Authorizer = auth.Authorizer{} },
	} {
		t.Run(name, func(t *testing.T) {
			deps := Dependencies{Bus: mediator.New(), Service: slicetest.Service(db), Authorizer: authorizer}
			mutate(&deps)
			if err := Setup(deps); err == nil {
				t.Fatalf("Setup accepted missing %s", name)
			}
		})
	}
}

func TestUnit_ResendAuthorizesBeforeRevealingInvitationExistence(t *testing.T) {
	tenantID := identity.NewID()
	for _, tc := range []struct {
		name  string
		roles []string
		code  apperror.Code
		field string
	}{
		{name: "caller without role is forbidden for unknown id", code: apperror.Forbidden},
		{name: "customer viewer is forbidden for unknown id", roles: []string{auth.CustomerViewer}, code: apperror.Forbidden},
		{name: "tenant admin gets not found", roles: []string{auth.TenantAdmin}, code: apperror.NotFound, field: "counterpartyInvitationId"},
		{name: "employee gets not found", roles: []string{auth.Employee}, code: apperror.NotFound, field: "counterpartyInvitationId"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bus := mediator.New()
			if err := Setup(Dependencies{Bus: bus, Service: slicetest.Service(slicetest.DB(t)), Authorizer: slicetest.Authorizer(tc.roles, []string{auth.DashboardProduct})}); err != nil {
				t.Fatal(err)
			}
			_, err := bus.Send(slicetest.Context(tenantID), Command{TenantID: tenantID, InvitationID: identity.NewID()})
			if code, field, _ := apperror.Public(err); code != tc.code || field != tc.field {
				t.Fatalf("error=%v code=%s field=%q, want %s/%q", err, code, field, tc.code, tc.field)
			}
		})
	}
}
