package resolvers

import (
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestAIP_InspectionScopeConditionFollowsTheActorGrants(t *testing.T) {
	tenant, unit, asset, project, inspection := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	actor := func(role string, scopes ...requestctx.Scope) requestctx.Metadata {
		return requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{TenantID: tenant, Roles: []string{role}, Scopes: scopes, ProductEntitlements: auth.ProductsForRole(role)}}
	}
	if condition, args := inspectionScopeCondition(actor(auth.TenantAdmin), "inspection_id"); condition != "" || args != nil {
		t.Fatalf("tenant administrator was restricted: %q %v", condition, args)
	}
	if condition, args := inspectionScopeCondition(actor(auth.Manager), "inspection_id"); condition != "1=0" || args != nil {
		t.Fatalf("an actor without grants sees rows: %q %v", condition, args)
	}
	if condition, _ := inspectionScopeCondition(actor(auth.Manager, requestctx.Scope{Kind: "TENANT", ID: tenant}, requestctx.Scope{Kind: "UNKNOWN", ID: unit}), "inspection_id"); condition != "1=0" {
		t.Fatalf("unsupported grant kinds widened the view: %q", condition)
	}
	condition, args := inspectionScopeCondition(actor(auth.Viewer,
		requestctx.Scope{Kind: "BUSINESS_UNIT", ID: unit}, requestctx.Scope{Kind: "ASSET", ID: asset}, requestctx.Scope{Kind: "PROJECT", ID: project}, requestctx.Scope{Kind: "INSPECTION", ID: inspection}), "d.inspection_id")
	for _, fragment := range []string{"d.inspection_id IN (SELECT s.id FROM inspections.inspections s WHERE s.tenant_id = ?", "s.business_unit_id IN ?", "s.asset_id IN ?", "s.project_id IN ?", "s.id IN ?"} {
		if !strings.Contains(condition, fragment) {
			t.Fatalf("condition %q lacks %q", condition, fragment)
		}
	}
	if len(args) != 5 || args[0] != tenant {
		t.Fatalf("arguments = %v, want the tenant followed by one list per grant kind", args)
	}
	single, singleArgs := inspectionScopeCondition(actor(auth.Employee, requestctx.Scope{Kind: "ASSET", ID: asset}), "inspection_id")
	if strings.Contains(single, "business_unit_id") || strings.Contains(single, "project_id") || len(singleArgs) != 2 {
		t.Fatalf("an asset grant produced %q %v", single, singleArgs)
	}
}

func TestAIP_CursorIdentifiersMustBeUUIDs(t *testing.T) {
	if !validCursorID(identity.NewID().String()) {
		t.Fatal("a UUID cursor id was rejected")
	}
	for _, id := range []string{"", "not-a-uuid", "x' OR '1'='1", "00000000-0000-0000-0000-00000000000g", strings.Repeat("a", 500)} {
		if validCursorID(id) {
			t.Fatalf("cursor id %q accepted", id)
		}
	}
}
