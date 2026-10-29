//go:build integration

package promote_inspection_to_origin

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestPromotionRequiresManagerRoleAndTenantIsolationCoversStatusRequestAndProcess(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	view := p.status()
	promotion := p.request("tenant-isolation", view)
	unauthorized := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: p.f.TenantID, Principal: requestctx.Principal{IdentityID: p.f.IdentityID, MembershipID: p.f.MembershipID, TenantID: p.f.TenantID, Roles: []string{auth.CustomerViewer}}})
	if _, err := p.bus.Ask(unauthorized, Query{TenantID: p.f.TenantID, InspectionID: p.f.InspectionID}); err == nil {
		t.Fatal("user without TenantAdmin/Manager role read promotion status")
	} else if code, _, _ := apperror.Public(err); code != apperror.NotFound {
		t.Fatalf("unauthorized status should be hidden as not found, got %v", err)
	}
	if _, err := p.bus.Send(unauthorized, Command{TenantID: p.f.TenantID, InspectionID: p.f.InspectionID, ClientMutationID: "unauthorized-request", ConfirmationDigest: view.ConfirmationDigest}); err == nil {
		t.Fatal("user without TenantAdmin/Manager role requested promotion")
	} else if code, _, _ := apperror.Public(err); code != apperror.NotFound {
		t.Fatalf("unauthorized request should be hidden as not found, got %v", err)
	}
	otherTenant := identity.NewID()
	otherContext := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: otherTenant, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: otherTenant, Internal: true}})
	if _, err := p.bus.Ask(otherContext, Query{TenantID: otherTenant, InspectionID: p.f.InspectionID}); err == nil {
		t.Fatal("another tenant read the promotion status")
	} else if code, _, _ := apperror.Public(err); code != apperror.NotFound {
		t.Fatalf("cross-tenant status should be not found, got %v", err)
	}
	if _, err := p.bus.Send(otherContext, Command{TenantID: otherTenant, InspectionID: p.f.InspectionID, ClientMutationID: "tenant-b-request", ConfirmationDigest: view.ConfirmationDigest}); err == nil {
		t.Fatal("another tenant requested a promotion for tenant A")
	} else if code, _, _ := apperror.Public(err); code != apperror.NotFound {
		t.Fatalf("cross-tenant request should be not found, got %v", err)
	}
	if err := Process(otherContext, p.h.DB, ProcessDependencies{Inventory: p.inventory}, otherTenant, promotion.ID); err == nil {
		t.Fatal("tenant B processed tenant A's promotion")
	}
	final := p.loadPromotion(promotion.ID)
	origin := p.loadOrigin()
	if final.Status != "PREPARING" || origin.ActiveVersionID != nil {
		t.Fatalf("unauthorized/cross-tenant calls changed operation state: promotion=%+v origin=%+v", final, origin)
	}
	var foreignOperations int64
	if err := p.h.AdminDB.Model(&database.InspectionOriginPromotion{}).Where("tenant_id=? AND id=?", otherTenant, promotion.ID).Count(&foreignOperations).Error; err != nil {
		t.Fatal(err)
	}
	if foreignOperations != 0 {
		t.Fatalf("tenant B has a promotion row for tenant A: %d", foreignOperations)
	}
}
