//go:build integration

package process_inventory_job

import (
	"strings"
	"testing"

	"inspection/libs/identity"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestProvenancePostgresGrantsConstraintsAndIsolation(t *testing.T) {
	f, key, _, note, revision := cataloged(t, "Pratos")
	draft, _ := f.draftOf(key)
	item := f.itemsOfRoom(key)["Pratos"]

	t.Run("IT-167 the catalog writes visual and voice sources under the runtime role", func(t *testing.T) {
		kinds := map[string]int{}
		for _, row := range f.sourcesOf(item.ID) {
			kinds[row.SourceKind+":"+row.AttributeName]++
		}
		if kinds["VOICE:name"] != 1 || kinds["VOICE:quantity"] != 1 || kinds["VISUAL:name"] != 1 || kinds["VISUAL:quantity"] != 0 {
			t.Fatalf("sources = %v", kinds)
		}
		if cites := f.voiceCites(item.ID); cites[note.String()] != revision {
			t.Fatalf("citations = %v", cites)
		}
	})
	t.Run("IT-174 IT-222 another tenant reads no provenance", func(t *testing.T) {
		var count int64
		if err := f.h.WithinTenant(f.ctx, identity.NewID(), func(tx *gorm.DB) error {
			return tx.Model(&database.InventoryItemAttributeSource{}).Count(&count).Error
		}); err != nil || count != 0 {
			t.Fatalf("another tenant sees %d sources (err=%v)", count, err)
		}
	})
	t.Run("IT-195 IT-196 a human decision appends reviews that cannot be deleted directly", func(t *testing.T) {
		f.decide(draft, item, "ACCEPT")
		reviews := 0
		for _, row := range f.sourcesOf(item.ID) {
			if row.SourceKind == "HUMAN_REVIEW" {
				reviews++
				if row.ReviewedBy == nil || *row.ReviewedBy != f.seed.MembershipID {
					t.Fatalf("review = %+v", row)
				}
			}
		}
		if reviews == 0 {
			t.Fatal("the decision recorded no review")
		}
		err := f.h.WithinTenant(f.ctx, f.seed.TenantID, func(tx *gorm.DB) error {
			return tx.Exec("DELETE FROM inventory.item_attribute_sources WHERE tenant_id=? AND item_id=? AND source_kind='HUMAN_REVIEW'", f.seed.TenantID, item.ID).Error
		})
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("direct delete of a review: err = %v, want the append-only guard", err)
		}
		if still := f.sourcesOf(item.ID); len(still) < reviews {
			t.Fatal("a review was deleted")
		}
	})
	t.Run("the earlier sources survive the decision and retention purges still work", func(t *testing.T) {
		if cites := f.voiceCites(item.ID); cites[note.String()] != revision {
			t.Fatalf("the voice source was erased by the decision: %v", cites)
		}
		// The retention purges delete evidence and then the item itself: the
		// sources follow through the cascade, reviews included.
		err := f.h.WithinTenant(f.ctx, f.seed.TenantID, func(tx *gorm.DB) error {
			if err := tx.Exec("DELETE FROM origins.inventory_item_evidence WHERE tenant_id=? AND item_id=?", f.seed.TenantID, item.ID).Error; err != nil {
				return err
			}
			return tx.Exec("DELETE FROM origins.inventory_items WHERE tenant_id=? AND id=?", f.seed.TenantID, item.ID).Error
		})
		if err != nil {
			t.Fatalf("purging an item with sources failed: %v", err)
		}
		if left := f.sourcesOf(item.ID); len(left) != 0 {
			t.Fatalf("sources left after the purge: %+v", left)
		}
	})
}

func TestProvenanceBulkAcceptAndDecisionPostgres(t *testing.T) {
	f := newFlow(t, "PHOTO")
	key, photo := f.voiceRoom("Cozinha")
	note, revision := f.note(itNote{Key: key, Photo: photo, Text: "cinco pratos"})
	f.answerWith(
		voiceItem{name: "Pratos", quantity: intp(5), evidence: []string{photo.String()}, citations: []map[string]any{cite(note, revision, "name", "quantity")}, confidence: 9500},
		voiceItem{name: "Mesa", evidence: []string{photo.String()}, confidence: 9500},
	)
	f.submitPhotos()
	f.drain()
	draft, _ := f.draftOf(key)
	env := f.environmentOf(key)
	service := f.inventory()

	if before := f.voicePending(draft); before != 1 {
		t.Fatalf("voice pending before any decision = %d, want the one voice-supported item", before)
	}
	t.Run("IT-211 bulk acceptance excludes voice-supported items until a person decides", func(t *testing.T) {
		accepted, err := service.BulkAccept(f.ctx, f.seed.TenantID, draft, env.ID, f.seed.MembershipID, 8500)
		if err != nil || accepted != 1 {
			t.Fatalf("accepted=%d err=%v, want only the visual item", accepted, err)
		}
		items := f.itemsOfRoom(key)
		if items["Mesa"].Decision != "ACCEPTED" || items["Pratos"].Decision != "PENDING" {
			t.Fatalf("items = %+v", items)
		}
		again, err := service.BulkAccept(f.ctx, f.seed.TenantID, draft, env.ID, f.seed.MembershipID, 8500)
		if !apperror.Is(err, apperror.InvalidState) || again != 0 {
			t.Fatalf("voice-only bulk acceptance: accepted=%d err=%v, want INVALID_STATE", again, err)
		}
	})
	t.Run("IT-201 IT-208 only the voice item needs the explicit decision, and it enters through the versioned flow", func(t *testing.T) {
		pratos := f.itemsOfRoom(key)["Pratos"]
		f.decide(draft, pratos, "ACCEPT")
		decided := f.itemsOfRoom(key)["Pratos"]
		if decided.Decision != "ACCEPTED" || decided.Version != pratos.Version+1 || decided.DecidedBy == nil {
			t.Fatalf("decided = %+v", decided)
		}
		if after := f.voicePending(draft); after != 0 {
			t.Fatalf("voice pending after the decision = %d", after)
		}
	})
}

// voicePending is the production count of pending items awaiting a voice review.
func (f *flow) voicePending(draft identity.ID) int {
	f.t.Helper()
	count := 0
	f.within(func(tx *gorm.DB) error {
		var err error
		count, err = inventorycore.VoicePendingCount(tx, f.seed.TenantID, draft)
		return err
	})
	return count
}

func TestCustomerViewNeverCarriesAttributeSources(t *testing.T) {
	f, key, _, _, _ := cataloged(t, "Pratos")
	draft, _ := f.draftOf(key)
	pratos := f.itemsOfRoom(key)["Pratos"]
	f.decide(draft, pratos, "ACCEPT")
	var version database.OriginVersion
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", f.seed.TenantID, draft).Take(&version).Error
	})
	now := f.now()
	purpose := "MOVE_IN"
	if err := f.h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", f.seed.TenantID, draft).Updates(map[string]any{"status": "ACTIVE", "purpose": purpose, "activated_at": now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.h.AdminDB.Model(&database.Origin{}).Where("tenant_id=? AND id=?", f.seed.TenantID, version.OriginID).Update("active_version_id", draft).Error; err != nil {
		t.Fatal(err)
	}
	service := f.inventory()
	customer, err := service.BaselineInventory(f.ctx, f.seed.TenantID, f.seed.AssetID, nil, true)
	if err != nil || len(customer.Environments) != 1 || len(customer.Environments[0].Items) != 1 {
		t.Fatalf("customer baseline = %+v err=%v", customer, err)
	}
	if got := customer.Environments[0].Items[0].Sources; len(got) != 0 {
		t.Fatalf("customer-safe view carries provenance: %+v", got)
	}
	internal, err := service.BaselineInventory(f.ctx, f.seed.TenantID, f.seed.AssetID, nil, false)
	if err != nil || len(internal.Environments[0].Items[0].Sources) == 0 {
		t.Fatalf("internal view must carry provenance: %+v err=%v", internal, err)
	}
}
