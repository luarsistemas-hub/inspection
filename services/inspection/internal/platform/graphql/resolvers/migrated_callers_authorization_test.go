package resolvers

import (
	"context"
	"testing"

	"inspection/libs/identity"
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	projectcore "inspection/services/inspection/internal/features/projects/core"
	schedulecore "inspection/services/inspection/internal/features/schedules/core"
	segmentcore "inspection/services/inspection/internal/features/segments/core"
	templatecore "inspection/services/inspection/internal/features/templates/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

type storeFunc func(context.Context, identity.ID, identity.ID) (requestctx.Principal, error)

func (f storeFunc) Resolve(ctx context.Context, tenantID, identityID identity.ID) (requestctx.Principal, error) {
	return f(ctx, tenantID, identityID)
}

// TestTFE_UT_003IT_222MigratedCallersDenyRevokedEntitlementAndWrongAudience
// exercises callers migrated to explicit product metadata. Every case is
// refused by the authorizer before any database access (the services carry no
// database), so the denial is proven independent of resource I/O.
func TestTFE_UT_003IT_222MigratedCallersDenyRevokedEntitlementAndWrongAudience(t *testing.T) {
	tenant, subject, membership := identity.NewID(), identity.NewID(), identity.NewID()
	storeFor := func(role string, entitlements ...string) auth.Authorizer {
		return auth.Authorizer{Store: storeFunc(func(context.Context, identity.ID, identity.ID) (requestctx.Principal, error) {
			return requestctx.Principal{IdentityID: subject, TenantID: tenant, MembershipID: membership, Roles: []string{role}, ProductEntitlements: entitlements}, nil
		})}
	}
	token := func(audience string) context.Context {
		product := auth.DashboardProduct
		if audience == "inspection-admin" {
			product = auth.AdminProduct
		}
		return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{IdentityID: subject, TenantID: tenant, Audience: audience, Product: product}})
	}
	dashboardCallers := map[string]func(auth.Authorizer, context.Context) error{
		"inspections list": func(a auth.Authorizer, ctx context.Context) error {
			_, err := inspectioncore.Service{Authorizer: a}.List(ctx, inspectioncore.ListInput{TenantID: tenant})
			return err
		},
		"schedules list": func(a auth.Authorizer, ctx context.Context) error {
			_, err := schedulecore.Service{Authorizer: a}.List(ctx, tenant, 10, "")
			return err
		},
		"projects list": func(a auth.Authorizer, ctx context.Context) error {
			_, err := projectcore.Service{Authorizer: a}.List(ctx, tenant, 10, "")
			return err
		},
	}
	for name, call := range dashboardCallers {
		// MANAGER whose DASHBOARD entitlement was revoked is refused with the dashboard token.
		if err := call(storeFor(auth.Manager), token("inspection-dashboard")); apperrorCode(err) != apperror.Forbidden {
			t.Errorf("%s: revoked DASHBOARD entitlement err=%v", name, err)
		}
		// A token minted for the Admin product cannot call a Dashboard operation, even when entitled to both.
		if err := call(storeFor(auth.Manager, auth.AdminProduct, auth.DashboardProduct), token("inspection-admin")); apperrorCode(err) != apperror.Forbidden {
			t.Errorf("%s: wrong-audience token err=%v", name, err)
		}
	}
	adminCallers := map[string]func(auth.Authorizer, context.Context) error{
		"templates publish": func(a auth.Authorizer, ctx context.Context) error {
			_, err := templatecore.Service{Authorizer: a}.Publish(ctx, tenant, "key", "name", "idem", []byte(`{}`))
			return err
		},
		"segments publish": func(a auth.Authorizer, ctx context.Context) error {
			_, err := segmentcore.Service{Authorizer: a}.Publish(ctx, tenant, "key", "name", "idem", []byte(`{}`), []byte(`{}`))
			return err
		},
	}
	for name, call := range adminCallers {
		if err := call(storeFor(auth.InspectionConfigAdmin), token("inspection-admin")); apperrorCode(err) != apperror.Forbidden {
			t.Errorf("%s: revoked ADMIN entitlement err=%v", name, err)
		}
		if err := call(storeFor(auth.TenantAdmin, auth.AdminProduct, auth.DashboardProduct), token("inspection-dashboard")); apperrorCode(err) != apperror.Forbidden {
			t.Errorf("%s: dashboard token on an Admin operation err=%v", name, err)
		}
	}
}

func apperrorCode(err error) apperror.Code {
	if err == nil {
		return ""
	}
	code, _, _ := apperror.Public(err)
	return code
}
