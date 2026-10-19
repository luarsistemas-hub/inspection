//go:build integration

package core_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	originactivate "inspection/services/inspection/internal/features/origins/activate_version"
	origincore "inspection/services/inspection/internal/features/origins/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestPermittedVideoOriginImpossibilityProducesNoUsableVersionIT033(t *testing.T) {
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
	originID, versionID, draftID := identity.NewID(), identity.NewID(), fixture.DraftID
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", fixture.TenantID, draftID).Updates(map[string]any{"kind": "ORIGIN", "media_kind": "VIDEO", "requirements": json.RawMessage(`[{"key":"video:main","impossibilityAllowed":true}]`), "status": "OPEN", "version": 1, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.OriginVersion{ID: versionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: fixture.ResponsibilityID, Status: "DRAFT", MediaKind: "VIDEO", IdempotencyKey: "it033-impossible", CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	capture := capturecore.Service{DB: h.DB, Within: h.WithinTenant, Now: func() time.Time { return now }}
	if err := capture.DeclareImpossibility(ctx, fixture.TenantID, fixture.ResponsibilityID, "video:main", "area is unsafe to enter", 0); err != nil {
		t.Fatalf("declare permitted impossibility: %v", err)
	}
	if _, err := capture.Submit(ctx, fixture.TenantID, fixture.ResponsibilityID, true); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("submission without an origin video error=%v, want INVALID_STATE", err)
	}
	if _, err := (origincore.Service{DB: h.DB, Within: h.WithinTenant, Now: func() time.Time { return now }}).ActivateCompleted(ctx, fixture.TenantID, fixture.ResponsibilityID); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("activation without a usable origin error=%v, want INVALID_STATE", err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var answer database.RequirementAnswer
		if err := tx.Where("tenant_id=? AND draft_id=? AND requirement_key=?", fixture.TenantID, draftID, "video:main").Take(&answer).Error; err != nil {
			return err
		}
		if answer.ImpossibilityReason != "area is unsafe to enter" || string(answer.MediaIDs) != "[]" {
			t.Errorf("impossibility answer=%+v", answer)
		}
		var version database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, versionID).Take(&version).Error; err != nil {
			return err
		}
		if version.Status != "DRAFT" || version.VideoPreparationID != nil {
			t.Errorf("impossible origin became usable: status=%s preparation=%v", version.Status, version.VideoPreparationID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestActivateSubmittedVideoOriginWithoutMediaReturnsInvalidStateIT035(t *testing.T) {
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
	responsibilityID, originID, versionID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.Responsibility{ID: responsibilityID, TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, ParticipantID: fixture.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: fixture.TenantID, ResponsibilityID: responsibilityID, Kind: "ORIGIN", MediaKind: "VIDEO", TemplateVersionID: fixture.TemplateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[]`), Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.OriginVersion{ID: versionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: responsibilityID, Status: "DRAFT", MediaKind: "VIDEO", IdempotencyKey: "it035-no-media", CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	service := origincore.Service{DB: h.DB, Within: h.WithinTenant, Now: func() time.Time { return now }}
	if err := originactivate.Setup(originactivate.Dependencies{DB: h.DB, Bus: bus, Service: service, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	principal := requestctx.Principal{IdentityID: fixture.IdentityID, MembershipID: fixture.MembershipID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}}
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.TenantID, Principal: principal, CorrelationID: "it035"})
	if _, err := bus.Send(ctx, originactivate.Command{TenantID: fixture.TenantID, VersionID: versionID}); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("activation error=%v, want INVALID_STATE", err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var version database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, versionID).Take(&version).Error; err != nil {
			return err
		}
		if version.Status != "DRAFT" || version.VideoPreparationID != nil {
			t.Errorf("origin without media changed: status=%s preparation=%v", version.Status, version.VideoPreparationID)
		}
		var origin database.Origin
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, originID).Take(&origin).Error; err != nil {
			return err
		}
		if origin.ActiveVersionID != nil {
			t.Errorf("origin without media became active: %v", *origin.ActiveVersionID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
