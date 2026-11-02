package promote_inspection_to_origin

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

type promotionAuthorizationProbe struct {
	archived bool
	resolved []requestctx.Scope
	newWork  []requestctx.Scope
}

func (p *promotionAuthorizationProbe) Resolve(_ context.Context, _, _ identity.ID, target requestctx.Scope) (auth.ScopeDecision, error) {
	p.resolved = append(p.resolved, target)
	return auth.ScopeDecision{Allowed: true}, nil
}

func (p *promotionAuthorizationProbe) ResolveNewWork(_ context.Context, _, _ identity.ID, target requestctx.Scope) (auth.ScopeDecision, bool, error) {
	p.newWork = append(p.newWork, target)
	return auth.ScopeDecision{Allowed: true}, p.archived, nil
}

func TestPromotionAuthorizationChecksBusinessUnitAndInspectionNewWork(t *testing.T) {
	tenantID, businessUnitID, inspectionID := identity.NewID(), identity.NewID(), identity.NewID()
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{
		IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.Manager},
	}})

	for _, tc := range []struct {
		name      string
		archived  bool
		wantError bool
	}{
		{name: "active inspection and project"},
		{name: "archived project", archived: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &promotionAuthorizationProbe{archived: tc.archived}
			authorizer := auth.Authorizer{Scopes: probe}
			_, err := authorizeNewWork(ctx, authorizer, tenantID, []string{auth.Manager}, businessUnitID, inspectionID)
			if (err != nil) != tc.wantError {
				t.Fatalf("authorization error=%v, wantError=%v", err, tc.wantError)
			}
			if err != nil {
				if code, _, _ := apperror.Public(err); code != apperror.NotFound {
					t.Fatalf("error code=%s, want NOT_FOUND", code)
				}
			}
			if len(probe.resolved) != 1 || probe.resolved[0].Kind != "BUSINESS_UNIT" || probe.resolved[0].ID != businessUnitID {
				t.Fatalf("regular scope checks=%+v, want BUSINESS_UNIT %s", probe.resolved, businessUnitID)
			}
			if len(probe.newWork) != 1 || probe.newWork[0].Kind != "INSPECTION" || probe.newWork[0].ID != inspectionID {
				t.Fatalf("new-work checks=%+v, want INSPECTION %s", probe.newWork, inspectionID)
			}
		})
	}
}
