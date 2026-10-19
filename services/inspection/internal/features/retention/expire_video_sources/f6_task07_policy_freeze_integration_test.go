//go:build integration

package expire_video_sources

import (
	"context"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	configurevideopolicy "inspection/services/inspection/internal/features/media/configure_video_policy"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestVV_IT_018PolicyChangeAfterFreezeKeepsC1Expiry(t *testing.T) {
	h, fixture := newTask04RetentionHarness(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	frozen, ok := ExpiryAt(t0, "CURRENT", 5, 30)
	if !ok || !frozen.Equal(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("frozen C1 expiry = %v", frozen)
	}
	mediaID := identity.NewID()
	key := "it018/" + mediaID.String() + ".mp4"
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, key, strings.NewReader("source"), 6, "video/mp4"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Store.Delete(context.Background(), key) })
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		digest := strings.Repeat("d", 64)
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: key, ContentType: "video/mp4", SHA256: digest, SizeBytes: 6, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "it018-" + mediaID.String(), AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: t0}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: t0, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &t0, CreatedAt: t0}).Error; err != nil {
			return err
		}
		days := 5
		return tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &t0, RetentionDays: &days, SourceExpiryAt: &frozen, Status: "RETAINED", CreatedAt: t0, UpdatedAt: t0}).Error
	}); err != nil {
		t.Fatal(err)
	}
	// The tenant raises current retention to 7 days after C1 was frozen.
	bus := mediator.New()
	if err := configurevideopolicy.Setup(configurevideopolicy.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Within: h.WithinTenant}); err != nil {
		t.Fatal(err)
	}
	admin := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.TenantID, Principal: requestctx.Principal{IdentityID: fixture.IdentityID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}}})
	var policy database.VideoPolicy
	_ = h.AdminDB.Where("tenant_id=?", fixture.TenantID).Take(&policy).Error
	expected := policy.Version
	if expected == 0 {
		expected = 1
	}
	if _, err := bus.Send(admin, configurevideopolicy.Command{TenantID: fixture.TenantID, MaxDurationMinutes: 15, OriginRetentionDays: 30, CurrentRetentionDays: 7, ExpectedVersion: expected, MutationID: "vv-it-018"}); err != nil {
		t.Fatalf("policy change to 7 days: %v", err)
	}
	var source database.VideoSource
	if err := h.AdminDB.Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Take(&source).Error; err != nil {
		t.Fatal(err)
	}
	if source.SourceExpiryAt == nil || !source.SourceExpiryAt.Equal(frozen) || source.RetentionDays == nil || *source.RetentionDays != 5 {
		t.Fatalf("policy change moved frozen C1 expiry: expiry=%v days=%v", source.SourceExpiryAt, source.RetentionDays)
	}
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := executor.RunDue(ctx, fixture.TenantID, frozen, 10); err != nil || claimed != 1 {
		t.Fatalf("sweep at the frozen 2026-10-19T12:00:00Z deadline claimed=%d err=%v; want 1", claimed, err)
	}
}
