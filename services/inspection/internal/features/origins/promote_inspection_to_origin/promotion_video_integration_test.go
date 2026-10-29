//go:build integration

package promote_inspection_to_origin

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	inventorytest "inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	processinventory "inspection/services/inspection/internal/features/analysis/process_inventory_job"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestPromotionActivatesSealedPlanWithMainAndSupplementAndRetainsExpiredSourceFrames(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f, err := h.SeedVideoEnvironmentFixture(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	secondMediaID, secondPreparationID, secondFrameID := identity.NewID(), identity.NewID(), identity.NewID()
	planID := identity.NewID()
	secondFrameObjectKey := "tenant/" + f.TenantID.String() + "/video-frames/" + secondFrameID.String() + ".jpg"
	secondFrameBytes := []byte("representative frame from supplement clip")
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, secondFrameObjectKey, bytes.NewReader(secondFrameBytes), int64(len(secondFrameBytes)), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	var submissionID identity.ID
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", f.TenantID, f.InspectionID).Update("status", "COMPLETED").Error; err != nil {
			return err
		}
		var submission database.SubmissionVersion
		if err := tx.Where("tenant_id=? AND draft_id=?", f.TenantID, f.DraftID).Order("version_number DESC").Take(&submission).Error; err != nil {
			submission = database.SubmissionVersion{ID: identity.NewID(), TenantID: f.TenantID, DraftID: f.DraftID, ResponsibilityID: f.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: []byte(`{}`), PayloadDigest: "video-multi-clip-promotion", SubmittedAt: now}
			if createErr := tx.Create(&submission).Error; createErr != nil {
				return createErr
			}
		}
		submissionID = submission.ID
		var mainMedia database.MediaObject
		if err := tx.Where("tenant_id=? AND id=?", f.TenantID, f.MediaID).Take(&mainMedia).Error; err != nil {
			return err
		}
		var mainPreparation database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", f.TenantID, f.PreparationID).Take(&mainPreparation).Error; err != nil {
			return err
		}
		var mainSource database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=?", f.TenantID, f.MediaID).Take(&mainSource).Error; err != nil {
			return err
		}
		mainSource.Status = "EXPIRED"
		if err := tx.Save(&mainSource).Error; err != nil {
			return err
		}
		var mainFrame database.VideoFrame
		if err := tx.Where("tenant_id=? AND id=?", f.TenantID, f.FrameID).Take(&mainFrame).Error; err != nil {
			return err
		}
		mainMedia.ID = secondMediaID
		mainMedia.RequirementKey = "env:new:" + f.EnvironmentIdentityID.String()
		mainMedia.IdempotencyKey = "supplement-media-" + secondMediaID.String()
		mainMedia.ObjectKey = "tenant/" + f.TenantID.String() + "/media/" + secondMediaID.String()
		if err := tx.Create(&mainMedia).Error; err != nil {
			return err
		}
		mainPreparation.ID = secondPreparationID
		mainPreparation.MediaID = secondMediaID
		mainPreparation.RequirementKey = &mainMedia.RequirementKey
		mainPreparation.CreatedAt = now
		if err := tx.Create(&mainPreparation).Error; err != nil {
			return err
		}
		secondSource := mainSource
		secondSource.ID = identity.NewID()
		secondSource.MediaID = secondMediaID
		secondSource.Status = "RETAINED"
		secondSource.OriginalDeletedAt = nil
		if err := tx.Create(&secondSource).Error; err != nil {
			return err
		}
		mainFrame.ID = secondFrameID
		mainFrame.SourceMediaID = secondMediaID
		mainFrame.PreparationID = secondPreparationID
		mainFrame.ObjectKey = secondFrameObjectKey
		if err := tx.Create(&mainFrame).Error; err != nil {
			return err
		}
		plan := database.VideoPlan{ID: planID, TenantID: f.TenantID, InspectionID: f.InspectionID, SubmissionID: submissionID, CurrentPreparationID: f.PreparationID, Generation: 1, PlanDigest: strings.Repeat("a", 64), Status: "SEALED", NextAttemptAt: now, SealedAt: &now, CreatedAt: now}
		if err := tx.Create(&plan).Error; err != nil {
			return err
		}
		for _, prep := range []database.VideoPlanPreparation{
			{TenantID: f.TenantID, PlanID: planID, PreparationID: f.PreparationID, Role: "MAIN", RequirementKey: nil, CreatedAt: now},
			{TenantID: f.TenantID, PlanID: planID, PreparationID: secondPreparationID, Role: "SUPPLEMENT", RequirementKey: &mainMedia.RequirementKey, CreatedAt: now},
		} {
			if err := tx.Create(&prep).Error; err != nil {
				return err
			}
		}
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", f.TenantID, f.InspectionID).Update("active_video_plan_id", planID).Error
	}); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	inventory := core.Service{DB: h.DB, Now: func() time.Time { return now }}
	if err := Setup(Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Now: func() time.Time { return now }, Inventory: inventory}); err != nil {
		t.Fatal(err)
	}
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}}})
	statusRaw, err := bus.Ask(ctx, Query{TenantID: f.TenantID, InspectionID: f.InspectionID})
	if err != nil {
		t.Fatal(err)
	}
	status := statusRaw.(StatusView)
	if !status.Eligible {
		t.Fatalf("sealed two-clip inspection should be eligible: %+v", status.Impediments)
	}
	requested, err := bus.Send(ctx, Command{TenantID: f.TenantID, InspectionID: f.InspectionID, ClientMutationID: "multi-clip-promotion", Purpose: "INVENTORY_UPDATE", ConfirmationDigest: status.ConfirmationDigest})
	if err != nil {
		t.Fatal(err)
	}
	promotion := requested.(Result).Promotion
	if promotion.DraftVersionID == nil || promotion.VideoPlanID == nil || *promotion.VideoPlanID != planID {
		t.Fatalf("promotion did not prepare a draft against the sealed plan: %+v", promotion)
	}
	var catalogJob database.InventoryJob
	if err := h.AdminDB.Where("tenant_id=? AND origin_version_id=? AND kind='CATALOG'", f.TenantID, *promotion.DraftVersionID).Take(&catalogJob).Error; err != nil {
		t.Fatal(err)
	}
	var catalogFrames []string
	gateway := &inventorytest.Gateway{Respond: func(request llm.StructuredRequest) (string, error) {
		for _, image := range request.Images {
			catalogFrames = append(catalogFrames, image.EvidenceID)
		}
		return fmt.Sprintf(`{"environments":[{"typeCode":"LIVING_ROOM","name":"Sala","items":[{"typeCode":"OTHER","name":"Item dos dois clipes","condition":"GOOD","observation":"OBSERVED","confidenceBps":9000,"evidence":[{"mediaId":%q,"frameId":%q},{"mediaId":%q,"frameId":%q}]}]}]}`,
			f.MediaID.String(), f.FrameID.String(), secondMediaID.String(), secondFrameID.String()), nil
	}}
	processor, err := processinventory.Setup(processinventory.Dependencies{DB: h.DB, Store: h.Store, Gateway: gateway, Now: func() time.Time { return now }, Within: h.WithinTenant})
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(ctx, catalogJob, func(write func(*gorm.DB) error) error { return h.WithinTenant(ctx, f.TenantID, write) }); err != nil {
		t.Fatal(err)
	}
	if len(catalogFrames) != 2 || catalogFrames[0] == catalogFrames[1] || !containsVideoFrame(catalogFrames, f.FrameID.String()) || !containsVideoFrame(catalogFrames, secondFrameID.String()) {
		t.Fatalf("CATALOG did not receive both sealed-plan frames: %v", catalogFrames)
	}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.InventoryJob{}).Where("tenant_id=? AND id=?", f.TenantID, catalogJob.ID).Update("status", "SUCCEEDED").Error; err != nil {
			return err
		}
		return processor.Succeeded(ctx, tx, catalogJob)
	}); err != nil {
		t.Fatal(err)
	}
	if err := Process(ctx, h.DB, ProcessDependencies{Inventory: inventory, Now: func() time.Time { return now }}, f.TenantID, promotion.ID); err != nil {
		t.Fatal(err)
	}
	var final database.InspectionOriginPromotion
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, promotion.ID).Take(&final).Error; err != nil {
		t.Fatal(err)
	}
	var origin database.Origin
	if err := h.AdminDB.Where("tenant_id=? AND asset_id=?", f.TenantID, f.AssetID).Take(&origin).Error; err != nil {
		t.Fatal(err)
	}
	var item database.InventoryItem
	if err := h.AdminDB.Where("tenant_id=? AND origin_version_id=?", f.TenantID, *promotion.DraftVersionID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	if final.Status != "COMPLETED" || origin.ActiveVersionID == nil || final.OriginVersionID == nil || *origin.ActiveVersionID != *final.OriginVersionID || item.Decision != "ACCEPTED" || item.AutoAcceptedByPromotionID == nil || *item.AutoAcceptedByPromotionID != promotion.ID {
		t.Fatalf("two-clip promotion did not complete with automatic acceptance: promotion=%+v origin=%+v item=%+v", final, origin, item)
	}
	for _, preparationID := range []identity.ID{f.PreparationID, secondPreparationID} {
		var references int64
		if err := h.AdminDB.Model(&database.VideoEvidenceReference{}).Where("tenant_id=? AND preparation_id=? AND owner_type='ORIGIN_VERSION' AND owner_id=?", f.TenantID, preparationID, *final.OriginVersionID).Count(&references).Error; err != nil {
			t.Fatal(err)
		}
		if references != 1 {
			t.Fatalf("preparation %s has %d retained origin references; want 1", preparationID, references)
		}
	}
}

func containsVideoFrame(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
