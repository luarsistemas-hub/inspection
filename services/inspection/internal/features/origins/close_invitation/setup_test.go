package close_invitation

import (
	"context"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCloseDraftOriginRevokesAccessAndReplaysIdempotently(t *testing.T) {
	db := closeFixtureDB(t)
	tenantID, actorID, versionID, responsibilityID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	assetID, originID, businessUnitID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	version := database.OriginVersion{ID: versionID, TenantID: tenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: responsibilityID, Status: "DRAFT", MediaKind: "PHOTO", IdempotencyKey: "close-test", CreatedAt: now}
	rows := []any{
		&database.Asset{ID: assetID, TenantID: tenantID, BusinessUnitID: businessUnitID, SegmentVersionID: identity.NewID(), Name: "Asset", ExternalKey: "asset-1", Address: "Address", AddressStatus: "INCOMPLETE", PolicyOverrides: []byte(`{}`), Status: "ACTIVE", Version: 1, IdempotencyKey: "asset-close-1"},
		&database.Origin{ID: originID, TenantID: tenantID, AssetID: assetID, Version: 1, CreatedAt: now, UpdatedAt: now},
		&version,
		&database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "ORIGIN", MediaKind: "PHOTO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now},
		&database.Invitation{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, TokenHash: make([]byte, 32), DeliveryIntents: []byte(`[]`), Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now},
		&database.ExternalSession{ID: identity.NewID(), TenantID: tenantID, InvitationID: identity.NewID(), ResponsibilityID: responsibilityID, SessionDigest: []byte("session"), CSRFDigest: []byte("csrf"), ExpiresAt: now.Add(time.Hour), CreatedAt: now},
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	bus := mediator.New()
	if err := Setup(Dependencies{DB: db, Bus: bus, Authorizer: auth.Authorizer{}, Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) }, Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: actorID, TenantID: tenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "close-origin"})
	command := Command{TenantID: tenantID, ActorID: actorID, VersionID: versionID, Reason: "  replaced source  ", ClientMutationID: "close-1"}
	first, err := bus.Send(ctx, command)
	if err != nil || first.(database.OriginVersion).Status != "INVALIDATED" {
		t.Fatalf("close command result=%v err=%v", first, err)
	}
	second, err := bus.Send(ctx, command)
	if err != nil || second.(database.OriginVersion).Status != "INVALIDATED" {
		t.Fatalf("idempotent replay result=%v err=%v", second, err)
	}
	if _, err := (capturecore.Service{DB: db, Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) }}).Submit(ctx, tenantID, responsibilityID, false); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("submit through stale closed draft error=%v, want INVALID_STATE", err)
	}
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).First(&draft).Error; err != nil || draft.Status != "EXPIRED" {
		t.Fatalf("draft status=%q err=%v", draft.Status, err)
	}
	var invitation database.Invitation
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).First(&invitation).Error; err != nil || invitation.Status != "REVOKED" || invitation.RevokedAt == nil {
		t.Fatalf("invitation=%+v err=%v", invitation, err)
	}
	var session database.ExternalSession
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).First(&session).Error; err != nil || session.RevokedAt == nil {
		t.Fatalf("session=%+v err=%v", session, err)
	}
	var audits int64
	if err := db.Model(&database.AuditEvent{}).Where("tenant_id=? AND target_id=? AND action=?", tenantID, versionID.String(), "origin.invitation_closed").Count(&audits).Error; err != nil || audits != 1 {
		t.Fatalf("audit count=%d err=%v", audits, err)
	}
}

func TestCloseOriginRejectsInvalidStateInputAndViewer(t *testing.T) {
	db := closeFixtureDB(t)
	tenantID, actorID, versionID, responsibilityID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	assetID, originID, businessUnitID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	for _, row := range []any{
		&database.Asset{ID: assetID, TenantID: tenantID, BusinessUnitID: businessUnitID, SegmentVersionID: identity.NewID(), Name: "Asset", ExternalKey: "asset-1", Address: "Address", AddressStatus: "INCOMPLETE", PolicyOverrides: []byte(`{}`), Status: "ACTIVE", Version: 1, IdempotencyKey: "asset-close-2"},
		&database.Origin{ID: originID, TenantID: tenantID, AssetID: assetID, Version: 1, CreatedAt: now, UpdatedAt: now},
		&database.OriginVersion{ID: versionID, TenantID: tenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: responsibilityID, Status: "ACTIVE", IdempotencyKey: "active-origin", CreatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	bus := mediator.New()
	if err := Setup(Dependencies{DB: db, Bus: bus, Authorizer: auth.Authorizer{}, Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) }}); err != nil {
		t.Fatal(err)
	}
	command := Command{TenantID: tenantID, ActorID: actorID, VersionID: versionID, Reason: "close", ClientMutationID: "close-2"}
	ctx := func(role string) context.Context {
		return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: actorID, TenantID: tenantID, Roles: []string{role}}})
	}
	if _, err := bus.Send(ctx(auth.TenantAdmin), command); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("active version close error=%v, want INVALID_STATE", err)
	}
	command.VersionID = identity.NewID()
	if _, err := bus.Send(ctx(auth.TenantAdmin), command); !apperror.Is(err, apperror.NotFound) {
		t.Fatalf("foreign version close error=%v, want NOT_FOUND", err)
	}
	command.VersionID = versionID
	if _, err := bus.Send(ctx(auth.Viewer), command); !apperror.Is(err, apperror.Forbidden) {
		t.Fatalf("viewer close error=%v, want FORBIDDEN", err)
	}
	command.Reason = " "
	if _, err := bus.Send(ctx(auth.TenantAdmin), command); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("blank reason error=%v, want INVALID_INPUT", err)
	}
}

func closeFixtureDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:close-origin-"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"assets", "origins", "capture", "invitations", "audit"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.Asset{}, &database.Origin{}, &database.OriginVersion{}, &database.CaptureDraft{}, &database.Invitation{}, &database.ExternalSession{}, &database.AuditEvent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	return db
}
