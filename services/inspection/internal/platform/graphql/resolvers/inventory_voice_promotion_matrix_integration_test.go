//go:build integration

package resolvers

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"inspection/libs/identity"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	promoteorigin "inspection/services/inspection/internal/features/origins/promote_inspection_to_origin"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
)

type promotionMutationResult struct {
	Data struct {
		Promote struct {
			Promotion *struct {
				ID             string  `json:"id"`
				Status         string  `json:"status"`
				DraftVersionID *string `json:"draftVersionId"`
			} `json:"promotion"`
			UserErrors []userErrors `json:"userErrors"`
		} `json:"promoteInspectionToOrigin"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func requestVoicePromotion(t *testing.T, fx api06Fixture, mutationID string) promotionMutationResult {
	t.Helper()
	status := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`query { inspectionOriginPromotion(inspectionId:%q) { confirmationDigest } }`, fx.seed.InspectionID), fx.ctx, "", "")
	var digest struct {
		Data struct {
			Status struct {
				ConfirmationDigest string `json:"confirmationDigest"`
			} `json:"inspectionOriginPromotion"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(status), &digest); err != nil || digest.Data.Status.ConfirmationDigest == "" {
		t.Fatalf("promotion status = %s err=%v", status, err)
	}
	query := fmt.Sprintf(`mutation { promoteInspectionToOrigin(input:{inspectionId:%q,clientMutationId:%q,confirmationDigest:%q}) { promotion { id status draftVersionId } userErrors { code field message } } }`, fx.seed.InspectionID, mutationID, digest.Data.Status.ConfirmationDigest)
	var out promotionMutationResult
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
	if err := json.Unmarshal([]byte(response), &out); err != nil {
		t.Fatalf("promotion response = %s err=%v", response, err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("promotion GraphQL errors = %+v response=%s", out.Errors, response)
	}
	return out
}

func seedPromotionVoiceDraft(t *testing.T, role string) (api06Fixture, voiceDraft) {
	t.Helper()
	fx, d := seedVoiceDraft(t, role)
	if err := fx.h.AdminDB.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.InspectionID).Update("status", "COMPLETED").Error; err != nil {
		t.Fatal(err)
	}
	return fx, d
}

func processVoicePromotion(t *testing.T, fx api06Fixture, promotionID string) error {
	t.Helper()
	id, err := identity.ParseID(promotionID)
	if err != nil {
		t.Fatal(err)
	}
	service := inventorycore.Service{DB: fx.h.DB, Within: fx.h.WithinTenant}
	return promoteorigin.Process(fx.ctx, fx.h.DB, promoteorigin.ProcessDependencies{Inventory: service}, fx.seed.TenantID, id)
}

func TestInventoryVoicePromotionMatrixIT216IT218ToIT221(t *testing.T) {
	t.Run("IT-216 promotion is denied without revealing voice item details", func(t *testing.T) {
		fx, d := seedPromotionVoiceDraft(t, auth.Viewer)
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`mutation { promoteInspectionToOrigin(input:{inspectionId:%q,clientMutationId:"it216-denied",confirmationDigest:"untrusted"}) { promotion { id } userErrors { code field message } } }`, fx.seed.InspectionID), fx.ctx, "", "")
		if !strings.Contains(response, `"code":"NOT_FOUND"`) || strings.Contains(response, "Pratos") || strings.Contains(response, d.item.String()) {
			t.Fatalf("unauthorized promotion response revealed item details or was not denied: %s", response)
		}
	})

	t.Run("IT-218 interrupted activation preserves durable state and pending voice count", func(t *testing.T) {
		fx, d := seedPromotionVoiceDraft(t, auth.TenantAdmin)
		bulk := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`mutation { bulkAcceptInventoryEnvironment(input:{originVersionId:%q,environmentId:%q,minimumConfidenceBps:8500,clientMutationId:"it218-bulk"}) { acceptedCount userErrors { code } } }`, d.version, d.environment), fx.ctx, "", "")
		if !strings.Contains(bulk, `"acceptedCount":0`) || !strings.Contains(bulk, `"code":"INVALID_STATE"`) {
			t.Fatalf("bulk accept must leave voice-backed items pending: %s", bulk)
		}
		requested := requestVoicePromotion(t, fx, "it218-interrupted")
		promotion := requested.Data.Promote.Promotion
		if promotion == nil || promotion.DraftVersionID == nil {
			t.Fatalf("promotion request = %+v", requested.Data.Promote)
		}
		if err := fx.h.AdminDB.Exec(`CREATE OR REPLACE FUNCTION fail_it218_activation_decision() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='ACTIVATE' THEN RAISE EXCEPTION 'injected activation failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_it218_activation_decision BEFORE INSERT ON origins.inventory_decisions FOR EACH ROW EXECUTE FUNCTION fail_it218_activation_decision()`).Error; err != nil {
			t.Fatal(err)
		}
		defer fx.h.AdminDB.Exec(`DROP TRIGGER IF EXISTS fail_it218_activation_decision ON origins.inventory_decisions; DROP FUNCTION IF EXISTS fail_it218_activation_decision()`)
		if err := processVoicePromotion(t, fx, promotion.ID); err == nil {
			t.Fatal("injected activation failure was not returned")
		}
		var stored database.InspectionOriginPromotion
		if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, promotion.ID).Take(&stored).Error; err != nil {
			t.Fatal(err)
		}
		var item database.InventoryItem
		if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, d.item).Take(&item).Error; err != nil {
			t.Fatal(err)
		}
		var origin database.Origin
		if err := fx.h.AdminDB.Where("tenant_id=? AND asset_id=?", fx.seed.TenantID, fx.seed.AssetID).Take(&origin).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Status != "ACTIVATING" || item.Decision != "PENDING" || origin.ActiveVersionID == nil || *origin.ActiveVersionID != fx.originVersionID {
			t.Fatalf("state after interrupted activation: promotion=%s item=%s active=%v", stored.Status, item.Decision, origin.ActiveVersionID)
		}
		if err := fx.h.AdminDB.Exec(`DROP TRIGGER IF EXISTS fail_it218_activation_decision ON origins.inventory_decisions; DROP FUNCTION IF EXISTS fail_it218_activation_decision()`).Error; err != nil {
			t.Fatal(err)
		}
		if err := processVoicePromotion(t, fx, promotion.ID); err != nil {
			t.Fatal(err)
		}
		var completed database.AuditEvent
		if err := fx.h.AdminDB.Where("tenant_id=? AND action='origin.inspection_promotion_completed' AND target_id=?", fx.seed.TenantID, promotion.ID).Take(&completed).Error; err != nil || completed.Reason != "voice_pending=1" {
			t.Fatalf("completed voice pending count = %q err=%v", completed.Reason, err)
		}
	})

	t.Run("IT-219 replay after success does not duplicate active origin or items", func(t *testing.T) {
		fx, d := seedPromotionVoiceDraft(t, auth.TenantAdmin)
		first := requestVoicePromotion(t, fx, "it219-replay")
		promotion := first.Data.Promote.Promotion
		if promotion == nil {
			t.Fatalf("first promotion = %+v", first.Data.Promote)
		}
		if err := processVoicePromotion(t, fx, promotion.ID); err != nil {
			t.Fatal(err)
		}
		second := requestVoicePromotion(t, fx, "it219-replay")
		if second.Data.Promote.Promotion == nil || second.Data.Promote.Promotion.ID != promotion.ID || second.Data.Promote.Promotion.Status != "COMPLETED" {
			t.Fatalf("replayed promotion = %+v", second.Data.Promote)
		}
		var active, items int64
		if err := fx.h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND origin_id=(SELECT id FROM origins.origins WHERE tenant_id=? AND asset_id=?) AND status='ACTIVE'", fx.seed.TenantID, fx.seed.TenantID, fx.seed.AssetID).Count(&active).Error; err != nil {
			t.Fatal(err)
		}
		if err := fx.h.AdminDB.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=?", fx.seed.TenantID, d.version).Count(&items).Error; err != nil {
			t.Fatal(err)
		}
		if active != 1 || items != 1 {
			t.Fatalf("replay duplicated records: active versions=%d items=%d", active, items)
		}
	})

	t.Run("IT-220 promotion waits for catalog readiness and exposes the pending state", func(t *testing.T) {
		fx, d := seedPromotionVoiceDraft(t, auth.TenantAdmin)
		if err := fx.h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, d.version).Update("draft_state", "PREPARING").Error; err != nil {
			t.Fatal(err)
		}
		if err := fx.h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND kind='CATALOG'", fx.seed.TenantID, d.version).Update("status", "PENDING").Error; err != nil {
			t.Fatal(err)
		}
		requested := requestVoicePromotion(t, fx, "it220-catalog-pending")
		promotion := requested.Data.Promote.Promotion
		if promotion == nil || promotion.Status != "PREPARING" || promotion.DraftVersionID == nil {
			t.Fatalf("promotion before catalog readiness = %+v", requested.Data.Promote)
		}
		if err := processVoicePromotion(t, fx, promotion.ID); err != nil {
			t.Fatal(err)
		}
		status := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`query { inspectionOriginPromotion(inspectionId:%q) { currentPromotion { status draftVersionId } conflictingDraft { draftState } } }`, fx.seed.InspectionID), fx.ctx, "", "")
		if !strings.Contains(status, `"status":"PREPARING"`) || !strings.Contains(status, `"draftState":"PREPARING"`) {
			t.Fatalf("catalog readiness is not reflected in promotion status: %s", status)
		}
		var pending int64
		if err := fx.h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND status IN ('PENDING','RUNNING')", fx.seed.TenantID, d.version).Count(&pending).Error; err != nil || pending == 0 {
			t.Fatalf("pending catalog jobs=%d err=%v", pending, err)
		}
	})

	t.Run("IT-221 active origin transitions and invalidation preserve voice item history", func(t *testing.T) {
		fx, d := seedPromotionVoiceDraft(t, auth.TenantAdmin)
		requested := requestVoicePromotion(t, fx, "it221-origin-transition")
		promotion := requested.Data.Promote.Promotion
		if promotion == nil {
			t.Fatalf("promotion = %+v", requested.Data.Promote)
		}
		if err := processVoicePromotion(t, fx, promotion.ID); err != nil {
			t.Fatal(err)
		}
		if promotion.DraftVersionID == nil {
			t.Fatal("promotion has no draft version")
		}
		invalidate := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`mutation { invalidateOriginVersion(input:{versionId:%q,clientMutationId:"it221-invalidate"}) { version { status } userErrors { code } } }`, *promotion.DraftVersionID), fx.ctx, "", "")
		if !strings.Contains(invalidate, `"status":"INVALIDATED"`) || strings.Contains(invalidate, `"code":"`) {
			t.Fatalf("invalidation after activation = %s", invalidate)
		}
		var item database.InventoryItem
		if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, d.item).Take(&item).Error; err != nil {
			t.Fatal(err)
		}
		var sources int64
		if err := fx.h.AdminDB.Model(&database.InventoryItemAttributeSource{}).Where("tenant_id=? AND item_id=? AND source_kind='VOICE'", fx.seed.TenantID, d.item).Count(&sources).Error; err != nil {
			t.Fatal(err)
		}
		if item.Decision != "PENDING" || sources == 0 {
			t.Fatalf("voice history after origin invalidation: decision=%s sources=%d", item.Decision, sources)
		}
	})
}
