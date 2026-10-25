//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	inventorydraft "inspection/services/inspection/internal/features/inventory/draft"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestIT038ReviewHandoffAfterSubmitAndDashboardPermissions(t *testing.T) {
	fixture := newAPI03FailureFixture(t, "acceptProcessing")
	now := time.Now().UTC()
	reviewerID, reviewerIdentity := identity.NewID(), identity.NewID()
	originID, versionID := identity.NewID(), identity.NewID()
	var assetID identity.ID
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		var asset database.Asset
		if err := tx.Where("tenant_id=?", fixture.tenantID).Take(&asset).Error; err != nil {
			return err
		}
		assetID = asset.ID
		if err := tx.Create(&database.Membership{ID: reviewerID, TenantID: fixture.tenantID, IdentityID: reviewerIdentity, Issuer: "it038", Subject: reviewerID.String(), Role: auth.Employee, Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ResourceScope{ID: identity.NewID(), TenantID: fixture.tenantID, MembershipID: reviewerID, Kind: "ASSET", ResourceID: assetID, CreatedAt: now}).Error; err != nil {
			return err
		}
		requirements, _ := json.Marshal([]capturecore.Requirement{{Key: "env:new:" + identity.NewID().String(), Section: "LIVING_ROOM", Label: "Sala", EvidenceKind: "VIDEO", Required: true, ImpossibilityAllowed: true, MinimumMedia: 1, MaximumMedia: 3, CaptureSourcePolicy: "CAMERA_DEFAULT", ComparisonTarget: "CURRENT_ONLY", EnvironmentTypeCode: "LIVING_ROOM", EnvironmentName: "Sala", AddedDuringCapture: true}})
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Update("requirements", requirements).Error; err != nil {
			return err
		}
		return tx.Model(&database.Invitation{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.invitationID).Update("reviewer_membership_id", reviewerID).Error
	}); err != nil {
		t.Fatal(err)
	}

	// The forwarded link works on its own: the external session captures
	// without any internal account, and there is no handoff before submission.
	open := executeCaptureGraphQL(t, fixture.server, `query { externalCapture { status } }`, fixture.tenantID, fixture.credentials, true)
	handoff := executeCaptureGraphQL(t, fixture.server, `query { externalCapture { reviewHandoff { inspectionId } } }`, fixture.tenantID, fixture.credentials, true)
	if strings.Contains(open, `"errors"`) || !strings.Contains(open, `"status":"OPEN"`) || !strings.Contains(handoff, `"reviewHandoff":null`) {
		t.Fatalf("forwarded capture link before submit: %s", open)
	}

	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Updates(map[string]any{"status": "SUBMITTED", "version": 2}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.tenantID, AssetID: assetID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		ready := "READY"
		return tx.Create(&database.OriginVersion{ID: versionID, TenantID: fixture.tenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: identity.Ptr(fixture.responsibilityID), Source: "CAPTURE", HasInventory: true, DraftState: &ready, Status: "DRAFT", MediaKind: "PHOTO", IdempotencyKey: "it038-version", CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	submitted := executeCaptureGraphQL(t, fixture.server, `query { externalCapture { reviewHandoff { inspectionId originVersionId } } }`, fixture.tenantID, fixture.credentials, true)
	if strings.Contains(submitted, `"errors"`) || !strings.Contains(submitted, `"reviewHandoff":{"inspectionId":"`) || !strings.Contains(submitted, `"originVersionId":"`+versionID.String()+`"`) {
		t.Fatalf("submitted capture must hand off to the draft review: %s", submitted)
	}

	if err := inventorydraft.Setup(fixture.bus, inventorycore.Service{DB: fixture.harness.DB, Within: fixture.harness.WithinTenant}); err != nil {
		t.Fatal(err)
	}
	resolver := &Resolver{DB: fixture.harness.DB, Bus: fixture.bus, Authorizer: auth.Authorizer{}}
	principal := func(membership, identityID identity.ID, role string) context.Context {
		return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fixture.tenantID, Principal: requestctx.Principal{Issuer: "it038", Subject: membership.String(), IdentityID: identityID, MembershipID: membership, TenantID: fixture.tenantID, Roles: []string{role}, Scopes: []requestctx.Scope{{Kind: "ASSET", ID: assetID}}, ProductEntitlements: auth.ProductsForRole(role)}})
	}
	draft, err := resolver.Query().InventoryDraft(principal(reviewerID, reviewerIdentity, auth.Employee), versionID.String())
	if err != nil || draft == nil {
		t.Fatalf("invited reviewer must open the draft: %v", err)
	}
	viewerID, viewerIdentity := identity.NewID(), identity.NewID()
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.Membership{ID: viewerID, TenantID: fixture.tenantID, IdentityID: viewerIdentity, Issuer: "it038", Subject: viewerID.String(), Role: "CUSTOMER_VIEWER", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ResourceScope{ID: identity.NewID(), TenantID: fixture.tenantID, MembershipID: viewerID, Kind: "ASSET", ResourceID: assetID, CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Query().InventoryDraft(principal(viewerID, viewerIdentity, "CUSTOMER_VIEWER"), versionID.String()); !apperror.Is(err, apperror.Forbidden) {
		t.Fatalf("membership without a review role must be forbidden, got %v", err)
	}
}
