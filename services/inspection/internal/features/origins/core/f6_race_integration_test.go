//go:build integration

package core_test

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	origincore "inspection/services/inspection/internal/features/origins/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestConcurrentAuthorizedOriginInvitationsKeepDistinctDraftsAndActiveVersionIT038(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	activeID, activeResponsibility := identity.NewID(), identity.NewID()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.Origin{ID: activeID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, ActiveVersionID: &activeResponsibility, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.OriginVersion{ID: activeResponsibility, TenantID: fixture.TenantID, OriginID: activeID, VersionNumber: 1, ResponsibilityID: identity.NewID(), Status: "ACTIVE", MediaKind: "PHOTO", IdempotencyKey: "it038-active", CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	service := origincore.Service{DB: h.DB, Within: h.WithinTenant}
	start := make(chan struct{})
	type result struct {
		invite origincore.Invitation
		err    error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, key := range []string{"it038-invite-a", "it038-invite-b"} {
		key := key
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			operatorCtx := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.TenantID, Principal: requestctx.Principal{IdentityID: fixture.IdentityID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: key})
			if _, err := (auth.Authorizer{}).Authorize(operatorCtx, fixture.TenantID, []string{auth.TenantAdmin}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: fixture.BusinessUnitID}, true); err != nil {
				results <- result{err: err}
				return
			}
			invite, err := service.Invite(operatorCtx, origincore.InviteInput{
				TenantID: fixture.TenantID, AssetID: fixture.AssetID, TemplateVersionID: fixture.TemplateVersionID,
				Delivery:     []invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "qa@example.test"}},
				Requirements: []capturecore.Requirement{{Key: "room", Required: true, MinimumMedia: 1, MaximumMedia: 1}},
				Policy:       json.RawMessage(`{}`), ExpiresAt: now.Add(time.Hour), IdempotencyKey: key,
			})
			results <- result{invite: invite, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var invites []origincore.Invitation
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent Invite: %v", result.err)
		}
		invites = append(invites, result.invite)
	}
	if len(invites) != 2 || invites[0].VersionID == invites[1].VersionID || invites[0].ResponsibilityID == invites[1].ResponsibilityID {
		t.Fatalf("concurrent invitations did not produce distinct drafts: %+v", invites)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var origin database.Origin
		if err := tx.Where("tenant_id=? AND asset_id=?", fixture.TenantID, fixture.AssetID).Take(&origin).Error; err != nil {
			return err
		}
		if origin.ID != activeID || origin.ActiveVersionID == nil || *origin.ActiveVersionID != activeResponsibility {
			t.Errorf("racing invitations changed active origin: %+v", origin)
		}
		var versions []database.OriginVersion
		if err := tx.Where("tenant_id=? AND origin_id=?", fixture.TenantID, activeID).Order("version_number").Find(&versions).Error; err != nil {
			return err
		}
		if len(versions) != 3 || versions[0].Status != "ACTIVE" || versions[1].Status != "DRAFT" || versions[2].Status != "DRAFT" || versions[1].VersionNumber == versions[2].VersionNumber {
			t.Errorf("origin version history after race: %+v", versions)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
