//go:build integration

package resolvers

import (
	"fmt"
	"strings"
	"testing"

	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
)

func TestInventoryVoiceDecisionMatrixIT203IT204(t *testing.T) {
	t.Run("IT-203 stale reviewer sees current decision", func(t *testing.T) {
		fx, d := seedVoiceDraft(t, auth.Employee)
		mutation := func(action, id string) string {
			return fmt.Sprintf(`mutation { decideInventoryItems(input:{originVersionId:%q,decisions:[{itemId:%q,action:%q,expectedVersion:1}],clientMutationId:%q}) { items { id decision version } userErrors { code field } } }`, d.version.String(), d.item.String(), action, id)
		}
		first := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, mutation("ACCEPT", "it203-first"), fx.ctx, "", "")
		if !strings.Contains(first, `"decision":"ACCEPTED"`) {
			t.Fatalf("first decision = %s", first)
		}
		stale := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, mutation("REJECT", "it203-stale"), fx.ctx, "", "")
		if !strings.Contains(stale, `"code":"CONFLICT"`) && !strings.Contains(stale, `"field":"expectedVersion"`) {
			t.Fatalf("stale decision = %s", stale)
		}
		current := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`query { inventoryDraft(originVersionId:%q) { environments { items { id decision } } } }`, d.version.String()), fx.ctx, "", "")
		if !strings.Contains(current, `"decision":"ACCEPTED"`) {
			t.Fatalf("current decision not shown: %s", current)
		}
	})
	t.Run("IT-204 rejected save leaves the confirmed item unchanged", func(t *testing.T) {
		fx, d := seedVoiceDraft(t, auth.Employee)
		if err := fx.h.AdminDB.Exec(`CREATE OR REPLACE FUNCTION reject_it204_decision() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected decision failure'; END $$; CREATE TRIGGER reject_it204_decision BEFORE UPDATE ON origins.inventory_items FOR EACH ROW EXECUTE FUNCTION reject_it204_decision()`).Error; err != nil {
			t.Fatal(err)
		}
		defer fx.h.AdminDB.Exec(`DROP TRIGGER IF EXISTS reject_it204_decision ON origins.inventory_items; DROP FUNCTION IF EXISTS reject_it204_decision()`)
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`mutation { decideInventoryItems(input:{originVersionId:%q,decisions:[{itemId:%q,action:"ACCEPT",expectedVersion:1}],clientMutationId:"it204-fail"}) { items { id decision } userErrors { code } } }`, d.version.String(), d.item.String()), fx.ctx, "", "")
		if !strings.Contains(response, `"userErrors":[{"code":"DEPENDENCY_UNAVAILABLE"}`) && !strings.Contains(response, `"errors"`) {
			t.Fatalf("failed save response = %s", response)
		}
		var item database.InventoryItem
		if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, d.item).Take(&item).Error; err != nil || item.Decision != "PENDING" {
			t.Fatalf("item after failed save = %+v err=%v", item, err)
		}
	})
}

func TestInventoryVoiceMergeRetryIT191(t *testing.T) {
	fx := newInventoryITFixture(t, auth.Employee)
	seed := seedInventoryDraft(t, fx, false)
	first := addInventoryITItem(t, fx, seed, "Mesas citadas")
	second := addInventoryITItem(t, fx, seed, "Mesas citadas")
	query := fmt.Sprintf(`mutation { mergeInventoryItems(input:{originVersionId:%q,itemIds:[%q,%q],clientMutationId:"it191-same"}) { item { id name decision } userErrors { code } } }`, seed.version.String(), first.String(), second.String())
	for i := 0; i < 2; i++ {
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
		if strings.Contains(response, `"errors"`) || (i == 0 && strings.Contains(response, `"userErrors":[{"code":`)) {
			t.Fatalf("merge attempt %d: %s", i+1, response)
		}
	}
	var kept, absorbed int64
	if err := fx.h.AdminDB.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=? AND merged_into_id IS NULL AND name=?", fx.seed.TenantID, seed.version, "Mesas citadas").Count(&kept).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.AdminDB.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=? AND merged_into_id IS NOT NULL", fx.seed.TenantID, seed.version).Count(&absorbed).Error; err != nil {
		t.Fatal(err)
	}
	if kept != 1 || absorbed != 1 {
		t.Fatalf("merge rows kept=%d absorbed=%d; want one combined item", kept, absorbed)
	}
}
