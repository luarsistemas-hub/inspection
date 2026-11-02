//go:build integration

package promote_inspection_to_origin

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

type promotionCallAuthorizationProbe struct {
	resolved []requestctx.Scope
	newWork  []requestctx.Scope
}

func (p *promotionCallAuthorizationProbe) Resolve(_ context.Context, _, _ identity.ID, target requestctx.Scope) (auth.ScopeDecision, error) {
	p.resolved = append(p.resolved, target)
	return auth.ScopeDecision{Allowed: true}, nil
}

func (p *promotionCallAuthorizationProbe) ResolveNewWork(_ context.Context, _, _ identity.ID, target requestctx.Scope) (auth.ScopeDecision, bool, error) {
	p.newWork = append(p.newWork, target)
	return auth.ScopeDecision{}, false, nil
}

func TestTFE_IT_431RequestAuthorizesBusinessUnitThenInspectionNewWork(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	probe := &promotionCallAuthorizationProbe{}
	d := Dependencies{DB: p.h.DB, Authorizer: auth.Authorizer{Scopes: probe}, Now: func() time.Time { return p.now }, Inventory: p.inventory}
	ctx := managerPromotionContext(p.ctx)
	status := p.status()
	_, err := request(ctx, d, Command{TenantID: p.f.TenantID, InspectionID: p.f.InspectionID, ClientMutationID: "auth-request-call", Purpose: "INVENTORY_UPDATE", ConfirmationDigest: status.ConfirmationDigest})
	if err == nil {
		t.Fatal("request should stop when the inspection new-work grant is denied")
	}
	assertPromotionCallScopes(t, probe, p.f.BusinessUnitID, p.f.InspectionID)
}

func TestTFE_IT_431PrepareDraftAuthorizesBusinessUnitThenInspectionNewWork(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	probe := &promotionCallAuthorizationProbe{}
	d := Dependencies{DB: p.h.DB, Authorizer: auth.Authorizer{Scopes: probe}, Now: func() time.Time { return p.now }, Inventory: p.inventory}
	ctx := managerPromotionContext(p.ctx)
	_, err := prepareDraft(ctx, d, PrepareDraftCommand{TenantID: p.f.TenantID, InspectionID: p.f.InspectionID, ActorID: p.f.MembershipID})
	if err == nil {
		t.Fatal("prepareDraft should stop when the inspection new-work grant is denied")
	}
	assertPromotionCallScopes(t, probe, p.f.BusinessUnitID, p.f.InspectionID)
}

func managerPromotionContext(ctx context.Context) context.Context {
	metadata, _ := requestctx.FromContext(ctx)
	metadata.Principal.Roles = []string{auth.Manager}
	return requestctx.WithMetadata(ctx, metadata)
}

func assertPromotionCallScopes(t *testing.T, probe *promotionCallAuthorizationProbe, businessUnitID, inspectionID identity.ID) {
	t.Helper()
	if len(probe.resolved) != 1 || probe.resolved[0].Kind != "BUSINESS_UNIT" || probe.resolved[0].ID != businessUnitID {
		t.Fatalf("regular authorization calls=%+v, want BUSINESS_UNIT %s", probe.resolved, businessUnitID)
	}
	if len(probe.newWork) != 1 || probe.newWork[0].Kind != "INSPECTION" || probe.newWork[0].ID != inspectionID {
		t.Fatalf("new-work authorization calls=%+v, want INSPECTION %s", probe.newWork, inspectionID)
	}
}
