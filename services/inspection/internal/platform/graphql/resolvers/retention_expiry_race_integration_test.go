//go:build integration

package resolvers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	expirevideo "inspection/services/inspection/internal/features/retention/expire_video_sources"
	purgedata "inspection/services/inspection/internal/features/retention/purge_data"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestExpirySerializesPlaybackLeaseAndLegalHoldIT604(t *testing.T) {
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
	firstReady, expiry := now.Add(-48*time.Hour), now.Add(-time.Hour)
	mediaID, attemptID, preparationID, frameID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	digestBytes := sha256.Sum256([]byte("it604"))
	digest := hex.EncodeToString(digestBytes[:])
	originalKey := fmt.Sprintf("video-sources/it604/%s", mediaID)
	frameKey := fmt.Sprintf("video-frames/it324/%s.jpg", frameID)
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, frameKey, strings.NewReader("frame-bytes"), int64(len("frame-bytes")), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Store.Delete(context.Background(), frameKey) })
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "it604-attempt", PayloadDigest: digest, AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: originalKey, ContentType: "video/mp4", SHA256: digest, SizeBytes: 5, Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "it604-media", AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &firstReady, CreatedAt: firstReady}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &firstReady, SourceExpiryAt: &expiry, Status: "RETAINED", CreatedAt: firstReady, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoFrame{ID: frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: digest, ObjectKey: frameKey, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: int64(len("frame-bytes")), CreatedAt: firstReady}).Error
	}); err != nil {
		t.Fatal(err)
	}
	executor, err := expirevideo.Setup(expirevideo.Dependencies{DB: h.DB, Store: h.Store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	leaseID := identity.NewID()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.VideoSourceLease{ID: leaseID, TenantID: fixture.TenantID, SourceMediaID: mediaID, Purpose: "PLAYBACK", LeaseUntil: now.Add(10 * time.Minute), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if claimed, err := executor.RunDue(ctx, fixture.TenantID, now, 1); err != nil || claimed != 0 {
		t.Fatalf("sweep with active playback lease claimed=%d err=%v; want 0", claimed, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", fixture.TenantID, leaseID).Delete(&database.VideoSourceLease{}).Error
	}); err != nil {
		t.Fatal(err)
	}

	locked, release := make(chan struct{}), make(chan struct{})
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			var source database.VideoSource
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Take(&source).Error; err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	select {
	case <-locked:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out acquiring source row lock")
	}
	server := task06GraphQLServer(h.DB)
	adminContext := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fixture.TenantID, Principal: requestctx.Principal{IdentityID: fixture.IdentityID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}}})
	holdResponse := make(chan string, 1)
	go func() {
		query := `mutation { applyLegalHold(input:{sourceMediaId:"` + mediaID.String() + `",reason:"expiry race",clientMutationId:"it604"}) { status } }`
		holdResponse <- executeTask06GraphQL(t, server, query, adminContext)
	}()
	waitForSourceLockWait(t, h.AdminDB, 5*time.Second)
	if claimed, err := executor.RunDue(ctx, fixture.TenantID, now, 1); err != nil || claimed != 0 {
		close(release)
		t.Fatalf("sweep while legal hold waits on source lock claimed=%d err=%v; want 0", claimed, err)
	}
	close(release)
	if err := <-lockDone; err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-holdResponse:
		if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"status":"HELD"`) {
			t.Fatalf("hold did not win after source lock release: %s", response)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for legal-hold mutation")
	}
	if claimed, err := executor.RunDue(ctx, fixture.TenantID, now, 1); err != nil || claimed != 0 {
		t.Fatalf("sweep after committed legal hold claimed=%d err=%v; want 0", claimed, err)
	}
	var source database.VideoSource
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Take(&source).Error
	}); err != nil {
		t.Fatal(err)
	}
	if source.Status != "RETAINED" {
		t.Fatalf("source status=%s after held expiry race; want RETAINED", source.Status)
	}
	if _, err := purgedata.PurgeWithStore(ctx, h.AdminDB, h.Store, fixture.TenantID, fixture.InspectionID, retentioncore.Clock{DueAt: now.Add(-time.Hour)}); err == nil {
		t.Fatal("evidence purge succeeded while source hold protected its source and frame bank")
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var frame database.VideoFrame
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, frameID).Take(&frame).Error; err != nil {
			return err
		}
		if frame.ObjectKey != frameKey {
			t.Errorf("held frame key=%s, want %s", frame.ObjectKey, frameKey)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reader, _, err := (objectstore.VideoStore{Store: h.Store}).Open(ctx, frameKey)
	if err != nil {
		t.Fatalf("held frame bytes were purged: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
}

func waitForSourceLockWait(t *testing.T, db *gorm.DB, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var waiting bool
		if err := db.Raw("SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query ILIKE '%video_sources%')").Scan(&waiting).Error; err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("legal-hold transaction did not block on the source row lock")
}
