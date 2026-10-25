package resolve

import (
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/counterparty/internal/slicetest"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/mediator"
)

func TestUnit_ResolveSetupRejectsMissingDependencies(t *testing.T) {
	db := slicetest.DB(t)
	authorizer := slicetest.Authorizer([]string{auth.TenantAdmin}, []string{auth.DashboardProduct})
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

func TestUnit_ResolveAuthorizesBeforeRevealingInvitationExistence(t *testing.T) {
	tenantID := identity.NewID()
	for _, tc := range []struct {
		name     string
		roles    []string
		products []string
		code     apperror.Code
		field    string
	}{
		{name: "caller without role is forbidden for unknown id", roles: nil, products: []string{auth.DashboardProduct}, code: apperror.Forbidden},
		{name: "viewer is forbidden for unknown id", roles: []string{auth.Viewer}, products: []string{auth.DashboardProduct}, code: apperror.Forbidden},
		{name: "tenant admin gets not found", roles: []string{auth.TenantAdmin}, products: []string{auth.DashboardProduct}, code: apperror.NotFound, field: "counterpartyInvitationId"},
		{name: "manager gets not found", roles: []string{auth.Manager}, products: []string{auth.DashboardProduct}, code: apperror.NotFound, field: "counterpartyInvitationId"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bus := mediator.New()
			if err := Setup(Dependencies{Bus: bus, Service: slicetest.Service(slicetest.DB(t)), Authorizer: slicetest.Authorizer(tc.roles, tc.products)}); err != nil {
				t.Fatal(err)
			}
			_, err := bus.Send(slicetest.Context(tenantID), Command{TenantID: tenantID, InvitationID: identity.NewID(), ActorID: identity.NewID(), Outcome: "DECLINE", Reason: "motivo"})
			if code, field, _ := apperror.Public(err); code != tc.code || field != tc.field {
				t.Fatalf("error=%v code=%s field=%q, want %s/%q", err, code, field, tc.code, tc.field)
			}
		})
	}
}
