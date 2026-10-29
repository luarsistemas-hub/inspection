//go:build integration

package promote_inspection_to_origin

import (
	"testing"

	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
)

func TestPromotionCompletesWhenDraftIsActivatedOutsideIt(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	promotion := p.request("activated-outside", p.status())
	p.readyDraft(*promotion.DraftVersionID, promotion.ID, "ACCEPTED")
	origin := p.loadOrigin()
	if _, err := p.inventory.Activate(p.ctx, core.ActivateInput{TenantID: p.f.TenantID, OriginVersionID: *promotion.DraftVersionID, ActorMembershipID: p.f.MembershipID, Purpose: "INVENTORY_UPDATE", ExpectedOriginVersion: origin.Version}); err != nil {
		t.Fatal(err)
	}
	p.process(promotion.ID)
	completed := p.loadPromotion(promotion.ID)
	if completed.Status != "COMPLETED" || completed.OriginVersionID == nil || *completed.OriginVersionID != *promotion.DraftVersionID {
		t.Fatalf("promotion did not adopt the human activation: %+v", completed)
	}
	view := p.status()
	for _, impediment := range view.Impediments {
		if impediment.Code == "PROMOTION_IN_PROGRESS" {
			t.Fatalf("closed promotion still blocks the asset: %+v", view.Impediments)
		}
	}
}

func TestNewRequestAfterCatalogFailureRequeuesFailedJobs(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	first := p.request("requeue-first", p.status())
	draftID := *first.DraftVersionID
	p.readyDraft(draftID, first.ID, "ACCEPTED")
	if err := p.h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", p.f.TenantID, draftID).Update("draft_state", "FAILED").Error; err != nil {
		t.Fatal(err)
	}
	if err := p.h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=?", p.f.TenantID, draftID).Update("status", "FAILED").Error; err != nil {
		t.Fatal(err)
	}
	p.process(first.ID)
	if failed := p.loadPromotion(first.ID); failed.Status != "FAILED" || !failed.Retryable {
		t.Fatalf("catalog failure was not retryable: %+v", failed)
	}
	raw, err := p.bus.Send(p.ctx, Command{TenantID: p.f.TenantID, InspectionID: p.f.InspectionID, ClientMutationID: "requeue-second", Purpose: "INVENTORY_UPDATE", ConfirmationDigest: p.status().ConfirmationDigest})
	if err != nil {
		t.Fatal(err)
	}
	second := raw.(Result).Promotion
	if second.DraftVersionID == nil || *second.DraftVersionID != draftID {
		t.Fatalf("draft with human decisions was not reused: %+v", second)
	}
	var draft database.OriginVersion
	if err := p.h.AdminDB.Where("tenant_id=? AND id=?", p.f.TenantID, draftID).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	var failedJobs, pendingJobs int64
	if err := p.h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND status='FAILED'", p.f.TenantID, draftID).Count(&failedJobs).Error; err != nil {
		t.Fatal(err)
	}
	if err := p.h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND status='PENDING'", p.f.TenantID, draftID).Count(&pendingJobs).Error; err != nil {
		t.Fatal(err)
	}
	if draft.DraftState == nil || *draft.DraftState != "PREPARING" || failedJobs != 0 || pendingJobs == 0 {
		t.Fatalf("failed catalog was not requeued: state=%v failed=%d pending=%d", draft.DraftState, failedJobs, pendingJobs)
	}
}

func TestRetryRevalidatesPromotionImpediments(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	view := p.status()
	promotion := p.request("retry-archived", view)
	code := "CATALOG_FAILED"
	if err := p.h.AdminDB.Model(&database.InspectionOriginPromotion{}).Where("tenant_id=? AND id=?", p.f.TenantID, promotion.ID).Updates(map[string]any{"status": "FAILED", "failure_code": code, "retryable": true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := p.h.AdminDB.Model(&database.Asset{}).Where("tenant_id=? AND id=?", p.f.TenantID, p.f.AssetID).Update("status", "ARCHIVED").Error; err != nil {
		t.Fatal(err)
	}
	_, err := p.bus.Send(p.ctx, Command{TenantID: p.f.TenantID, InspectionID: p.f.InspectionID, ClientMutationID: "retry-archived", Purpose: "INVENTORY_UPDATE", ConfirmationDigest: view.ConfirmationDigest})
	if !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("retry on an archived asset = %v, want InvalidState", err)
	}
	if reloaded := p.loadPromotion(promotion.ID); reloaded.Status != "FAILED" {
		t.Fatalf("rejected retry reopened the promotion: %+v", reloaded)
	}
}
