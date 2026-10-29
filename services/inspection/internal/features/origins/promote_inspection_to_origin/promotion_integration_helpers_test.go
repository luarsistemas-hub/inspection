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

type promotionFixture struct {
	test      *testing.T
	h         *harness.Harness
	f         harness.Task06Fixture
	ctx       context.Context
	bus       *mediator.Bus
	now       time.Time
	inventory core.Service
	activeID  identity.ID
}

func newPromotionFixture(t *testing.T, status string) *promotionFixture {
	return newPromotionFixtureBase(t, status, false)
}

func newPromotionFixtureWithOrigin(t *testing.T, status string) *promotionFixture {
	return newPromotionFixtureBase(t, status, true)
}

func newPromotionFixtureBase(t *testing.T, status string, withOrigin bool) *promotionFixture {
	t.Helper()
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	var f harness.Task06Fixture
	var activeID identity.ID
	if withOrigin {
		baseline, seedErr := h.SeedInventoryBaseline(ctx, harness.Task06FixtureOptions{InspectionStatus: status})
		if seedErr != nil {
			t.Fatal(seedErr)
		}
		f = baseline.Task06Fixture
		activeID = baseline.OriginVersionID
	} else {
		f, err = h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: status})
		if err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	inventory := core.Service{DB: h.DB, Now: func() time.Time { return now }}
	bus := mediator.New()
	if err := Setup(Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Now: func() time.Time { return now }, Inventory: inventory}); err != nil {
		t.Fatal(err)
	}
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}}})
	return &promotionFixture{test: t, h: h, f: f, ctx: ctx, bus: bus, now: now, inventory: inventory, activeID: activeID}
}

func (p *promotionFixture) status() StatusView {
	p.t().Helper()
	raw, err := p.bus.Ask(p.ctx, Query{TenantID: p.f.TenantID, InspectionID: p.f.InspectionID})
	if err != nil {
		p.t().Fatal(err)
	}
	return raw.(StatusView)
}

func (p *promotionFixture) request(mutationID string, status StatusView) database.InspectionOriginPromotion {
	p.t().Helper()
	raw, err := p.bus.Send(p.ctx, Command{TenantID: p.f.TenantID, InspectionID: p.f.InspectionID, ClientMutationID: mutationID, Purpose: "INVENTORY_UPDATE", ConfirmationDigest: status.ConfirmationDigest})
	if err != nil {
		p.t().Fatal(err)
	}
	promotion := raw.(Result).Promotion
	if promotion.DraftVersionID != nil {
		if err := p.h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND status IN ('PENDING','RUNNING')", p.f.TenantID, *promotion.DraftVersionID).Updates(map[string]any{"status": "CANCELED", "lease_token": nil, "lease_until": nil, "updated_at": p.now}).Error; err != nil {
			p.t().Fatal(err)
		}
	}
	return promotion
}

func (p *promotionFixture) t() *testing.T {
	// The test is attached during construction to keep helpers concise.
	return p.test
}

func (p *promotionFixture) readyDraft(draftID, promotionID identity.ID, decisions ...string) []identity.ID {
	p.t().Helper()
	items := make([]identity.ID, 0, len(decisions))
	err := p.h.WithinTenant(p.ctx, p.f.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", p.f.TenantID, draftID).Update("draft_state", "READY").Error; err != nil {
			return err
		}
		var env database.InventoryEnvironment
		if err := tx.Where("tenant_id=? AND origin_version_id=?", p.f.TenantID, draftID).Take(&env).Error; err != nil {
			if err := tx.Create(&database.InventoryEnvironment{ID: identity.NewID(), TenantID: p.f.TenantID, OriginVersionID: draftID, IdentityID: p.f.EnvironmentIdentityID, TypeCode: "LIVING_ROOM", Name: "Sala", Expected: true, Decision: "PENDING", Position: 0, Version: 1, CreatedAt: p.now, UpdatedAt: p.now}).Error; err != nil {
				return err
			}
			if err := tx.Where("tenant_id=? AND origin_version_id=?", p.f.TenantID, draftID).Take(&env).Error; err != nil {
				return err
			}
		}
		for _, decision := range decisions {
			withEvidence := decision != "PENDING_NO_EVIDENCE"
			if !withEvidence {
				decision = "PENDING"
			}
			item := database.InventoryItem{ID: identity.NewID(), TenantID: p.f.TenantID, OriginVersionID: draftID, EnvironmentID: env.ID, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Item " + decision, Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: decision, Version: 1, CreatedAt: p.now, UpdatedAt: p.now}
			if decision == "REJECTED" {
				reason := "NOT_EXISTS"
				item.RejectionReason = &reason
			}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
			items = append(items, item.ID)
			if decision != "REJECTED" && withEvidence {
				if err := tx.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: p.f.TenantID, ItemID: item.ID, Kind: "PHOTO", MediaID: p.f.MediaID, Position: 0, CreatedAt: p.now}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		p.t().Fatal(err)
	}
	return items
}

func (p *promotionFixture) process(promotionID identity.ID) {
	p.t().Helper()
	if err := Process(p.ctx, p.h.DB, ProcessDependencies{Inventory: p.inventory, Now: func() time.Time { return p.now }}, p.f.TenantID, promotionID); err != nil {
		p.t().Fatal(err)
	}
}

func (p *promotionFixture) loadPromotion(id identity.ID) database.InspectionOriginPromotion {
	p.t().Helper()
	var promotion database.InspectionOriginPromotion
	if err := p.h.AdminDB.Where("tenant_id=? AND id=?", p.f.TenantID, id).Take(&promotion).Error; err != nil {
		p.t().Fatal(err)
	}
	return promotion
}

func (p *promotionFixture) loadOrigin() database.Origin {
	p.t().Helper()
	var origin database.Origin
	if err := p.h.AdminDB.Where("tenant_id=? AND asset_id=?", p.f.TenantID, p.f.AssetID).Take(&origin).Error; err != nil {
		p.t().Fatal(err)
	}
	return origin
}
