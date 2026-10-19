//go:build integration

package expire_video_sources

import (
	"context"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

// task07Source is a due current source C1 with original, playback proxy,
// retained frames F11/F12 and an RPT1 dependency on its preparation.
type task07Source struct {
	tenantID, mediaID, preparationID, referenceID identity.ID
	originalKey, proxyKey                         string
	frameKeys                                     []string
	expiry                                        time.Time
}

func seedTask07DueSource(t *testing.T, h *harness.Harness, fixture harness.Task06Fixture) task07Source {
	t.Helper()
	ctx := context.Background()
	t0 := time.Now().UTC().Add(-6 * 24 * time.Hour).Truncate(time.Microsecond)
	s := task07Source{tenantID: fixture.TenantID, mediaID: identity.NewID(), preparationID: identity.NewID(), referenceID: identity.NewID(), expiry: t0.Add(5 * 24 * time.Hour)}
	s.originalKey = "it-task07/" + s.mediaID.String() + ".mp4"
	s.proxyKey = "it-task07/" + s.mediaID.String() + "-proxy.mp4"
	s.frameKeys = []string{"it-task07/" + s.mediaID.String() + "-f11.jpg", "it-task07/" + s.mediaID.String() + "-f12.jpg"}
	for _, key := range append([]string{s.originalKey, s.proxyKey}, s.frameKeys...) {
		if err := h.Store.Client.Put(ctx, h.Store.Bucket, key, strings.NewReader("bytes"), 5, "application/octet-stream"); err != nil {
			t.Fatal(err)
		}
		key := key
		t.Cleanup(func() { _ = h.Store.Delete(context.Background(), key) })
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		digest := strings.Repeat("c", 64)
		if err := tx.Create(&database.MediaObject{ID: s.mediaID, TenantID: s.tenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: s.originalKey, ContentType: "video/mp4", SHA256: digest, SizeBytes: 5, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "it-task07-" + s.mediaID.String(), AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: t0}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaDerivative{ID: identity.NewID(), TenantID: s.tenantID, MediaID: s.mediaID, ObjectKey: s.proxyKey, Kind: "VIDEO_PLAYBACK", SHA256: digest, ContentType: "video/mp4", Width: 2, Height: 2, SizeBytes: 5, CreatedAt: t0}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: s.preparationID, TenantID: s.tenantID, MediaID: s.mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: t0, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &t0, CreatedAt: t0}).Error; err != nil {
			return err
		}
		days := 5
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: s.tenantID, MediaID: s.mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 90_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &t0, RetentionDays: &days, SourceExpiryAt: &s.expiry, Status: "RETAINED", CreatedAt: t0, UpdatedAt: t0}).Error; err != nil {
			return err
		}
		for i, key := range s.frameKeys {
			if err := tx.Create(&database.VideoFrame{ID: identity.NewID(), TenantID: s.tenantID, SourceMediaID: s.mediaID, PreparationID: s.preparationID, PTSMicros: int64(70+i) * 1_000_000, SHA256: digest, ObjectKey: key, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 5, CreatedAt: t0}).Error; err != nil {
				return err
			}
		}
		return tx.Create(&database.VideoEvidenceReference{ID: s.referenceID, TenantID: s.tenantID, PreparationID: s.preparationID, OwnerType: "REPORT_SNAPSHOT", OwnerID: identity.NewID(), CreatedAt: t0}).Error
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func task07ObjectExists(t *testing.T, h *harness.Harness, key string) bool {
	t.Helper()
	reader, _, err := (objectstore.VideoStore{Store: h.Store}).Open(context.Background(), key)
	if err != nil {
		return false
	}
	_ = reader.Close()
	return true
}

func task07LoadSource(t *testing.T, h *harness.Harness, s task07Source) database.VideoSource {
	t.Helper()
	var source database.VideoSource
	if err := h.AdminDB.Where("tenant_id=? AND media_id=?", s.tenantID, s.mediaID).Take(&source).Error; err != nil {
		t.Fatal(err)
	}
	return source
}

func assertTask07RetainedEvidence(t *testing.T, h *harness.Harness, s task07Source) {
	t.Helper()
	var frames, references int64
	if err := h.AdminDB.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", s.tenantID, s.preparationID).Count(&frames).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.VideoEvidenceReference{}).Where("tenant_id=? AND id=?", s.tenantID, s.referenceID).Count(&references).Error; err != nil {
		t.Fatal(err)
	}
	if frames != 2 || references != 1 {
		t.Fatalf("retained evidence frames=%d RPT1 references=%d; want 2 and 1", frames, references)
	}
	for _, key := range s.frameKeys {
		if !task07ObjectExists(t, h, key) {
			t.Fatalf("retained frame object %s was deleted", key)
		}
	}
}

func TestVV_IT_310SweepDueUnheldSourceRemovesOriginalAndProxyKeepsFramesAndReport(t *testing.T) {
	h, fixture := newTask04RetentionHarness(t)
	s := seedTask07DueSource(t, h, fixture)
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := executor.RunDue(context.Background(), s.tenantID, s.expiry.Add(time.Minute), 10); err != nil || claimed != 1 {
		t.Fatalf("sweep claimed=%d err=%v; want 1", claimed, err)
	}
	if source := task07LoadSource(t, h, s); source.Status != "EXPIRED" || source.OriginalDeletedAt == nil {
		t.Fatalf("swept source status=%s originalDeletedAt=%v; want EXPIRED with deletion time", source.Status, source.OriginalDeletedAt)
	}
	if task07ObjectExists(t, h, s.originalKey) || task07ObjectExists(t, h, s.proxyKey) {
		t.Fatalf("original or playback proxy remains available after expiry")
	}
	assertTask07RetainedEvidence(t, h, s)
}

func TestVV_IT_319RepeatCleanupAfterExpiryKeepsFramesAndExpiredSource(t *testing.T) {
	h, fixture := newTask04RetentionHarness(t)
	s := seedTask07DueSource(t, h, fixture)
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if claimed, err := executor.RunDue(ctx, s.tenantID, s.expiry.Add(time.Minute), 10); err != nil || claimed != 1 {
		t.Fatalf("first sweep claimed=%d err=%v; want 1", claimed, err)
	}
	first := task07LoadSource(t, h, s)
	for i := 0; i < 3; i++ {
		if claimed, err := executor.RunDue(ctx, s.tenantID, s.expiry.Add(time.Duration(i+2)*time.Hour), 10); err != nil || claimed != 0 {
			t.Fatalf("repeat sweep %d claimed=%d err=%v; want 0", i, claimed, err)
		}
		if err := executor.DeleteRequested(ctx, s.tenantID, s.mediaID); err != nil {
			t.Fatalf("repeat requested deletion %d: %v", i, err)
		}
	}
	again := task07LoadSource(t, h, s)
	if again.Status != "EXPIRED" || again.OriginalDeletedAt == nil || !again.OriginalDeletedAt.Equal(*first.OriginalDeletedAt) || again.DeleteErrorCode != "" {
		t.Fatalf("repeat cleanup changed expired source: before=%+v after=%+v", first, again)
	}
	assertTask07RetainedEvidence(t, h, s)
}

func TestVV_IT_330DueSourceUnderActiveSourceHoldKeepsBytes(t *testing.T) {
	h, fixture := newTask04RetentionHarness(t)
	s := seedTask07DueSource(t, h, fixture)
	holdID := identity.NewID()
	if err := h.AdminDB.Create(&database.LegalHold{ID: holdID, TenantID: s.tenantID, SourceMediaID: &s.mediaID, Reason: "H1 preserve source", Active: true, CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if claimed, err := executor.RunDue(ctx, s.tenantID, s.expiry.Add(time.Hour), 10); err != nil || claimed != 0 {
		t.Fatalf("sweep under H1 claimed=%d err=%v; want 0", claimed, err)
	}
	if source := task07LoadSource(t, h, s); source.Status != "RETAINED" || source.OriginalDeletedAt != nil {
		t.Fatalf("held source status=%s originalDeletedAt=%v; want RETAINED", source.Status, source.OriginalDeletedAt)
	}
	if !task07ObjectExists(t, h, s.originalKey) || !task07ObjectExists(t, h, s.proxyKey) {
		t.Fatal("held source bytes were deleted")
	}
	assertTask07RetainedEvidence(t, h, s)
	// Releasing H1 is what lets the frozen deadline take effect.
	if err := h.AdminDB.Model(&database.LegalHold{}).Where("tenant_id=? AND id=?", s.tenantID, holdID).Updates(map[string]any{"active": false, "released_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if claimed, err := executor.RunDue(ctx, s.tenantID, s.expiry.Add(2*time.Hour), 10); err != nil || claimed != 1 {
		t.Fatalf("sweep after H1 release claimed=%d err=%v; want 1", claimed, err)
	}
}

func TestVV_IT_317ExpiryRaceWithProtectionCommittedUnderSourceLockDoesNotClaim(t *testing.T) {
	for _, protection := range []string{"lease", "hold"} {
		t.Run(protection, func(t *testing.T) {
			h, fixture := newTask04RetentionHarness(t)
			s := seedTask07DueSource(t, h, fixture)
			executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			now := s.expiry.Add(time.Minute)
			// The protector holds the source row lock, as playback lease
			// acquisition does, while the sweep runs concurrently.
			protector := h.AdminDB.Begin()
			if protector.Error != nil {
				t.Fatal(protector.Error)
			}
			defer protector.Rollback()
			var locked database.VideoSource
			if err := protector.Raw("SELECT * FROM media.video_sources WHERE tenant_id=? AND media_id=? FOR UPDATE", s.tenantID, s.mediaID).Scan(&locked).Error; err != nil || locked.ID == (identity.ID{}) {
				t.Fatalf("lock source: %v id=%s", err, locked.ID)
			}
			if protection == "lease" {
				err = protector.Create(&database.VideoSourceLease{ID: identity.NewID(), TenantID: s.tenantID, SourceMediaID: s.mediaID, Purpose: "PLAYBACK", LeaseUntil: now.Add(time.Hour), CreatedAt: now}).Error
			} else {
				err = protector.Create(&database.LegalHold{ID: identity.NewID(), TenantID: s.tenantID, SourceMediaID: &s.mediaID, Reason: "race hold", Active: true, CreatedAt: now}).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			if claimed, err := executor.RunDue(ctx, s.tenantID, now, 10); err != nil || claimed != 0 {
				t.Fatalf("sweep racing locked protection claimed=%d err=%v; want 0", claimed, err)
			}
			if err := protector.Commit().Error; err != nil {
				t.Fatal(err)
			}
			if claimed, err := executor.RunDue(ctx, s.tenantID, now, 10); err != nil || claimed != 0 {
				t.Fatalf("sweep after committed %s claimed=%d err=%v; want 0", protection, claimed, err)
			}
			if source := task07LoadSource(t, h, s); source.Status != "RETAINED" || source.OriginalDeletedAt != nil {
				t.Fatalf("protected source status=%s originalDeletedAt=%v; want RETAINED", source.Status, source.OriginalDeletedAt)
			}
			if !task07ObjectExists(t, h, s.originalKey) {
				t.Fatal("protected source original was deleted")
			}
		})
	}
}
