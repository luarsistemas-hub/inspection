//go:build integration

package promote_inspection_to_origin

import (
	"encoding/json"
	"strings"
	"testing"

	"inspection/libs/identity"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/database"
)

// voiceNoteFor stores a completed note with its revision and returns the pair.
func (p *promotionFixture) voiceNoteFor(text string) (identity.ID, identity.ID) {
	p.t().Helper()
	note, revision := identity.NewID(), identity.NewID()
	if err := p.h.AdminDB.Exec(`INSERT INTO transcription.audio_notes (tenant_id,id,inspection_id,responsibility_id,client_note_id,requirement_key,consent_version,disclosure_version,target_kind,photo_media_id,source_media_id,duration_ms,content_sha256,content_type,size_bytes,status,anchor_status,no_speech,created_at,updated_at)
VALUES (?,?,?,?,?,'photo:overview','v1','v1','PHOTO',?,?,3000,?,'audio/webm',1000,'COMPLETED','VALID',false,now(),now())`, p.f.TenantID, note, p.f.InspectionID, p.f.ResponsibilityID, note, p.f.MediaID, p.f.MediaID, strings.Repeat("a", 64)).Error; err != nil {
		p.t().Fatal(err)
	}
	if err := p.h.AdminDB.Create(&database.TranscriptRevision{ID: revision, TenantID: p.f.TenantID, NoteID: note, RevisionNumber: 1, Text: text, Kind: "PARTICIPANT_DECLARATION", CreatedAt: p.now}).Error; err != nil {
		p.t().Fatal(err)
	}
	return note, revision
}

// voiceSource marks an attribute of an item as declared by the given note revision.
func (p *promotionFixture) voiceSource(draft, item, note, revision identity.ID, attribute string) {
	p.t().Helper()
	if err := p.h.AdminDB.Create(&database.InventoryItemAttributeSource{ID: identity.NewID(), TenantID: p.f.TenantID, ItemID: item, OriginVersionID: draft, AttributeName: attribute, SourceKind: "VOICE", NoteID: &note, RevisionID: &revision, CreatedAt: p.now}).Error; err != nil {
		p.t().Fatal(err)
	}
}

func (p *promotionFixture) itemDecision(id identity.ID) string {
	p.t().Helper()
	var item database.InventoryItem
	if err := p.h.AdminDB.Where("tenant_id=? AND id=?", p.f.TenantID, id).Take(&item).Error; err != nil {
		p.t().Fatal(err)
	}
	return item.Decision
}

func (p *promotionFixture) completionNote(promotionID identity.ID) string {
	p.t().Helper()
	var event database.AuditEvent
	if err := p.h.AdminDB.Where("tenant_id=? AND action='origin.inspection_promotion_completed' AND target_id=?", p.f.TenantID, promotionID.String()).Take(&event).Error; err != nil {
		p.t().Fatal(err)
	}
	return event.Reason
}

func (p *promotionFixture) activationCounts(draft identity.ID) inventorycore.ItemCounts {
	p.t().Helper()
	var decision database.InventoryDecision
	if err := p.h.AdminDB.Where("tenant_id=? AND origin_version_id=? AND action='ACTIVATE'", p.f.TenantID, draft).Take(&decision).Error; err != nil {
		p.t().Fatal(err)
	}
	var counts inventorycore.ItemCounts
	if err := json.Unmarshal(decision.After, &counts); err != nil {
		p.t().Fatal(err)
	}
	return counts
}

func TestPromotionLeavesVoiceSupportedItemsPendingAndCountsThemApart(t *testing.T) {
	t.Run("IT-209 IT-210 IT-213 IT-215 pending voice items never block, are never forced and are counted apart", func(t *testing.T) {
		p := newPromotionFixture(t, "COMPLETED")
		promotion := p.request("voice-promotion", p.status())
		draft := *promotion.DraftVersionID
		items := p.readyDraft(draft, promotion.ID, "PENDING", "PENDING", "PENDING", "PENDING", "ACCEPTED")
		note, revision := p.voiceNoteFor("cinco pratos")
		for _, item := range items[:3] {
			p.voiceSource(draft, item, note, revision, "quantity")
		}
		p.process(promotion.ID)
		final := p.loadPromotion(promotion.ID)
		if final.Status != "COMPLETED" || final.OriginVersionID == nil {
			t.Fatalf("a pending voice item must not block the promotion: %+v", final)
		}
		for _, item := range items[:3] {
			if decision := p.itemDecision(item); decision != "PENDING" {
				t.Fatalf("voice-supported item %s was %s: it must stay pending outside the active inventory", item, decision)
			}
		}
		if decision := p.itemDecision(items[3]); decision != "ACCEPTED" {
			t.Fatalf("the visual pending item is auto-accepted as before, got %s", decision)
		}
		if reason := p.autoAcceptNote(promotion.ID); reason != "accepted=1 pending_without_evidence=0 voice_pending=3" {
			t.Fatalf("auto-accept note = %q: voice-pending items are not items without evidence", reason)
		}
		if note := p.completionNote(promotion.ID); note != "voice_pending=3" {
			t.Fatalf("completion note = %q, want the separate count of the three items awaiting review", note)
		}
		if counts := p.activationCounts(*final.OriginVersionID); counts.VoicePending != 3 || counts.NotReviewed != 3 || counts.Included != 2 {
			t.Fatalf("activation counts = %+v, want voicePending=3 notReviewed=3 included=2", counts)
		}
	})
	t.Run("IT-214 without voice items there is no artificial count", func(t *testing.T) {
		p := newPromotionFixture(t, "COMPLETED")
		promotion := p.request("visual-promotion", p.status())
		p.readyDraft(*promotion.DraftVersionID, promotion.ID, "PENDING", "ACCEPTED")
		p.process(promotion.ID)
		final := p.loadPromotion(promotion.ID)
		if final.Status != "COMPLETED" || p.completionNote(promotion.ID) != "" {
			t.Fatalf("promotion = %+v note=%q", final, p.completionNote(promotion.ID))
		}
		if counts := p.activationCounts(*final.OriginVersionID); counts.VoicePending != 0 {
			t.Fatalf("counts = %+v", counts)
		}
	})
	t.Run("IT-212 IT-217 an item already reviewed by a person is accepted by the promotion like any other", func(t *testing.T) {
		p := newPromotionFixture(t, "COMPLETED")
		promotion := p.request("reviewed-promotion", p.status())
		draft := *promotion.DraftVersionID
		items := p.readyDraft(draft, promotion.ID, "PENDING")
		note, revision := p.voiceNoteFor("seis copos")
		p.voiceSource(draft, items[0], note, revision, "quantity")
		reviewer := p.f.MembershipID
		if err := p.h.AdminDB.Create(&database.InventoryItemAttributeSource{ID: identity.NewID(), TenantID: p.f.TenantID, ItemID: items[0], OriginVersionID: draft, AttributeName: "quantity", SourceKind: "HUMAN_REVIEW", ReviewedBy: &reviewer, CreatedAt: p.now}).Error; err != nil {
			t.Fatal(err)
		}
		p.process(promotion.ID)
		if decision := p.itemDecision(items[0]); decision != "ACCEPTED" {
			t.Fatalf("decision = %s: a reviewed voice attribute no longer holds the item back", decision)
		}
		if note := p.completionNote(promotion.ID); note != "" {
			t.Fatalf("completion note = %q, nothing awaits review", note)
		}
	})
}

func (p *promotionFixture) autoAcceptNote(promotionID identity.ID) string {
	p.t().Helper()
	var event database.AuditEvent
	if err := p.h.AdminDB.Where("tenant_id=? AND action='AUTO_ACCEPT_BY_PROMOTION' AND target_id=?", p.f.TenantID, promotionID.String()).Take(&event).Error; err != nil {
		p.t().Fatal(err)
	}
	return event.Reason
}

// catalogJob stores a CATALOG job of the draft for one room generation.
func (p *promotionFixture) catalogJob(draft identity.ID, key string, generation int, errorCode *string) identity.ID {
	p.t().Helper()
	id := identity.NewID()
	if err := p.h.AdminDB.Create(&database.InventoryJob{ID: id, TenantID: p.f.TenantID, InspectionID: p.f.InspectionID, OriginVersionID: draft, Kind: "CATALOG", RequirementKey: key, Generation: generation, Status: "SUCCEEDED", NextAttemptAt: p.now, ErrorCode: errorCode, CreatedAt: p.now, UpdatedAt: p.now}).Error; err != nil {
		p.t().Fatal(err)
	}
	return id
}

func TestTruncationOnlyBlocksPromotionWhileItIsTheLatestGenerationOfTheRoom(t *testing.T) {
	truncated := "TRUNCATED_ITEMS"
	truncatedImages := "TRUNCATED_IMAGES"
	t.Run("a truncated latest generation still blocks", func(t *testing.T) {
		p := newPromotionFixture(t, "COMPLETED")
		promotion := p.request("truncated-latest", p.status())
		draft := *promotion.DraftVersionID
		p.readyDraft(draft, promotion.ID, "ACCEPTED")
		p.catalogJob(draft, "env:truncated", 1, &truncated)
		p.process(promotion.ID)
		if final := p.loadPromotion(promotion.ID); final.Status != "FAILED" || final.FailureCode == nil || *final.FailureCode != "INCOMPLETE_ANALYSIS" {
			t.Fatalf("promotion = %+v, want INCOMPLETE_ANALYSIS", final)
		}
	})
	t.Run("image-only truncation remains diagnostic and does not block promotion", func(t *testing.T) {
		p := newPromotionFixture(t, "COMPLETED")
		promotion := p.request("truncated-images", p.status())
		draft := *promotion.DraftVersionID
		p.readyDraft(draft, promotion.ID, "ACCEPTED")
		jobID := p.catalogJob(draft, "env:truncated-images", 1, &truncatedImages)
		if err := p.h.AdminDB.Model(&database.InventoryJob{}).Where("id=?", jobID).Update("candidate_image_truncated", 2).Error; err != nil {
			t.Fatal(err)
		}
		p.process(promotion.ID)
		if final := p.loadPromotion(promotion.ID); final.Status != "COMPLETED" || final.OriginVersionID == nil {
			t.Fatalf("promotion = %+v, want image limitation to remain promotable", final)
		}
		var job database.InventoryJob
		if err := p.h.AdminDB.Where("id=?", jobID).Take(&job).Error; err != nil || job.CandidateImageTruncated != 2 || job.ErrorCode == nil || *job.ErrorCode != "TRUNCATED_IMAGES" {
			t.Fatalf("image truncation diagnostic was not preserved: %+v err=%v", job, err)
		}
	})
	t.Run("item and image truncation combination blocks promotion", func(t *testing.T) {
		p := newPromotionFixture(t, "COMPLETED")
		promotion := p.request("truncated-items-and-images", p.status())
		draft := *promotion.DraftVersionID
		p.readyDraft(draft, promotion.ID, "ACCEPTED")
		jobID := p.catalogJob(draft, "env:combined", 1, &truncated)
		if err := p.h.AdminDB.Model(&database.InventoryJob{}).Where("id=?", jobID).Update("candidate_image_truncated", 2).Error; err != nil {
			t.Fatal(err)
		}
		p.process(promotion.ID)
		if final := p.loadPromotion(promotion.ID); final.Status != "FAILED" || final.FailureCode == nil || *final.FailureCode != "INCOMPLETE_ANALYSIS" {
			t.Fatalf("promotion = %+v, want combined truncation to block due to TRUNCATED_ITEMS", final)
		}
	})
	t.Run("a clean regeneration replaces the truncated first generation", func(t *testing.T) {
		p := newPromotionFixture(t, "COMPLETED")
		promotion := p.request("truncated-regenerated", p.status())
		draft := *promotion.DraftVersionID
		p.readyDraft(draft, promotion.ID, "ACCEPTED")
		p.catalogJob(draft, "env:truncated", 1, &truncated)
		p.catalogJob(draft, "env:truncated", 2, nil)
		p.process(promotion.ID)
		if final := p.loadPromotion(promotion.ID); final.Status != "COMPLETED" || final.OriginVersionID == nil {
			t.Fatalf("promotion = %+v, want the clean regeneration to promote", final)
		}
	})
	t.Run("a truncated regeneration blocks even after a clean first generation", func(t *testing.T) {
		p := newPromotionFixture(t, "COMPLETED")
		promotion := p.request("truncated-second", p.status())
		draft := *promotion.DraftVersionID
		p.readyDraft(draft, promotion.ID, "ACCEPTED")
		p.catalogJob(draft, "env:truncated", 1, nil)
		p.catalogJob(draft, "env:truncated", 2, &truncated)
		p.process(promotion.ID)
		if final := p.loadPromotion(promotion.ID); final.Status != "FAILED" {
			t.Fatalf("promotion = %+v", final)
		}
	})
}
