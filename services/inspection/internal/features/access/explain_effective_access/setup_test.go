package explain_effective_access

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
)

type scopeResolver func(context.Context, identity.ID, identity.ID, requestctx.Scope) (auth.ScopeDecision, error)

func (f scopeResolver) Resolve(ctx context.Context, tenantID, membershipID identity.ID, target requestctx.Scope) (auth.ScopeDecision, error) {
	return f(ctx, tenantID, membershipID, target)
}

func TestSetupRejectsMissingDependencies(t *testing.T) {
	if err := Setup(Dependencies{}); err == nil {
		t.Fatal("expected missing dependency error")
	}
}

func TestExplainEffectiveAccessUsesScopeAndRoleSources(t *testing.T) {
	tenantID, scopedID, adminID, targetID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	resolver := scopeResolver(func(_ context.Context, tenant, membership identity.ID, target requestctx.Scope) (auth.ScopeDecision, error) {
		if tenant == tenantID && membership == scopedID && target.ID == targetID {
			return auth.ScopeDecision{Allowed: true, DirectGrant: requestctx.Scope{Kind: "BUSINESS_UNIT", ID: identity.NewID()}}, nil
		}
		return auth.ScopeDecision{}, nil
	})
	targets := []Scope{{Kind: "INSPECTION", ID: targetID}}
	scoped := explain(context.Background(), resolver, tenantID, database.Membership{ID: scopedID, Role: auth.Manager, Status: "ACTIVE", Version: 2}, targets)
	if len(scoped.Decisions) != 1 || !scoped.Decisions[0].Allowed || scoped.Decisions[0].Inheritance != "INHERITED" {
		t.Fatalf("unexpected scoped explanation: %#v", scoped.Decisions)
	}
	admin := explain(context.Background(), resolver, tenantID, database.Membership{ID: adminID, Role: auth.TenantAdmin, Status: "ACTIVE", Version: 3}, targets)
	if !admin.Decisions[0].Allowed || admin.Decisions[0].Source != "ROLE" || admin.Decisions[0].Inheritance != "TENANT" {
		t.Fatalf("unexpected tenant-admin explanation: %#v", admin.Decisions)
	}
}

func TestExplainEffectiveAccessRejectsInvalidTargetsAndDisabledMembership(t *testing.T) {
	tenantID, disabledID, targetID := identity.NewID(), identity.NewID(), identity.NewID()
	resolver := scopeResolver(func(context.Context, identity.ID, identity.ID, requestctx.Scope) (auth.ScopeDecision, error) {
		t.Fatal("disabled membership must not resolve scopes")
		return auth.ScopeDecision{}, nil
	})
	if decision := explain(context.Background(), resolver, tenantID, database.Membership{ID: disabledID, Role: auth.Manager, Status: "DISABLED"}, []Scope{{Kind: "INSPECTION", ID: targetID}}).Decisions[0]; decision.Allowed || decision.Validity != "DISABLED" {
		t.Fatalf("unexpected disabled decision: %#v", decision)
	}
	if err := validateTargets([]Scope{{Kind: "INVALID", ID: targetID}}); err == nil {
		t.Fatal("invalid target was accepted")
	}
}
