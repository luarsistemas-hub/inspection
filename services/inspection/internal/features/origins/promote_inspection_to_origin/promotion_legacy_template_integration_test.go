//go:build integration

package promote_inspection_to_origin

import (
	"context"
	"os"
	"testing"
	"time"

	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestSchemaV1SubmittedCaptureCanPreparePromotionOrReturnsClearImpediment(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED", SchemaVersion: 1, RequirementKey: "room-1"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	bus := mediator.New()
	inventory := core.Service{DB: h.DB, Now: func() time.Time { return now }}
	if err := Setup(Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Now: func() time.Time { return now }, Inventory: inventory}); err != nil {
		t.Fatal(err)
	}
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}}})
	raw, err := bus.Ask(ctx, Query{TenantID: f.TenantID, InspectionID: f.InspectionID})
	if err != nil {
		t.Fatal(err)
	}
	status := raw.(StatusView)
	if status.Eligible {
		requested, err := bus.Send(ctx, Command{TenantID: f.TenantID, InspectionID: f.InspectionID, ClientMutationID: "schema-v1-capture", Purpose: "INVENTORY_UPDATE", ConfirmationDigest: status.ConfirmationDigest})
		if err != nil {
			t.Fatal(err)
		}
		promotion := requested.(Result).Promotion
		if promotion.Status != "PREPARING" || promotion.CaptureDraftID != f.DraftID || promotion.CatalogPromptSnapshotID == nil || promotion.CatalogGeneration == nil {
			t.Fatalf("schema v1 capture did not follow the catalog preparation path: %+v", promotion)
		}
		return
	}
	if len(status.Impediments) == 0 || status.Impediments[0].Code == "" || status.Impediments[0].Message == "" {
		t.Fatalf("schema v1 capture returned no clear impediment: %+v", status)
	}
	if _, err := bus.Send(ctx, Command{TenantID: f.TenantID, InspectionID: f.InspectionID, ClientMutationID: "schema-v1-blocked", ConfirmationDigest: status.ConfirmationDigest}); err == nil {
		t.Fatal("schema v1 capture with an impediment was accepted")
	}
	var promotions int64
	if err := h.AdminDB.Model(&database.InspectionOriginPromotion{}).Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Count(&promotions).Error; err != nil {
		t.Fatal(err)
	}
	if promotions != 0 {
		t.Fatalf("blocked schema v1 request created a partial operation: %d", promotions)
	}
}
