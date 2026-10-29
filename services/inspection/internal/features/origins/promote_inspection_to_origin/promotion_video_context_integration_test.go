//go:build integration

package promote_inspection_to_origin

import (
	"context"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestProcessRequiresConfirmationWhenActiveSealedVideoPlanChanges(t *testing.T) {
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
	firstPlanID := identity.NewID()
	var submissionID identity.ID
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", f.TenantID, f.InspectionID).Update("status", "COMPLETED").Error; err != nil {
			return err
		}
		var submission database.SubmissionVersion
		if err := tx.Where("tenant_id=? AND draft_id=?", f.TenantID, f.DraftID).Order("version_number DESC").Take(&submission).Error; err != nil {
			submission = database.SubmissionVersion{ID: identity.NewID(), TenantID: f.TenantID, DraftID: f.DraftID, ResponsibilityID: f.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: []byte(`{}`), PayloadDigest: "video-promotion-test", SubmittedAt: now}
			if createErr := tx.Create(&submission).Error; createErr != nil {
				return createErr
			}
		}
		submissionID = submission.ID
		plan := database.VideoPlan{ID: firstPlanID, TenantID: f.TenantID, InspectionID: f.InspectionID, SubmissionID: submission.ID, CurrentPreparationID: f.PreparationID, Generation: 1, PlanDigest: "plan-digest-1", Status: "SEALED", NextAttemptAt: now, SealedAt: &now, CreatedAt: now}
		if err := tx.Create(&plan).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlanPreparation{TenantID: f.TenantID, PlanID: firstPlanID, PreparationID: f.PreparationID, Role: "MAIN", RequirementKey: ptrString("env:new:" + f.EnvironmentIdentityID.String()), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", f.TenantID, f.InspectionID).Update("active_video_plan_id", firstPlanID).Error
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
	view := statusRaw.(StatusView)
	if !view.Eligible {
		t.Fatalf("video fixture should be eligible: %+v", view.Impediments)
	}
	requested, err := bus.Send(ctx, Command{TenantID: f.TenantID, InspectionID: f.InspectionID, ClientMutationID: "video-context", Purpose: "INVENTORY_UPDATE", ConfirmationDigest: view.ConfirmationDigest})
	if err != nil {
		t.Fatal(err)
	}
	promotion := requested.(Result).Promotion
	if promotion.Status != "PREPARING" || promotion.VideoPlanID == nil || *promotion.VideoPlanID != firstPlanID || promotion.AnalysisGeneration == nil || *promotion.AnalysisGeneration != 1 {
		t.Fatalf("promotion did not pin the sealed plan: %+v", promotion)
	}
	if err := h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND status IN ('PENDING','RUNNING')", f.TenantID, *promotion.DraftVersionID).Updates(map[string]any{"status": "CANCELED", "lease_token": nil, "lease_until": nil, "updated_at": now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", f.TenantID, *promotion.DraftVersionID).Update("draft_state", "READY").Error; err != nil {
			return err
		}
		var environment database.InventoryEnvironment
		if err := tx.Where("tenant_id=? AND origin_version_id=?", f.TenantID, *promotion.DraftVersionID).Take(&environment).Error; err != nil {
			environment = database.InventoryEnvironment{ID: identity.NewID(), TenantID: f.TenantID, OriginVersionID: *promotion.DraftVersionID, IdentityID: f.EnvironmentIdentityID, TypeCode: "LIVING_ROOM", Name: "Sala", Expected: true, Decision: "PENDING", Position: 0, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&environment).Error; err != nil {
				return err
			}
		}
		return tx.Create(&database.InventoryItem{ID: identity.NewID(), TenantID: f.TenantID, OriginVersionID: *promotion.DraftVersionID, EnvironmentID: environment.ID, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Item de vídeo", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "ACCEPTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	secondPlanID := identity.NewID()
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		plan := database.VideoPlan{ID: secondPlanID, TenantID: f.TenantID, InspectionID: f.InspectionID, SubmissionID: submissionID, CurrentPreparationID: f.PreparationID, Generation: 2, PlanDigest: "plan-digest-2", Status: "SEALED", NextAttemptAt: now, SealedAt: &now, CreatedAt: now.Add(time.Second)}
		if err := tx.Create(&plan).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlanPreparation{TenantID: f.TenantID, PlanID: secondPlanID, PreparationID: f.PreparationID, Role: "MAIN", RequirementKey: ptrString("env:new:" + f.EnvironmentIdentityID.String()), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", f.TenantID, f.InspectionID).Update("active_video_plan_id", secondPlanID).Error
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
	var draft database.OriginVersion
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, *promotion.DraftVersionID).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	var autoAccepted int64
	if err := h.AdminDB.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=? AND auto_accepted_by_promotion_id IS NOT NULL", f.TenantID, *promotion.DraftVersionID).Count(&autoAccepted).Error; err != nil {
		t.Fatal(err)
	}
	if final.Status != "NEEDS_CONFIRMATION" || final.FailureCode == nil || *final.FailureCode != "CONTEXT_CHANGED" || origin.ActiveVersionID != nil || draft.Status != "DRAFT" || autoAccepted != 0 {
		t.Fatalf("changed video plan must not activate or accept items: promotion=%+v origin=%+v draft=%+v autoAccepted=%d", final, origin, draft, autoAccepted)
	}
}

func ptrString(value string) *string { return &value }
