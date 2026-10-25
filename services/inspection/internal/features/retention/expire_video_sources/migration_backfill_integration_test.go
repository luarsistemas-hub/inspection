//go:build integration

package expire_video_sources_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// TestIntegration_MigrationBackfillsSupersededAtAndRetentionFloorV87V91 replays
// the v87/v91 backfills (and v92, which v91's function body precedes) on a
// fresh disposable database seeded with pre-migration shapes. It mutates the
// migration history, so it only runs against its own dedicated database.
func TestIntegration_MigrationBackfillsSupersededAtAndRetentionFloorV87V91(t *testing.T) {
	url, runtimeURL := os.Getenv("INSPECTION_TEST_BACKFILL_DATABASE_URL"), os.Getenv("INSPECTION_TEST_BACKFILL_RUNTIME_DATABASE_URL")
	if url == "" || runtimeURL == "" {
		t.Skip("a fresh disposable backfill database (privileged and runtime URLs) is required")
	}
	ctx := context.Background()
	cfg := harness.ConfigFromEnv()
	cfg.DatabaseURL, cfg.MigrationDatabaseURL, cfg.RuntimeDatabaseURL, cfg.Migrate = url, url, runtimeURL, true
	h, err := harness.New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	day := func(n int) time.Time { return now.AddDate(0, 0, n) }
	v1, v2, v3, v4, v5 := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	fixedMedia, anchoredMedia, flooredMedia := identity.NewID(), identity.NewID(), identity.NewID()
	fixedExpiry, readyAt, existingFloor := day(30), day(-2), day(90)
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		var asset database.Asset
		if err := tx.Where("tenant_id=? AND id=?", f.TenantID, f.AssetID).Take(&asset).Error; err != nil {
			return err
		}
		otherAsset := asset
		otherAsset.ID = identity.NewID()
		otherAsset.ExternalKey, otherAsset.IdempotencyKey = "backfill-"+otherAsset.ID.String(), "backfill-"+otherAsset.ID.String()
		if err := tx.Create(&otherAsset).Error; err != nil {
			return err
		}
		originA, originB := identity.NewID(), identity.NewID()
		for _, origin := range []database.Origin{{ID: originA, TenantID: f.TenantID, AssetID: f.AssetID, ActiveVersionID: &v3, Version: 3, CreatedAt: now, UpdatedAt: now}, {ID: originB, TenantID: f.TenantID, AssetID: otherAsset.ID, ActiveVersionID: &v5, Version: 2, CreatedAt: now, UpdatedAt: now}} {
			if err := tx.Create(&origin).Error; err != nil {
				return err
			}
		}
		ptr := func(t time.Time) *time.Time { return &t }
		for _, version := range []database.OriginVersion{
			// v1 never received superseded_at; its successor v2 activated at day -20.
			{ID: v1, OriginID: originA, VersionNumber: 1, Status: "SUPERSEDED", ActivatedAt: ptr(day(-30))},
			// v2 carries the wrong legacy backfill (its own activation); successor v3 activated at day -5.
			{ID: v2, OriginID: originA, VersionNumber: 2, Status: "SUPERSEDED", SupersedesID: &v1, ActivatedAt: ptr(day(-20)), SupersededAt: ptr(day(-20))},
			{ID: v3, OriginID: originA, VersionNumber: 3, Status: "ACTIVE", SupersedesID: &v2, ActivatedAt: ptr(day(-5))},
			// v4 has a genuine supersession instant that must survive the repair.
			{ID: v4, OriginID: originB, VersionNumber: 1, Status: "SUPERSEDED", ActivatedAt: ptr(day(-40)), SupersededAt: ptr(day(-15))},
			{ID: v5, OriginID: originB, VersionNumber: 2, Status: "ACTIVE", SupersedesID: &v4, ActivatedAt: ptr(day(-10))},
		} {
			version.TenantID, version.MediaKind, version.IdempotencyKey, version.CreatedAt = f.TenantID, "PHOTO", "backfill-"+version.ID.String(), day(-50)
			if err := tx.Create(&version).Error; err != nil {
				return err
			}
		}
		days := 10
		for _, source := range []database.VideoSource{
			{MediaID: fixedMedia, RetentionAnchor: "FIXED", SourceExpiryAt: &fixedExpiry},
			{MediaID: anchoredMedia, RetentionAnchor: "ACTIVE_BASELINE"},
			{MediaID: flooredMedia, RetentionAnchor: "ACTIVE_BASELINE", RetentionFloorAt: &existingFloor},
		} {
			if err := tx.Create(&database.MediaObject{ID: source.MediaID, TenantID: f.TenantID, ResponsibilityID: f.ResponsibilityID, ObjectKey: "backfill/" + source.MediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", IdempotencyKey: "backfill-" + source.MediaID.String(), AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CreatedAt: now}).Error; err != nil {
				return err
			}
			source.ID, source.TenantID, source.Container, source.Codec, source.PixelFormat, source.ColorTransfer = identity.NewID(), f.TenantID, "mp4", "h264", "yuv420p", "bt709"
			source.DurationMicros, source.FrameRate, source.ScreeningStatus, source.Status = 1, 1, "APPROVED", "RETAINED"
			source.FirstFrameReadyAt, source.RetentionDays, source.CreatedAt, source.UpdatedAt = &readyAt, &days, now, now
			if err := tx.Create(&source).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.MigrationDB.Exec(`DELETE FROM platform.schema_migrations WHERE version IN (87,91,92)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := (database.Migrator{DB: h.MigrationDB}).Migrate(ctx); err != nil {
		t.Fatalf("replay v87/v91/v92: %v", err)
	}
	var applied int64
	if err := h.MigrationDB.Raw(`SELECT count(*) FROM platform.schema_migrations WHERE version IN (87,91,92)`).Scan(&applied).Error; err != nil || applied != 3 {
		t.Fatalf("replayed migrations recorded=%d err=%v", applied, err)
	}
	versions := map[identity.ID]database.OriginVersion{}
	sources := map[identity.ID]database.VideoSource{}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		var rows []database.OriginVersion
		if err := tx.Where("tenant_id=? AND id IN ?", f.TenantID, []identity.ID{v1, v2, v4}).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			versions[row.ID] = row
		}
		var sourceRows []database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id IN ?", f.TenantID, []identity.ID{fixedMedia, anchoredMedia, flooredMedia}).Find(&sourceRows).Error; err != nil {
			return err
		}
		for _, row := range sourceRows {
			sources[row.MediaID] = row
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		id   identity.ID
		want time.Time
	}{
		{"v87 fills a missing superseded_at from the successor activation", v1, day(-20)},
		{"v91 repairs superseded_at copied from the version's own activation", v2, day(-5)},
		{"v91 keeps a genuine superseded_at", v4, day(-15)},
	} {
		if got := versions[tc.id].SupersededAt; got == nil || !got.Equal(tc.want) {
			t.Errorf("%s: superseded_at=%v, want %v", tc.name, got, tc.want)
		}
	}
	for _, tc := range []struct {
		name    string
		mediaID identity.ID
		want    time.Time
	}{
		{"v91 floor from an assigned fixed expiry", fixedMedia, fixedExpiry},
		{"v91 floor from first readiness plus retention days for an anchored source", anchoredMedia, readyAt.AddDate(0, 0, 10)},
		{"v91 keeps an existing floor", flooredMedia, existingFloor},
	} {
		if got := sources[tc.mediaID].RetentionFloorAt; got == nil || !got.Equal(tc.want) {
			t.Errorf("%s: retention_floor_at=%v, want %v", tc.name, got, tc.want)
		}
	}
}
