//go:build integration

package promote_inspection_to_origin

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestProcessRequiresConfirmationWhenOriginVersionChangesAfterReady(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	view := p.status()
	promotion := p.request("context-origin-version", view)
	p.readyDraft(*promotion.DraftVersionID, promotion.ID, "ACCEPTED")
	if err := p.h.AdminDB.Model(&database.Origin{}).Where("tenant_id=? AND asset_id=?", p.f.TenantID, p.f.AssetID).Update("version", 2).Error; err != nil {
		t.Fatal(err)
	}
	p.process(promotion.ID)
	final := p.loadPromotion(promotion.ID)
	origin := p.loadOrigin()
	assertPromotionState(t, p, final, origin, "NEEDS_CONFIRMATION", "CONTEXT_CHANGED", "DRAFT")
}

func TestProcessRequiresConfirmationWhenAnotherCaptureIsSubmitted(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	view := p.status()
	promotion := p.request("context-new-capture", view)
	p.readyDraft(*promotion.DraftVersionID, promotion.ID, "ACCEPTED")
	newCaptureID := identity.NewID()
	newMediaID := identity.NewID()
	err := p.h.WithinTenant(p.ctx, p.f.TenantID, func(tx *gorm.DB) error {
		var capture database.CaptureDraft
		if err := tx.Where("tenant_id=? AND id=?", p.f.TenantID, p.f.DraftID).Take(&capture).Error; err != nil {
			return err
		}
		responsibilityID := identity.NewID()
		if err := tx.Create(&database.Responsibility{ID: responsibilityID, TenantID: p.f.TenantID, InspectionID: p.f.InspectionID, ParticipantID: p.f.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: p.now.Add(time.Second), UpdatedAt: p.now.Add(time.Second)}).Error; err != nil {
			return err
		}
		capture.ID = newCaptureID
		capture.ResponsibilityID = responsibilityID
		capture.CreatedAt = p.now.Add(time.Second)
		capture.UpdatedAt = p.now.Add(time.Second)
		if err := tx.Create(&capture).Error; err != nil {
			return err
		}
		var media database.MediaObject
		if err := tx.Where("tenant_id=? AND id=?", p.f.TenantID, p.f.MediaID).Take(&media).Error; err != nil {
			return err
		}
		media.ID = newMediaID
		media.ResponsibilityID = responsibilityID
		media.IdempotencyKey = "context-new-capture-" + newMediaID.String()
		media.ObjectKey = "tenant/" + p.f.TenantID.String() + "/media/" + newMediaID.String()
		media.CreatedAt = p.now.Add(time.Second)
		if err := tx.Create(&media).Error; err != nil {
			return err
		}
		var answers []database.RequirementAnswer
		if err := tx.Where("tenant_id=? AND draft_id=?", p.f.TenantID, p.f.DraftID).Find(&answers).Error; err != nil {
			return err
		}
		for _, answer := range answers {
			answer.ID = identity.NewID()
			answer.DraftID = newCaptureID
			var mediaIDs []identity.ID
			if err := json.Unmarshal(answer.MediaIDs, &mediaIDs); err != nil {
				return err
			}
			for i := range mediaIDs {
				if mediaIDs[i] == p.f.MediaID {
					mediaIDs[i] = newMediaID
				}
			}
			encoded, err := json.Marshal(mediaIDs)
			if err != nil {
				return err
			}
			answer.MediaIDs = encoded
			answer.CreatedAt = p.now.Add(time.Second)
			answer.UpdatedAt = p.now.Add(time.Second)
			if err := tx.Create(&answer).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	p.process(promotion.ID)
	final := p.loadPromotion(promotion.ID)
	origin := p.loadOrigin()
	assertPromotionState(t, p, final, origin, "NEEDS_CONFIRMATION", "CONTEXT_CHANGED", "DRAFT")
	if final.CaptureDraftID == newCaptureID {
		t.Fatal("promotion unexpectedly changed its pinned capture")
	}
}

func TestProcessTwiceActivatesExactlyOneVersion(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	promotion := p.request("duplicate-process", p.status())
	p.readyDraft(*promotion.DraftVersionID, promotion.ID, "ACCEPTED")
	p.process(promotion.ID)
	p.process(promotion.ID)
	final := p.loadPromotion(promotion.ID)
	origin := p.loadOrigin()
	if final.Status != "COMPLETED" || final.OriginVersionID == nil || origin.ActiveVersionID == nil || *origin.ActiveVersionID != *final.OriginVersionID {
		t.Fatalf("origin and promotion did not complete consistently: promotion=%+v origin=%+v", final, origin)
	}
	var active, promotionVersions int64
	if err := p.h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND origin_id=? AND status='ACTIVE'", p.f.TenantID, origin.ID).Count(&active).Error; err != nil {
		t.Fatal(err)
	}
	if err := p.h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", p.f.TenantID, *final.OriginVersionID).Count(&promotionVersions).Error; err != nil {
		t.Fatal(err)
	}
	if active != 1 || promotionVersions != 1 {
		t.Fatalf("duplicate activation created versions: active=%d promotionVersionRows=%d", active, promotionVersions)
	}
}

func TestProcessForDraftAndProcessConcurrentActivateOnce(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	promotion := p.request("duplicate-process-for-draft", p.status())
	p.readyDraft(*promotion.DraftVersionID, promotion.ID, "ACCEPTED")
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errors <- Process(p.ctx, p.h.DB, ProcessDependencies{Inventory: p.inventory, Now: func() time.Time { return p.now }}, p.f.TenantID, promotion.ID)
	}()
	go func() {
		defer wg.Done()
		errors <- ProcessForDraft(p.ctx, p.h.DB, p.f.TenantID, *promotion.DraftVersionID, ProcessDependencies{Inventory: p.inventory, Now: func() time.Time { return p.now }})
	}()
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	final := p.loadPromotion(promotion.ID)
	origin := p.loadOrigin()
	var active int64
	if err := p.h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND origin_id=? AND status='ACTIVE'", p.f.TenantID, origin.ID).Count(&active).Error; err != nil {
		t.Fatal(err)
	}
	if final.Status != "COMPLETED" || final.OriginVersionID == nil || origin.ActiveVersionID == nil || *origin.ActiveVersionID != *final.OriginVersionID || active != 1 {
		t.Fatalf("concurrent entry points did not converge on one active version: promotion=%+v origin=%+v active=%d", final, origin, active)
	}
}

func TestProcessCancelsPromotionWhenItsDraftWasDiscarded(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	promotion := p.request("discarded-promotion-draft", p.status())
	p.readyDraft(*promotion.DraftVersionID, promotion.ID, "ACCEPTED")
	if err := p.h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", p.f.TenantID, *promotion.DraftVersionID).Updates(map[string]any{"status": "DISCARDED", "draft_state": "READY", "discarded_at": p.now}).Error; err != nil {
		t.Fatal(err)
	}
	p.process(promotion.ID)
	final := p.loadPromotion(promotion.ID)
	origin := p.loadOrigin()
	assertPromotionState(t, p, final, origin, "CANCELED", "DRAFT_DISCARDED", "DISCARDED")
}

func TestProcessKeepsUnusableCatalogItemOutOfPromotedOrigin(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	status := p.status()
	promotion := p.request("evidence-missing-at-process", status)
	missingMediaID := identity.NewID()
	var missingMedia database.MediaObject
	if err := p.h.AdminDB.Where("tenant_id=? AND id=?", p.f.TenantID, p.f.MediaID).Take(&missingMedia).Error; err != nil {
		t.Fatal(err)
	}
	missingMedia.ID = missingMediaID
	missingMedia.Status = "PURGED"
	missingMedia.IdempotencyKey = "deleted-evidence-" + missingMediaID.String()
	missingMedia.ObjectKey = "tenant/" + p.f.TenantID.String() + "/media/" + missingMediaID.String()
	if err := p.h.AdminDB.Create(&missingMedia).Error; err != nil {
		t.Fatal(err)
	}
	if err := p.h.WithinTenant(p.ctx, p.f.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", p.f.TenantID, *promotion.DraftVersionID).Update("draft_state", "READY").Error; err != nil {
			return err
		}
		var env database.InventoryEnvironment
		if err := tx.Where("tenant_id=? AND origin_version_id=?", p.f.TenantID, *promotion.DraftVersionID).Take(&env).Error; err != nil {
			return err
		}
		itemID := identity.NewID()
		item := database.InventoryItem{ID: itemID, TenantID: p.f.TenantID, OriginVersionID: *promotion.DraftVersionID, EnvironmentID: env.ID, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Item sem evidência disponível", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", Version: 1, CreatedAt: p.now, UpdatedAt: p.now}
		if err := tx.Create(&item).Error; err != nil {
			return err
		}
		return tx.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: p.f.TenantID, ItemID: itemID, Kind: "PHOTO", MediaID: missingMediaID, Position: 0, CreatedAt: p.now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	p.process(promotion.ID)
	final := p.loadPromotion(promotion.ID)
	origin := p.loadOrigin()
	if final.Status != "COMPLETED" || final.OriginVersionID == nil || origin.ActiveVersionID == nil || *origin.ActiveVersionID != *final.OriginVersionID {
		t.Fatalf("available capture should become the origin without the unusable catalog item: promotion=%+v origin=%+v", final, origin)
	}
	var included int64
	if err := p.h.AdminDB.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=? AND decision IN ('ACCEPTED','EDITED','ADDED')", p.f.TenantID, *final.OriginVersionID).Count(&included).Error; err != nil {
		t.Fatal(err)
	}
	if included != 0 {
		t.Fatalf("unusable catalog item was included: %d", included)
	}
	blocked := newPromotionFixture(t, "SUBMITTED")
	blockedStatus := blocked.status()
	if blockedStatus.Eligible {
		t.Fatalf("incomplete inspection was considered eligible: %+v", blockedStatus.Impediments)
	}
	if _, err := blocked.bus.Send(blocked.ctx, Command{TenantID: blocked.f.TenantID, InspectionID: blocked.f.InspectionID, ClientMutationID: "blocked-request", ConfirmationDigest: blockedStatus.ConfirmationDigest}); err == nil {
		t.Fatal("request with an eligibility impediment should fail")
	} else if code, _, _ := apperror.Public(err); code != apperror.InvalidState {
		t.Fatalf("request with impediment code=%v; want InvalidState", code)
	}
}

func TestProcessHonorsDraftDecisionsAndAutoAcceptsOnlyPendingWithEvidence(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	promotion := p.request("decision-matrix", p.status())
	itemIDs := p.readyDraft(*promotion.DraftVersionID, promotion.ID, "REJECTED", "EDITED", "ADDED", "PENDING", "PENDING_NO_EVIDENCE")
	p.process(promotion.ID)
	final := p.loadPromotion(promotion.ID)
	origin := p.loadOrigin()
	if final.Status != "COMPLETED" || origin.ActiveVersionID == nil || *origin.ActiveVersionID != *final.OriginVersionID {
		t.Fatalf("promotion did not activate: promotion=%+v origin=%+v", final, origin)
	}
	want := []string{"EDITED", "ADDED", "ACCEPTED"}
	for index, id := range itemIDs[1:4] {
		var item database.InventoryItem
		if err := p.h.AdminDB.Where("tenant_id=? AND id=?", p.f.TenantID, id).Take(&item).Error; err != nil {
			t.Fatal(err)
		}
		if item.Decision != want[index] {
			t.Fatalf("item %s decision=%s; want %s", id, item.Decision, want[index])
		}
		if index == 2 && (item.AutoAcceptedByPromotionID == nil || *item.AutoAcceptedByPromotionID != promotion.ID) {
			t.Fatalf("evidence-backed pending item lacks auto-accept marker: %+v", item)
		}
	}
	var rejectedCount int64
	if err := p.h.AdminDB.Model(&database.InventoryItem{}).Where("tenant_id=? AND id=? AND decision='REJECTED'", p.f.TenantID, itemIDs[0]).Count(&rejectedCount).Error; err != nil {
		t.Fatal(err)
	}
	if rejectedCount != 1 {
		t.Fatalf("source draft's rejected decision was not retained in history: count=%d", rejectedCount)
	}
	baseline, err := p.inventory.BaselineInventory(p.ctx, p.f.TenantID, p.f.AssetID, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Environments) != 1 || len(baseline.Environments[0].Items) != 3 {
		t.Fatalf("active origin should expose only EDITED, ADDED and evidence-backed accepted items: %+v", baseline.Environments)
	}
	for _, item := range baseline.Environments[0].Items {
		if item.Item.Decision == "REJECTED" || item.Item.Decision == "PENDING" || item.Item.ID == itemIDs[4] {
			t.Fatalf("customer-facing active origin exposed rejected or pending item: %+v", item.Item)
		}
	}
}

func TestProcessMarksEmptyDraftFailedWithoutChangingActiveOrigin(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	promotion := p.request("empty-inventory", p.status())
	if err := p.h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", p.f.TenantID, *promotion.DraftVersionID).Update("draft_state", "READY").Error; err != nil {
		t.Fatal(err)
	}
	if err := p.h.AdminDB.Where("tenant_id=? AND origin_version_id=?", p.f.TenantID, *promotion.DraftVersionID).Delete(&database.InventoryEnvironment{}).Error; err != nil {
		t.Fatal(err)
	}
	p.process(promotion.ID)
	final := p.loadPromotion(promotion.ID)
	origin := p.loadOrigin()
	assertPromotionState(t, p, final, origin, "FAILED", "EMPTY_INVENTORY", "DRAFT")
}

func TestProcessActivatesCapturedEnvironmentWithoutCatalogItems(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	promotion := p.request("empty-items", p.status())
	p.readyDraft(*promotion.DraftVersionID, promotion.ID)
	if err := p.h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", p.f.TenantID, *promotion.DraftVersionID).Update("source", "CAPTURE").Error; err != nil {
		t.Fatal(err)
	}
	p.process(promotion.ID)
	final := p.loadPromotion(promotion.ID)
	origin := p.loadOrigin()
	if final.Status != "COMPLETED" || final.OriginVersionID == nil || origin.ActiveVersionID == nil || *origin.ActiveVersionID != *final.OriginVersionID {
		t.Fatalf("inspection with an empty catalog did not become the origin: promotion=%+v origin=%+v", final, origin)
	}
	var environments, items int64
	if err := p.h.AdminDB.Model(&database.InventoryEnvironment{}).Where("tenant_id=? AND origin_version_id=?", p.f.TenantID, *final.OriginVersionID).Count(&environments).Error; err != nil {
		t.Fatal(err)
	}
	if err := p.h.AdminDB.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=?", p.f.TenantID, *final.OriginVersionID).Count(&items).Error; err != nil {
		t.Fatal(err)
	}
	if environments != 1 || items != 0 {
		t.Fatalf("unexpected empty origin inventory: environments=%d items=%d", environments, items)
	}
}

func TestProcessRejectsPromotionWhenCaptureEvidenceDisappears(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	promotion := p.request("capture-evidence-purged", p.status())
	p.readyDraft(*promotion.DraftVersionID, promotion.ID)
	if err := p.h.AdminDB.Model(&database.MediaObject{}).Where("tenant_id=? AND id=?", p.f.TenantID, p.f.MediaID).Update("status", "PURGED").Error; err != nil {
		t.Fatal(err)
	}
	p.process(promotion.ID)
	assertPromotionState(t, p, p.loadPromotion(promotion.ID), p.loadOrigin(), "FAILED", "EVIDENCE_UNAVAILABLE", "DRAFT")
}

func assertPromotionState(t *testing.T, p *promotionFixture, promotion database.InspectionOriginPromotion, origin database.Origin, status, failure, draftStatus string) {
	t.Helper()
	if promotion.Status != status || promotion.FailureCode == nil || *promotion.FailureCode != failure {
		t.Fatalf("promotion status/failure=(%s,%v); want (%s,%s)", promotion.Status, promotion.FailureCode, status, failure)
	}
	if origin.ActiveVersionID != nil {
		t.Fatalf("origin changed despite failed/blocked processing: activeVersionId=%s", *origin.ActiveVersionID)
	}
	var draft database.OriginVersion
	if promotion.DraftVersionID == nil {
		t.Fatal("promotion has no draft version")
	}
	if err := p.h.AdminDB.Where("tenant_id=? AND id=?", p.f.TenantID, *promotion.DraftVersionID).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	if draft.Status != draftStatus {
		t.Fatalf("draft status=%s; want %s", draft.Status, draftStatus)
	}
	var marked int64
	if err := p.h.AdminDB.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=? AND auto_accepted_by_promotion_id IS NOT NULL", p.f.TenantID, *promotion.DraftVersionID).Count(&marked).Error; err != nil {
		t.Fatal(err)
	}
	if marked != 0 {
		t.Fatalf("blocked promotion left %d items auto-accepted", marked)
	}
}

func TestRequestRepeatedMutationReturnsSameOperationAfterFirstProcess(t *testing.T) {
	p := newPromotionFixture(t, "COMPLETED")
	view := p.status()
	first := p.request("same-mutation-process", view)
	p.readyDraft(*first.DraftVersionID, first.ID, "ACCEPTED")
	p.process(first.ID)
	raw, err := p.bus.Send(p.ctx, Command{TenantID: p.f.TenantID, InspectionID: p.f.InspectionID, ClientMutationID: "same-mutation-process", Purpose: "INVENTORY_UPDATE", ConfirmationDigest: view.ConfirmationDigest})
	if err != nil {
		t.Fatal(err)
	}
	second := raw.(Result).Promotion
	if second.ID != first.ID || second.Status != "COMPLETED" || second.OriginVersionID == nil || *second.OriginVersionID != *first.DraftVersionID {
		t.Fatalf("repeated mutation did not return completed operation: first=%+v second=%+v", first, second)
	}
}
