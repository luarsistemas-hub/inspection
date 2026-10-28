//go:build integration

package expire_video_sources

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

func TestVV_IT_315CurrentSourceBecomesEligibleExactlyAtFiveDayBoundary(t *testing.T) {
	h, fixture := newTask04RetentionHarness(t)
	ctx := context.Background()
	t0 := time.Now().UTC().Add(-10 * 24 * time.Hour).Truncate(time.Microsecond)
	boundary, ok := ExpiryAt(t0, "CURRENT", 5, 30)
	if !ok || !boundary.Equal(t0.Add(5*24*time.Hour)) {
		t.Fatalf("ExpiryAt(T0, CURRENT) = %v, %t; want T0+5d", boundary, ok)
	}
	mediaID, preparationID := identity.NewID(), identity.NewID()
	key := "it315/" + mediaID.String() + ".mp4"
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, key, strings.NewReader("source"), 6, "video/mp4"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Store.Delete(context.Background(), key) })
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		digest := strings.Repeat("d", 64)
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: key, ContentType: "video/mp4", SHA256: digest, SizeBytes: 6, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "it315-" + mediaID.String(), AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: t0}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: t0, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &t0, CreatedAt: t0}).Error; err != nil {
			return err
		}
		days := 5
		return tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &t0, RetentionDays: &days, SourceExpiryAt: &boundary, Status: "RETAINED", CreatedAt: t0, UpdatedAt: t0}).Error
	}); err != nil {
		t.Fatal(err)
	}
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	before := boundary.Add(-time.Microsecond)
	if claimed, err := executor.RunDue(ctx, fixture.TenantID, before, 10); err != nil || claimed != 0 {
		t.Fatalf("sweep at T0+5d-1us claimed=%d err=%v; want 0", claimed, err)
	}
	if status := task04SourceStatus(t, h, fixture.TenantID, mediaID); status != "RETAINED" {
		t.Fatalf("source status before boundary=%s, want RETAINED", status)
	}
	if claimed, err := executor.RunDue(ctx, fixture.TenantID, boundary, 10); err != nil || claimed != 1 {
		t.Fatalf("sweep at T0+5d claimed=%d err=%v; want 1", claimed, err)
	}
	if status := task04SourceStatus(t, h, fixture.TenantID, mediaID); status != "EXPIRED" {
		t.Fatalf("source status at boundary=%s, want EXPIRED", status)
	}
}

func TestVV_IT_335PurgeRefusesBankReferencedByReportUntilReleased(t *testing.T) {
	h, fixture := newTask04RetentionHarness(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	mediaID, preparationID, versionID, responsibilityID, snapshotID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	key := "it335/" + preparationID.String() + ".jpg"
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, key, strings.NewReader("frame"), 5, "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Store.Delete(context.Background(), key) })
	referenceID := identity.NewID()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		digest := strings.Repeat("e", 64)
		originID := identity.NewID()
		if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Responsibility{ID: responsibilityID, TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, ParticipantID: fixture.ParticipantID, Status: "EXPIRED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "it335/source-" + mediaID.String(), ContentType: "video/mp4", SHA256: digest, SizeBytes: 5, Status: "READY", MediaKind: "VIDEO", IdempotencyKey: "it335-media-" + mediaID.String(), RequirementKey: "video:main", AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		supersededAt := now.AddDate(0, 0, -400)
		if err := tx.Create(&database.OriginVersion{ID: versionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: &responsibilityID, Status: "SUPERSEDED", MediaKind: "VIDEO", VideoPreparationID: &preparationID, SupersededAt: &supersededAt, IdempotencyKey: "it335-origin-" + versionID.String(), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: identity.NewID(), TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: digest, ObjectKey: key, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 5, CreatedAt: now}).Error; err != nil {
			return err
		}
		// RPT1 still cites frames of P1.
		return tx.Create(&database.VideoEvidenceReference{ID: referenceID, TenantID: fixture.TenantID, PreparationID: preparationID, OwnerType: "REPORT_SNAPSHOT", OwnerID: snapshotID, CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	if purged, err := executor.PurgeInactiveOriginBanks(ctx, fixture.TenantID, 10); err != nil || purged != 0 {
		t.Fatalf("purge while RPT1 references P1 = %d, %v; want 0", purged, err)
	}
	if frames, exists := task04BankState(t, h, fixture.TenantID, preparationID, key); frames != 1 || !exists {
		t.Fatalf("referenced P1 after refused purge: frames=%d object=%t", frames, exists)
	}
	// Releasing RPT1's dependency is what makes P1 eligible.
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.TenantID, referenceID).Delete(&database.VideoEvidenceReference{}).Error; err != nil {
		t.Fatal(err)
	}
	if purged, err := executor.PurgeInactiveOriginBanks(ctx, fixture.TenantID, 10); err != nil || purged != 1 {
		t.Fatalf("purge after reference release = %d, %v; want 1", purged, err)
	}
	if frames, exists := task04BankState(t, h, fixture.TenantID, preparationID, key); frames != 0 || exists {
		t.Fatalf("released P1 after purge: frames=%d object=%t", frames, exists)
	}
}

func newTask04RetentionHarness(t *testing.T) (*harness.Harness, harness.Task06Fixture) {
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
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return h, fixture
}

func task04SourceStatus(t *testing.T, h *harness.Harness, tenantID, mediaID identity.ID) string {
	t.Helper()
	var source database.VideoSource
	if err := h.AdminDB.Where("tenant_id=? AND media_id=?", tenantID, mediaID).Take(&source).Error; err != nil {
		t.Fatal(err)
	}
	return source.Status
}

func task04BankState(t *testing.T, h *harness.Harness, tenantID, preparationID identity.ID, key string) (int64, bool) {
	t.Helper()
	var frames int64
	if err := h.AdminDB.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", tenantID, preparationID).Count(&frames).Error; err != nil {
		t.Fatal(err)
	}
	reader, _, err := (objectstore.VideoStore{Store: h.Store}).Open(context.Background(), key)
	if err != nil {
		return frames, false
	}
	_ = reader.Close()
	return frames, true
}
