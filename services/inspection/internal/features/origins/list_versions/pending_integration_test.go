//go:build integration

package list_versions

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestSubmittedVideoOriginListsPendingAndUnselectable(t *testing.T) {
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
	originID, versionID, attemptID := identity.NewID(), identity.NewID(), identity.NewID()
	responsibilityID, draftID, mediaID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.Responsibility{ID: responsibilityID, TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, ParticipantID: fixture.ParticipantID, Status: "IN_PROGRESS", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.CaptureDraft{ID: draftID, TenantID: fixture.TenantID, ResponsibilityID: responsibilityID, Kind: "ORIGIN", MediaKind: "VIDEO", TemplateVersionID: fixture.TemplateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[]`), Status: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		attempt := database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: responsibilityID, Kind: "ORIGIN", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "FILE", Status: "RECEIVED", IdempotencyKey: "it032-video-origin", PayloadDigest: "it032-payload", AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: responsibilityID, ObjectKey: "tenant/" + fixture.TenantID.String() + "/media/" + mediaID.String(), ContentType: "video/mp4", SHA256: "it032-source", SizeBytes: 100, Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, IdempotencyKey: "it032-video-media", RequirementKey: "video:main", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: fixture.TenantID, ResponsibilityID: responsibilityID, DisclosureVersion: "video-v1", PhotoProcessing: true, VideoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.OriginVersion{ID: versionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: &responsibilityID, Status: "DRAFT", MediaKind: "VIDEO", IdempotencyKey: "it032-origin", CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := (capturecore.Service{DB: h.DB}).Submit(ctx, fixture.TenantID, responsibilityID, false); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := assetget.Setup(assetget.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	if err := Setup(Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	principal := requestctx.Principal{IdentityID: fixture.IdentityID, MembershipID: fixture.MembershipID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}}
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.TenantID, Principal: principal})
	raw, err := bus.Ask(ctx, Query{TenantID: fixture.TenantID, AssetID: fixture.AssetID})
	if err != nil {
		t.Fatal(err)
	}
	result := raw.(Result)
	if len(result.Versions) != 1 || result.Versions[0].ID != versionID {
		t.Fatalf("unexpected origin versions: %+v", result.Versions)
	}
	details := result.Details[versionID]
	if details.Selectable || details.PreparationStatus == nil || *details.PreparationStatus != "RECEIVED" {
		t.Fatalf("submitted but unprepared origin was selectable or missing readiness: %+v", details)
	}
	var job database.VideoPreparationJob
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Take(&job).Error
	}); err != nil {
		t.Fatal(err)
	}
	if job.Status != "QUEUED" || job.Phase != "RECEIVED" {
		t.Fatalf("worker should remain paused with queued preparation, got status=%s phase=%s payload=%s", job.Status, job.Phase, json.RawMessage(job.PendingObjectKeys))
	}
}
