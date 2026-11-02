//go:build integration

package resolvers

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestInventoryVoiceMergeMatrixIT188IT190IT192IT193(t *testing.T) {
	t.Run("IT-188 reviewer without room access cannot merge or learn room details", func(t *testing.T) {
		fx, d := seedVoiceDraft(t, auth.Employee)
		second := addVoiceMergeMatrixItem(t, fx, d, "Pratos extras", 3)
		if err := fx.h.AdminDB.Where("tenant_id=? AND membership_id=?", fx.seed.TenantID, fx.seed.MembershipID).Delete(&database.ResourceScope{}).Error; err != nil {
			t.Fatal(err)
		}
		meta, ok := requestctx.FromContext(fx.ctx)
		if !ok {
			t.Fatal("fixture context has no principal")
		}
		meta.Principal.Scopes = nil
		fx.ctx = requestctx.WithMetadata(fx.ctx, meta)
		query := fmt.Sprintf(`mutation { mergeInventoryItems(input:{originVersionId:%q,itemIds:[%q,%q],clientMutationId:"it188-no-room-access"}) { item { id name quantity } userErrors { code field message } } }`, d.version, d.item, second)
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
		if !strings.Contains(response, `"code":"FORBIDDEN"`) || strings.Contains(response, `"item":{`) {
			t.Fatalf("IT-188 response = %s; want denied merge without item data", response)
		}
		if strings.Contains(response, "Sala") || strings.Contains(response, "Pratos") {
			t.Fatalf("IT-188 response leaked inaccessible room details: %s", response)
		}
		assertVoiceMergeMatrixItems(t, fx, d, map[identity.ID]int{d.item: 5, second: 3}, map[identity.ID]string{d.item: "PENDING", second: "PENDING"})
	})

	t.Run("IT-190 failed merge save leaves original voice items and quantities readable", func(t *testing.T) {
		fx, d := seedVoiceDraft(t, auth.Employee)
		second := addVoiceMergeMatrixItem(t, fx, d, "Pratos extras", 3)
		if err := fx.h.AdminDB.Exec(`CREATE OR REPLACE FUNCTION reject_it190_merge() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected merge failure'; END $$; CREATE TRIGGER reject_it190_merge BEFORE UPDATE ON origins.inventory_items FOR EACH ROW EXECUTE FUNCTION reject_it190_merge()`).Error; err != nil {
			t.Fatal(err)
		}
		defer fx.h.AdminDB.Exec(`DROP TRIGGER IF EXISTS reject_it190_merge ON origins.inventory_items; DROP FUNCTION IF EXISTS reject_it190_merge()`)
		query := fmt.Sprintf(`mutation { mergeInventoryItems(input:{originVersionId:%q,itemIds:[%q,%q],clientMutationId:"it190-fail"}) { item { id name quantity } userErrors { code field message } } }`, d.version, d.item, second)
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
		if !strings.Contains(response, `"code":"DEPENDENCY_UNAVAILABLE"`) && !strings.Contains(response, `"errors"`) {
			t.Fatalf("IT-190 failed merge response = %s", response)
		}
		assertVoiceMergeMatrixItems(t, fx, d, map[identity.ID]int{d.item: 5, second: 3}, map[identity.ID]string{d.item: "PENDING", second: "PENDING"})
		read := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`query { inventoryDraft(originVersionId:%q) { environments { items { id name quantity decision } } } }`, d.version), fx.ctx, "", "")
		if !strings.Contains(read, `"name":"Pratos"`) || !strings.Contains(read, `"name":"Pratos extras"`) || !strings.Contains(read, `"quantity":5`) || !strings.Contains(read, `"quantity":3`) {
			t.Fatalf("IT-190 original items are not readable after rollback: %s", read)
		}
	})

	t.Run("IT-192 late note revision stays explicit after a decided item is regenerated", func(t *testing.T) {
		fx, d := seedVoiceDraft(t, auth.Employee)
		decision := fmt.Sprintf(`mutation { decideInventoryItems(input:{originVersionId:%q,decisions:[{itemId:%q,action:"ACCEPT",expectedVersion:1}],clientMutationId:"it192-decide"}) { items { id decision quantity } userErrors { code } } }`, d.version, d.item)
		decided := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, decision, fx.ctx, "", "")
		if !strings.Contains(decided, `"decision":"ACCEPTED"`) {
			t.Fatalf("IT-192 decision response = %s", decided)
		}
		lateRevision := identity.NewID()
		if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
			if err := tx.Create(&database.TranscriptRevision{ID: lateRevision, TenantID: fx.seed.TenantID, NoteID: d.noteA, RevisionNumber: 2, Text: "seis pratos", Kind: "HUMAN_EDIT", AuthorID: &fx.seed.MembershipID, CreatedAt: time.Now().UTC()}).Error; err != nil {
				return err
			}
			return tx.Table("transcription.audio_notes").Where("tenant_id=? AND id=?", fx.seed.TenantID, d.noteA).Update("current_revision_id", lateRevision).Error
		}); err != nil {
			t.Fatal(err)
		}
		out := regenerate(t, fx, d, d.version.String(), d.environment.String(), d.originRevision, fmt.Sprintf(`[%q]`, lateRevision), "it192-regenerate")
		if len(out.Errors) != 0 || out.Data.Regenerate.JobID == nil || len(out.Data.Regenerate.UserErrors) != 0 {
			t.Fatalf("IT-192 regeneration response = %+v", out)
		}
		var item database.InventoryItem
		if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, d.item).Take(&item).Error; err != nil {
			t.Fatal(err)
		}
		if item.Decision != "ACCEPTED" || item.Quantity == nil || *item.Quantity != 5 {
			t.Fatalf("IT-192 decided item = %+v; want accepted quantity 5", item)
		}
		read := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`query { inventoryDraft(originVersionId:%q) { voiceSummary { notesWithoutItem staleSuggestions } environments { items { id decision quantity attributeSources { sourceKind revisionId currentRevisionId stale } } } } }`, d.version), fx.ctx, "", "")
		if strings.Contains(read, fmt.Sprintf(`"revisionId":"%s"`, lateRevision)) || !strings.Contains(read, fmt.Sprintf(`"currentRevisionId":"%s"`, lateRevision)) || !strings.Contains(read, `"stale":true`) {
			t.Fatalf("IT-192 late revision should remain an explicit stale citation, not a new item source: %s", read)
		}
		if !strings.Contains(read, `"decision":"ACCEPTED"`) || !strings.Contains(read, `"quantity":5`) {
			t.Fatalf("IT-192 decided item changed after regeneration: %s", read)
		}
	})

	t.Run("IT-193 rejected voice candidate stays rejected after explicit regeneration", func(t *testing.T) {
		fx, d := seedVoiceDraft(t, auth.Employee)
		reject := fmt.Sprintf(`mutation { decideInventoryItems(input:{originVersionId:%q,decisions:[{itemId:%q,action:"REJECT",reason:"DUPLICATE",expectedVersion:1}],clientMutationId:"it193-reject"}) { items { id decision quantity } userErrors { code } } }`, d.version, d.item)
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, reject, fx.ctx, "", "")
		if !strings.Contains(response, `"decision":"REJECTED"`) {
			t.Fatalf("IT-193 rejection response = %s", response)
		}
		out := regenerate(t, fx, d, d.version.String(), d.environment.String(), d.originRevision, fmt.Sprintf(`[%q]`, d.revisionB), "it193-regenerate")
		if len(out.Errors) != 0 || out.Data.Regenerate.JobID == nil || len(out.Data.Regenerate.UserErrors) != 0 {
			t.Fatalf("IT-193 regeneration response = %+v", out)
		}
		var item database.InventoryItem
		if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, d.item).Take(&item).Error; err != nil {
			t.Fatal(err)
		}
		if item.Decision != "REJECTED" || item.Quantity == nil || *item.Quantity != 5 {
			t.Fatalf("IT-193 rejected candidate = %+v; want rejected quantity 5", item)
		}
		read := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`query { inventoryDraft(originVersionId:%q) { environments { items { id decision quantity } } } }`, d.version), fx.ctx, "", "")
		if strings.Contains(read, fmt.Sprintf(`"id":"%s","decision":"PENDING"`, d.item)) || !strings.Contains(read, fmt.Sprintf(`"id":"%s","decision":"REJECTED"`, d.item)) {
			t.Fatalf("IT-193 rejected candidate was resurrected in the draft: %s", read)
		}
	})
}

func addVoiceMergeMatrixItem(t *testing.T, fx api06Fixture, d voiceDraft, name string, quantity int) identity.ID {
	t.Helper()
	id := identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		confidence := 9000
		if err := tx.Create(&database.InventoryItem{ID: id, TenantID: fx.seed.TenantID, OriginVersionID: d.version, EnvironmentID: d.environment, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: name, Quantity: &quantity, Condition: "NOT_ASSESSABLE", Observation: "NOT_ASSESSABLE", ConfidenceBps: &confidence, Origin: "AI", Declared: true, Decision: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		note, revision := d.noteB, d.revisionB
		return tx.Create(&database.InventoryItemAttributeSource{ID: identity.NewID(), TenantID: fx.seed.TenantID, ItemID: id, OriginVersionID: d.version, AttributeName: "quantity", SourceKind: "VOICE", NoteID: &note, RevisionID: &revision, CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func assertVoiceMergeMatrixItems(t *testing.T, fx api06Fixture, d voiceDraft, quantities map[identity.ID]int, decisions map[identity.ID]string) {
	t.Helper()
	for id, quantity := range quantities {
		var item database.InventoryItem
		if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, id).Take(&item).Error; err != nil {
			t.Fatalf("load original item %s: %v", id, err)
		}
		if item.Quantity == nil || *item.Quantity != quantity || item.Decision != decisions[id] || item.MergedIntoID != nil {
			t.Errorf("original item %s after denied/failed merge = %+v; want quantity=%d decision=%s and no merge", id, item, quantity, decisions[id])
		}
		var sources int64
		if err := fx.h.AdminDB.Model(&database.InventoryItemAttributeSource{}).Where("tenant_id=? AND item_id=? AND source_kind='VOICE'", fx.seed.TenantID, id).Count(&sources).Error; err != nil {
			t.Fatal(err)
		}
		if sources == 0 {
			t.Errorf("original item %s lost its VOICE source", id)
		}
	}
}
