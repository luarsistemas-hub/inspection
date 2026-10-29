//go:build integration

package promote_inspection_to_origin

import (
	"testing"

	"inspection/services/inspection/internal/platform/database"
)

func TestFailedCatalogPromotionRetriesWithSameMutationAndCompletes(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	view := p.status()
	promotion := p.request("retry-catalog", view)
	if err := p.h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", p.f.TenantID, *promotion.DraftVersionID).Updates(map[string]any{"draft_state": "FAILED"}).Error; err != nil {
		t.Fatal(err)
	}
	p.process(promotion.ID)
	failed := p.loadPromotion(promotion.ID)
	if failed.Status != "FAILED" || failed.FailureCode == nil || *failed.FailureCode != "CATALOG_FAILED" || !failed.Retryable {
		t.Fatalf("catalog failure was not retryable: %+v", failed)
	}
	origin := p.loadOrigin()
	if origin.ActiveVersionID != nil {
		t.Fatalf("failed catalog changed the active origin: %+v", origin)
	}
	retried := p.request("retry-catalog", view)
	if retried.ID != promotion.ID || retried.Status != "PREPARING" || retried.Retryable || retried.DraftVersionID == nil || *retried.DraftVersionID != *promotion.DraftVersionID {
		t.Fatalf("retry did not reopen the existing operation and draft: %+v", retried)
	}
	p.readyDraft(*retried.DraftVersionID, retried.ID, "ACCEPTED")
	p.process(retried.ID)
	completed := p.loadPromotion(promotion.ID)
	origin = p.loadOrigin()
	if completed.Status != "COMPLETED" || completed.FailureCode != nil || completed.OriginVersionID == nil || origin.ActiveVersionID == nil || *origin.ActiveVersionID != *completed.OriginVersionID {
		t.Fatalf("retry did not activate the cataloged draft: promotion=%+v origin=%+v", completed, origin)
	}
	var activeCount, matchingVersions int64
	if err := p.h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND origin_id=? AND status='ACTIVE'", p.f.TenantID, origin.ID).Count(&activeCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := p.h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", p.f.TenantID, *completed.OriginVersionID).Count(&matchingVersions).Error; err != nil {
		t.Fatal(err)
	}
	if activeCount != 1 || matchingVersions != 1 {
		t.Fatalf("retry caused duplicate versions: active=%d matching=%d", activeCount, matchingVersions)
	}
}
