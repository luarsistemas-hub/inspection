//go:build integration

package close_invitation

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestCloseOriginRevokesStaleSessionAndPreventsSubmit(t *testing.T) {
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
	responsibilityID, draftID := identity.NewID(), identity.NewID()
	originID, versionID, invitationID := identity.NewID(), identity.NewID(), identity.NewID()
	sessionID := identity.NewID()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.Responsibility{ID: responsibilityID, TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, ParticipantID: fixture.ParticipantID, Status: "IN_PROGRESS", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.CaptureDraft{ID: draftID, TenantID: fixture.TenantID, ResponsibilityID: responsibilityID, Kind: "ORIGIN", MediaKind: "PHOTO", TemplateVersionID: fixture.TemplateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[]`), Status: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.OriginVersion{ID: versionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: responsibilityID, Status: "DRAFT", MediaKind: "PHOTO", IdempotencyKey: "close-it042-origin", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Invitation{ID: invitationID, TenantID: fixture.TenantID, ResponsibilityID: responsibilityID, TokenHash: make([]byte, 32), DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ExternalSession{ID: sessionID, TenantID: fixture.TenantID, InvitationID: invitationID, ResponsibilityID: responsibilityID, SessionDigest: make([]byte, 32), CSRFDigest: make([]byte, 32), ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := Setup(Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	principal := requestctx.Principal{IdentityID: fixture.IdentityID, MembershipID: fixture.MembershipID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}}
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.TenantID, Principal: principal, CorrelationID: "close-origin-it042"})
	if _, err := bus.Send(ctx, Command{TenantID: fixture.TenantID, ActorID: fixture.IdentityID, VersionID: versionID, Reason: "operator closed origin", ClientMutationID: "close-it042"}); err != nil {
		t.Fatal(err)
	}
	if _, err := (capturecore.Service{DB: h.DB}).Submit(ctx, fixture.TenantID, responsibilityID, false); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("stale submit error=%v, want INVALID_STATE", err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var version database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, versionID).Take(&version).Error; err != nil || version.Status != "INVALIDATED" {
			t.Errorf("origin version status=%q err=%v", version.Status, err)
		}
		var invitation database.Invitation
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, invitationID).Take(&invitation).Error; err != nil || invitation.Status != "REVOKED" || invitation.RevokedAt == nil {
			t.Errorf("invitation status=%q revokedAt=%v err=%v", invitation.Status, invitation.RevokedAt, err)
		}
		var session database.ExternalSession
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, sessionID).Take(&session).Error; err != nil || session.RevokedAt == nil {
			t.Errorf("session revokedAt=%v err=%v", session.RevokedAt, err)
		}
		var submissions int64
		if err := tx.Model(&database.SubmissionVersion{}).Where("tenant_id=? AND responsibility_id=?", fixture.TenantID, responsibilityID).Count(&submissions).Error; err != nil {
			return err
		}
		if submissions != 0 {
			t.Errorf("stale submit created %d submission versions", submissions)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
