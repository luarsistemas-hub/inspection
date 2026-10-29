//go:build integration

package promote_inspection_to_origin

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestConcurrentRequestsForDifferentInspectionsOnOneAssetHaveSingleWinner(t *testing.T) {
	p := newPromotionFixtureWithOrigin(t, "COMPLETED")
	secondInspection := cloneInspectionForSameAsset(t, p)
	firstView, err := p.bus.Ask(p.ctx, Query{TenantID: p.f.TenantID, InspectionID: p.f.InspectionID})
	if err != nil {
		t.Fatal(err)
	}
	secondView, err := p.bus.Ask(p.ctx, Query{TenantID: p.f.TenantID, InspectionID: secondInspection})
	if err != nil {
		t.Fatal(err)
	}
	commands := []Command{
		{TenantID: p.f.TenantID, InspectionID: p.f.InspectionID, ClientMutationID: "concurrent-a", Purpose: "INVENTORY_UPDATE", ConfirmationDigest: firstView.(StatusView).ConfirmationDigest},
		{TenantID: p.f.TenantID, InspectionID: secondInspection, ClientMutationID: "concurrent-b", Purpose: "INVENTORY_UPDATE", ConfirmationDigest: secondView.(StatusView).ConfirmationDigest},
	}
	type result struct {
		promotion database.InspectionOriginPromotion
		err       error
	}
	results := make([]result, 2)
	var wg sync.WaitGroup
	for i := range commands {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			raw, sendErr := p.bus.Send(p.ctx, commands[index])
			results[index].err = sendErr
			if sendErr == nil {
				results[index].promotion = raw.(Result).Promotion
			}
		}(i)
	}
	wg.Wait()
	winners, conflicts := 0, 0
	for _, result := range results {
		if result.err == nil {
			if result.promotion.Status != "PREPARING" {
				t.Fatalf("winning request status=%s; want PREPARING", result.promotion.Status)
			}
			winners++
			continue
		}
		code, _, message := apperror.Public(result.err)
		if code != apperror.Conflict || !strings.Contains(message, "promoção") {
			t.Fatalf("losing request should receive a pt-BR conflict, got %v", result.err)
		}
		conflicts++
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("concurrent request outcomes winners=%d conflicts=%d; want one each: %+v", winners, conflicts, results)
	}
	origin := p.loadOrigin()
	if origin.ActiveVersionID == nil || *origin.ActiveVersionID != p.activeID {
		t.Fatalf("request race changed the active origin before processing: %+v", origin)
	}
	var preparing, drafts int64
	if err := p.h.AdminDB.Model(&database.InspectionOriginPromotion{}).Where("tenant_id=? AND asset_id=? AND status='PREPARING'", p.f.TenantID, p.f.AssetID).Count(&preparing).Error; err != nil {
		t.Fatal(err)
	}
	if err := p.h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND origin_id=? AND status='DRAFT'", p.f.TenantID, origin.ID).Count(&drafts).Error; err != nil {
		t.Fatal(err)
	}
	if preparing != 1 || drafts != 1 {
		t.Fatalf("request race corrupted state: preparing=%d drafts=%d", preparing, drafts)
	}
}

func cloneInspectionForSameAsset(t *testing.T, p *promotionFixture) identity.ID {
	t.Helper()
	newInspectionID, responsibilityID, captureID, mediaID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	err := p.h.WithinTenant(p.ctx, p.f.TenantID, func(tx *gorm.DB) error {
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", p.f.TenantID, p.f.InspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		inspection.ID = newInspectionID
		inspection.SourceKey = "it-second-inspection-" + newInspectionID.String()
		inspection.CreatedAt = p.now.Add(time.Second)
		inspection.UpdatedAt = p.now.Add(time.Second)
		if err := tx.Create(&inspection).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Responsibility{ID: responsibilityID, TenantID: p.f.TenantID, InspectionID: newInspectionID, ParticipantID: p.f.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: p.now, UpdatedAt: p.now}).Error; err != nil {
			return err
		}
		var capture database.CaptureDraft
		if err := tx.Where("tenant_id=? AND id=?", p.f.TenantID, p.f.DraftID).Take(&capture).Error; err != nil {
			return err
		}
		capture.ID = captureID
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
		media.ID = mediaID
		media.ResponsibilityID = responsibilityID
		media.IdempotencyKey = "it-second-media-" + mediaID.String()
		media.ObjectKey = "tenant/" + p.f.TenantID.String() + "/media/" + mediaID.String()
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
			answer.DraftID = captureID
			var mediaIDs []identity.ID
			if err := json.Unmarshal(answer.MediaIDs, &mediaIDs); err != nil {
				return err
			}
			for i := range mediaIDs {
				if mediaIDs[i] == p.f.MediaID {
					mediaIDs[i] = mediaID
				}
			}
			encodedMediaIDs, err := json.Marshal(mediaIDs)
			if err != nil {
				return err
			}
			answer.MediaIDs = encodedMediaIDs
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
	return newInspectionID
}
