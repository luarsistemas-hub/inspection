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

func TestComparativePromotionSupersedesOldVersionWithoutCopyingUnobservedItems(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f, err := h.SeedInventoryBaseline(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED", ComparisonMode: "COMPARE_ORIGIN_CURRENT"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	inventory := core.Service{DB: h.DB, Now: func() time.Time { return now }}
	bus := mediator.New()
	if err := Setup(Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Now: func() time.Time { return now }, Inventory: inventory}); err != nil {
		t.Fatal(err)
	}
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}}})
	statusRaw, err := bus.Ask(ctx, Query{TenantID: f.TenantID, InspectionID: f.InspectionID})
	if err != nil {
		t.Fatal(err)
	}
	status := statusRaw.(StatusView)
	requestRaw, err := bus.Send(ctx, Command{TenantID: f.TenantID, InspectionID: f.InspectionID, ClientMutationID: "comparative-new-origin", Purpose: "INVENTORY_UPDATE", ConfirmationDigest: status.ConfirmationDigest})
	if err != nil {
		t.Fatal(err)
	}
	promotion := requestRaw.(Result).Promotion
	if promotion.DraftVersionID == nil || *promotion.DraftVersionID == f.OriginVersionID {
		t.Fatalf("promotion did not create a separate version: %+v", promotion)
	}
	if err := h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND status IN ('PENDING','RUNNING')", f.TenantID, *promotion.DraftVersionID).Updates(map[string]any{"status": "CANCELED", "lease_token": nil, "lease_until": nil, "updated_at": now}).Error; err != nil {
		t.Fatal(err)
	}
	newItemID := identity.NewID()
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", f.TenantID, *promotion.DraftVersionID).Update("draft_state", "READY").Error; err != nil {
			return err
		}
		var env database.InventoryEnvironment
		if err := tx.Where("tenant_id=? AND origin_version_id=?", f.TenantID, *promotion.DraftVersionID).Take(&env).Error; err != nil {
			return err
		}
		item := database.InventoryItem{ID: newItemID, TenantID: f.TenantID, OriginVersionID: *promotion.DraftVersionID, EnvironmentID: env.ID, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Item observado somente na vistoria nova", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "ACCEPTED", Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&item).Error; err != nil {
			return err
		}
		return tx.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: f.TenantID, ItemID: item.ID, Kind: "PHOTO", MediaID: f.MediaID, Position: 0, CreatedAt: now}).Error
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
	var oldVersion, newVersion database.OriginVersion
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, f.OriginVersionID).Take(&oldVersion).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, *promotion.DraftVersionID).Take(&newVersion).Error; err != nil {
		t.Fatal(err)
	}
	var oldItems, newItems, oldItemRetained, oldItemCopied int64
	if err := h.AdminDB.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=?", f.TenantID, f.OriginVersionID).Count(&oldItems).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=?", f.TenantID, *promotion.DraftVersionID).Count(&newItems).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.InventoryItem{}).Where("tenant_id=? AND id=? AND origin_version_id=?", f.TenantID, f.ItemIDs[0], f.OriginVersionID).Count(&oldItemRetained).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.InventoryItem{}).Where("tenant_id=? AND id=? AND origin_version_id=?", f.TenantID, f.ItemIDs[0], *promotion.DraftVersionID).Count(&oldItemCopied).Error; err != nil {
		t.Fatal(err)
	}
	if final.Status != "COMPLETED" || origin.ActiveVersionID == nil || *origin.ActiveVersionID != *promotion.DraftVersionID || oldVersion.Status != "SUPERSEDED" || newVersion.Status != "ACTIVE" || oldItems != 1 || oldItemRetained != 1 || newItems != 1 || oldItemCopied != 0 {
		t.Fatalf("comparative promotion copied old items or damaged history: promotion=%+v origin=%+v old=%+v new=%+v counts old=%d retained=%d new=%d copied=%d newItem=%s", final, origin, oldVersion, newVersion, oldItems, oldItemRetained, newItems, oldItemCopied, newItemID)
	}
}
