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

func TestVV_IT_061PromoteVideoPreservesPreparationAndPlanLineage(t *testing.T) {
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
	now := time.Now().UTC().Truncate(time.Microsecond)
	firstReady := now.Add(-40 * 24 * time.Hour)
	expiry := firstReady.Add(30 * 24 * time.Hour)
	deleted := expiry.Add(time.Hour)
	attemptID, mediaID, submissionID := identity.NewID(), identity.NewID(), identity.NewID()
	preparationID, frameID, planID := identity.NewID(), identity.NewID(), identity.NewID()
	manifest := strings.Repeat("a", 64)
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Update("media_kind", "VIDEO").Error; err != nil {
			return err
		}
		attempt := database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "expired-promotion", PayloadDigest: manifest, AdmittedAt: firstReady, ExpiresAt: now, CreatedAt: firstReady}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		media := database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "tenant/" + fixture.TenantID.String() + "/media/" + mediaID.String(), ContentType: "video/mp4", SHA256: manifest, SizeBytes: 100, Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "expired-promotion-video", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: firstReady}
		if err := tx.Create(&media).Error; err != nil {
			return err
		}
		submission := database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: fixture.DraftID, ResponsibilityID: fixture.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: manifest, SubmittedAt: firstReady}
		if err := tx.Create(&submission).Error; err != nil {
			return err
		}
		preparation := database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: firstReady, SourceSHA256: manifest, ManifestDigest: manifest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &firstReady, CreatedAt: firstReady}
		if err := tx.Create(&preparation).Error; err != nil {
			return err
		}
		days := 30
		source := database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &firstReady, RetentionDays: &days, SourceExpiryAt: &expiry, Status: "EXPIRED", OriginalDeletedAt: &deleted, CreatedAt: firstReady, UpdatedAt: deleted}
		if err := tx.Create(&source).Error; err != nil {
			return err
		}
		frame := database.VideoFrame{ID: frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: manifest, ObjectKey: "video-frames/" + fixture.TenantID.String() + "/" + frameID.String() + ".jpg", Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 10, CreatedAt: firstReady}
		if err := tx.Create(&frame).Error; err != nil {
			return err
		}
		scope := database.VideoScopeItem{ID: identity.NewID(), TenantID: fixture.TenantID, PreparationID: preparationID, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", Landmarks: json.RawMessage(`[]`), CreatedAt: firstReady}
		if err := tx.Create(&scope).Error; err != nil {
			return err
		}
		plan := database.VideoPlan{ID: planID, TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, SubmissionID: submissionID, CurrentPreparationID: preparationID, Generation: 1, PlanDigest: manifest, Status: "SEALED", SealedAt: &firstReady, CreatedAt: firstReady}
		if err := tx.Create(&plan).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlanPreparation{TenantID: fixture.TenantID, PlanID: planID, PreparationID: preparationID, Role: "MAIN", CreatedAt: firstReady}).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Update("active_video_plan_id", planID).Error; err != nil {
			return err
		}
		return tx.Model(&database.MediaObject{}).Where("tenant_id=? AND id=?", fixture.TenantID, mediaID).Update("status", "READY").Error
	}); err != nil {
		t.Fatal(err)
	}

	bus := mediator.New()
	if err := Setup(Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	principal := requestctx.Principal{IdentityID: fixture.IdentityID, MembershipID: fixture.MembershipID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}}
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.TenantID, Principal: principal})
	raw, err := bus.Send(ctx, Command{TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, ClientMutationID: "expired-promotion"})
	if err != nil {
		t.Fatal(err)
	}
	promoted, ok := raw.(database.OriginVersion)
	if !ok || promoted.Status != "ACTIVE" || promoted.MediaKind != "VIDEO" || promoted.VideoPreparationID == nil || *promoted.VideoPreparationID != preparationID || promoted.VideoPlanID == nil || *promoted.VideoPlanID != planID {
		t.Fatalf("promotion did not pin the retained frame bank: %#v", raw)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var source database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Take(&source).Error; err != nil {
			return err
		}
		if source.Status != "EXPIRED" || source.SourceExpiryAt == nil || !source.SourceExpiryAt.Equal(expiry) || source.OriginalDeletedAt == nil || !source.OriginalDeletedAt.Equal(deleted) || source.FirstFrameReadyAt == nil || !source.FirstFrameReadyAt.Equal(firstReady) {
			t.Errorf("promotion restored or extended expired source: status=%s expiry=%v deleted=%v", source.Status, source.SourceExpiryAt, source.OriginalDeletedAt)
		}
		var references int64
		if err := tx.Model(&database.VideoEvidenceReference{}).Where("tenant_id=? AND preparation_id=? AND owner_type='ORIGIN_VERSION' AND owner_id=?", fixture.TenantID, preparationID, promoted.ID).Count(&references).Error; err != nil {
			return err
		}
		if references != 1 {
			t.Errorf("retained frame dependency count=%d, want 1", references)
		}
		var frames int64
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND id=? AND preparation_id=?", fixture.TenantID, frameID, preparationID).Count(&frames).Error; err != nil {
			return err
		}
		if frames != 1 {
			t.Errorf("retained frame count=%d, want 1", frames)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestVV_IT_321PromoteExpiredCurrentKeepsFrameOnlyOrigin promotes an already
// expired current source C1: the frame-only origin is created while the
// deleted source and its first-readiness anchor stay untouched.
func TestVV_IT_321PromoteExpiredCurrentKeepsFrameOnlyOrigin(t *testing.T) {
	TestVV_IT_061PromoteVideoPreservesPreparationAndPlanLineage(t)
}

func TestVV_IT_069PromotionRollsBackOriginReferenceAfterLateFailure(t *testing.T) {
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
	now := time.Now().UTC().Truncate(time.Microsecond)
	firstReady := now.Add(-2 * 24 * time.Hour)
	expiry := firstReady.Add(5 * 24 * time.Hour)
	attemptID, mediaID, submissionID := identity.NewID(), identity.NewID(), identity.NewID()
	preparationID, frameID, planID := identity.NewID(), identity.NewID(), identity.NewID()
	manifest := strings.Repeat("b", 64)
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Update("media_kind", "VIDEO").Error; err != nil {
			return err
		}
		attempt := database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "it069-source", PayloadDigest: manifest, AdmittedAt: firstReady, ExpiresAt: now.Add(time.Hour), CreatedAt: firstReady}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		media := database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "tenant/" + fixture.TenantID.String() + "/it069-source", ContentType: "video/mp4", SHA256: manifest, SizeBytes: 100, Status: "READY", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "it069-video", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: firstReady}
		if err := tx.Create(&media).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: fixture.DraftID, ResponsibilityID: fixture.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: manifest, SubmittedAt: firstReady}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: firstReady, SourceSHA256: manifest, ManifestDigest: manifest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &firstReady, CreatedAt: firstReady}).Error; err != nil {
			return err
		}
		days := 30
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &firstReady, RetentionDays: &days, SourceExpiryAt: &expiry, Status: "RETAINED", CreatedAt: firstReady, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: manifest, ObjectKey: "video-frames/it069/" + frameID.String() + ".jpg", Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 10, CreatedAt: firstReady}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fixture.TenantID, PreparationID: preparationID, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", Landmarks: json.RawMessage(`[]`), CreatedAt: firstReady}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlan{ID: planID, TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, SubmissionID: submissionID, CurrentPreparationID: preparationID, Generation: 1, PlanDigest: manifest, Status: "SEALED", SealedAt: &firstReady, CreatedAt: firstReady}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlanPreparation{TenantID: fixture.TenantID, PlanID: planID, PreparationID: preparationID, Role: "MAIN", CreatedAt: firstReady}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Update("active_video_plan_id", planID).Error
	}); err != nil {
		t.Fatal(err)
	}
	const triggerFunction = "it069_fail_after_origin_reference"
	const triggerName = "it069_fail_on_source_expiry_extension"
	if err := h.AdminDB.Exec("CREATE FUNCTION " + triggerFunction + "() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced promotion rollback'; END $$").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = h.AdminDB.Exec("DROP TRIGGER IF EXISTS " + triggerName + " ON media.video_sources").Error
		_ = h.AdminDB.Exec("DROP FUNCTION IF EXISTS " + triggerFunction + "()").Error
	})
	if err := h.AdminDB.Exec("CREATE TRIGGER " + triggerName + " BEFORE UPDATE OF source_expiry_at ON media.video_sources FOR EACH ROW EXECUTE FUNCTION " + triggerFunction + "()").Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := Setup(Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.TenantID, Principal: requestctx.Principal{IdentityID: fixture.IdentityID, MembershipID: fixture.MembershipID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}}})
	if _, err := bus.Send(ctx, Command{TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, ClientMutationID: "it069"}); err == nil {
		t.Fatal("promotion succeeded despite injected failure after reference insertion")
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var versions, references, origins int64
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND responsibility_id=? AND media_kind='VIDEO'", fixture.TenantID, fixture.ResponsibilityID).Count(&versions).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoEvidenceReference{}).Where("tenant_id=? AND preparation_id=? AND owner_type='ORIGIN_VERSION'", fixture.TenantID, preparationID).Count(&references).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.Origin{}).Where("tenant_id=? AND asset_id=?", fixture.TenantID, fixture.AssetID).Count(&origins).Error; err != nil {
			return err
		}
		if versions != 0 || references != 0 || origins != 0 {
			t.Errorf("failed promotion left partial state: versions=%d references=%d origins=%d", versions, references, origins)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
