//go:build integration

package promote_inspection_to_origin

import (
	"testing"
	"time"
)

func TestReconcileResumesPromotionWhoseDraftIsReady(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	promotion := p.request("reconcile-ready", p.status())
	p.readyDraft(*promotion.DraftVersionID, promotion.ID, "ACCEPTED")
	if err := Reconcile(p.ctx, p.h.DB, ProcessDependencies{Inventory: p.inventory, Now: func() time.Time { return p.now }}); err != nil {
		t.Fatal(err)
	}
	final := p.loadPromotion(promotion.ID)
	origin := p.loadOrigin()
	if final.Status != "COMPLETED" || final.OriginVersionID == nil || origin.ActiveVersionID == nil || *origin.ActiveVersionID != *final.OriginVersionID {
		t.Fatalf("reconciliation did not complete the promotion: promotion=%+v origin=%+v", final, origin)
	}
}
