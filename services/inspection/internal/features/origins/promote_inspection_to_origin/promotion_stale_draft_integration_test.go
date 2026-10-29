//go:build integration

package promote_inspection_to_origin

import (
	"testing"

	"inspection/services/inspection/internal/platform/database"
)

func TestNewRequestAfterEmptyInventoryFailureCataloguesAgain(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	first := p.request("empty-first", p.status())
	p.readyDraft(*first.DraftVersionID, first.ID)
	if err := p.h.AdminDB.Where("tenant_id=? AND origin_version_id=?", p.f.TenantID, *first.DraftVersionID).Delete(&database.InventoryEnvironment{}).Error; err != nil {
		t.Fatal(err)
	}
	p.process(first.ID)
	failed := p.loadPromotion(first.ID)
	if failed.Status != "FAILED" || failed.FailureCode == nil || *failed.FailureCode != "EMPTY_INVENTORY" || failed.Retryable {
		t.Fatalf("empty catalog did not fail terminally: %+v", failed)
	}
	second := p.request("empty-second", p.status())
	if second.ID == first.ID || second.Status != "PREPARING" || second.DraftVersionID == nil || *second.DraftVersionID == *first.DraftVersionID {
		t.Fatalf("new request reused the empty draft instead of cataloguing again: %+v", second)
	}
	var jobs int64
	if err := p.h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND kind='CATALOG'", p.f.TenantID, *second.DraftVersionID).Count(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if jobs == 0 || second.CatalogGeneration == nil {
		t.Fatalf("new request copied the empty catalog instead of requesting a new one: jobs=%d promotion=%+v", jobs, second)
	}
	var stale database.OriginVersion
	if err := p.h.AdminDB.Where("tenant_id=? AND id=?", p.f.TenantID, *first.DraftVersionID).Take(&stale).Error; err != nil {
		t.Fatal(err)
	}
	if stale.Status != "DISCARDED" {
		t.Fatalf("empty draft of the failed promotion was not discarded: %+v", stale)
	}
	var fresh database.OriginVersion
	if err := p.h.AdminDB.Where("tenant_id=? AND id=?", p.f.TenantID, *second.DraftVersionID).Take(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	if fresh.Status != "DRAFT" || fresh.Source != "INSPECTION_PROMOTION" {
		t.Fatalf("new request did not create a fresh promotion draft: %+v", fresh)
	}
}

func TestNewRequestAfterFailureKeepsDraftWithHumanDecisions(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	first := p.request("kept-first", p.status())
	p.readyDraft(*first.DraftVersionID, first.ID, "REJECTED")
	code := "EMPTY_INVENTORY"
	if err := p.h.AdminDB.Model(&database.InspectionOriginPromotion{}).Where("tenant_id=? AND id=?", p.f.TenantID, first.ID).Updates(map[string]any{"status": "FAILED", "failure_code": code, "retryable": false}).Error; err != nil {
		t.Fatal(err)
	}
	second := p.request("kept-second", p.status())
	if second.DraftVersionID == nil || *second.DraftVersionID != *first.DraftVersionID {
		t.Fatalf("draft holding human decisions was not reused: %+v", second)
	}
}
