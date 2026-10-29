//go:build integration

package promote_inspection_video

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

// TestPromotionOfAnchoredSourceKeepsAnchorAndFloor is the ADR-019 regression for
// promotion: a source already anchored by an active baseline (ACTIVE_BASELINE,
// no source_expiry_at) is a valid retained evidence anchor, and promotion keeps
// the anchor and its persisted floor instead of inventing a fixed date.
func TestPromotionOfAnchoredSourceKeepsAnchorAndFloor(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Add(-2 * 24 * time.Hour).Truncate(time.Microsecond)
	promotedAt := t0.Add(2 * 24 * time.Hour)
	floor := t0.Add(400 * 24 * time.Hour)
	attemptID, mediaID, submissionID := identity.NewID(), identity.NewID(), identity.NewID()
	preparationID, frameID, planID := identity.NewID(), identity.NewID(), identity.NewID()
	digest := strings.Repeat("a", 64)
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Update("media_kind", "VIDEO").Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "anchored-promotion-" + attemptID.String(), PayloadDigest: digest, AdmittedAt: t0, ExpiresAt: promotedAt, CreatedAt: t0}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "anchored-promotion/" + mediaID.String(), ContentType: "video/mp4", SHA256: digest, SizeBytes: 100, Status: "READY", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "anchored-promotion-video-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: t0}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: fixture.DraftID, ResponsibilityID: fixture.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: digest, SubmittedAt: t0}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: t0, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &t0, CreatedAt: t0}).Error; err != nil {
			return err
		}
		days := 5
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &t0, RetentionDays: &days, RetentionFloorAt: &floor, RetentionAnchor: "ACTIVE_BASELINE", Status: "RETAINED", CreatedAt: t0, UpdatedAt: t0}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: digest, ObjectKey: "anchored-promotion/frame/" + frameID.String(), Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 10, CreatedAt: t0}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fixture.TenantID, PreparationID: preparationID, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", Landmarks: json.RawMessage(`[]`), CreatedAt: t0}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlan{ID: planID, TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, SubmissionID: submissionID, CurrentPreparationID: preparationID, Generation: 1, PlanDigest: digest, Status: "SEALED", SealedAt: &t0, NextAttemptAt: t0, CreatedAt: t0}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlanPreparation{TenantID: fixture.TenantID, PlanID: planID, PreparationID: preparationID, Role: "MAIN", CreatedAt: t0}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Update("active_video_plan_id", planID).Error
	}); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := Setup(Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Now: func() time.Time { return promotedAt }}); err != nil {
		t.Fatal(err)
	}
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.TenantID, Principal: requestctx.Principal{IdentityID: fixture.IdentityID, MembershipID: fixture.MembershipID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}}})
	if _, err := bus.Send(ctx, Command{TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, ClientMutationID: "anchored-promotion"}); err != nil {
		t.Fatal(err)
	}
	var source database.VideoSource
	if err := h.AdminDB.Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Take(&source).Error; err != nil {
		t.Fatal(err)
	}
	if source.RetentionAnchor != "ACTIVE_BASELINE" || source.SourceExpiryAt != nil || source.RetentionFloorAt == nil || !source.RetentionFloorAt.Equal(floor) || source.Status != "RETAINED" {
		t.Fatalf("anchored promotion anchor=%s expiry=%v floor=%v status=%s; want ACTIVE_BASELINE/nil/%v/RETAINED", source.RetentionAnchor, source.SourceExpiryAt, source.RetentionFloorAt, source.Status, floor)
	}
	var promoted database.OriginVersion
	if err := h.AdminDB.Where("tenant_id=? AND idempotency_key=?", fixture.TenantID, "video-promotion:"+fixture.InspectionID.String()).Take(&promoted).Error; err != nil || promoted.Status != "ACTIVE" {
		t.Fatalf("anchored source was not promoted: %+v err=%v", promoted, err)
	}
}
