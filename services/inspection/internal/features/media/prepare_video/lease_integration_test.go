//go:build integration

package prepare_video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

type scopeGatewayFunc func(context.Context, llm.StructuredRequest) (llm.StructuredResult, error)

func (f scopeGatewayFunc) CompleteStructured(ctx context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
	return f(ctx, request)
}

func TestVV_IT_591_592GatewayWaitHasNoOpenPreparationTransactionAndLeaseIsFenced(t *testing.T) {
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
	if err := h.AdminDB.Model(&database.MediaObject{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.MediaID).Update("status", "SCREENED").Error; err == nil {
		t.Fatal("submitted PHOTO evidence accepted a processing status mutation")
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	attemptID, preparationID, frameID, mediaID, submissionID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	staleToken, currentToken := identity.NewID(), identity.NewID()
	frameKey := "video-frames/" + fixture.TenantID.String() + "/" + frameID.String() + ".jpg"
	var frameData bytes.Buffer
	if err := jpeg.Encode(&frameData, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(frameData.Bytes())
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, frameKey, bytes.NewReader(frameData.Bytes()), int64(frameData.Len()), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = h.Store.Client.(objectstore.Deleter).Delete(context.Background(), h.Store.Bucket, frameKey)
	})
	expiry := now.Add(5 * 24 * time.Hour)
	leaseExpiry := now.Add(-time.Second)
	days := 5
	err = h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "lease-it592", PayloadDigest: hex.EncodeToString(digest[:]), AdmittedAt: now, ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "tenant/" + fixture.TenantID.String() + "/media/" + mediaID.String(), ContentType: "video/mp4", SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(frameData.Len()), Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "lease-it592-video", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: fixture.DraftID, ResponsibilityID: fixture.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: hex.EncodeToString(digest[:]), SubmittedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "PROCESSING", Phase: "DISCOVERING", PreparationVersion: 1, Generation: 1, Attempts: 1, NextAttemptAt: time.Unix(0, 0).UTC(), LeaseToken: &staleToken, LeaseExpiresAt: &leaseExpiry, SourceSHA256: hex.EncodeToString(digest[:]), PendingObjectKeys: []byte("[]"), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &now, RetentionDays: &days, SourceExpiryAt: &expiry, Status: "RETAINED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoFrame{ID: frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: hex.EncodeToString(digest[:]), ObjectKey: frameKey, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: int64(frameData.Len()), CreatedAt: now}).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.MediaObject{}).Where("tenant_id=? AND id=?", fixture.TenantID, mediaID).Update("status", "READY").Error; err == nil {
		t.Fatal("video media became ready before its preparation job")
	}
	response := []byte(`{"observations":[{"frameId":"` + frameID.String() + `","environmentCandidate":"kitchen","componentLabel":"sink","viewLabel":"wide","visibility":"VISIBLE","landmarks":[]}],"unresolvedFrameIds":[]}`)
	entered, resume := make(chan struct{}), make(chan struct{})
	executor := &Executor{deps: Dependencies{DB: h.DB, ClaimDB: h.AdminDB, Store: objectstore.VideoStore{Store: h.Store}, Gateway: scopeGatewayFunc(func(ctx context.Context, _ llm.StructuredRequest) (llm.StructuredResult, error) {
		close(entered)
		select {
		case <-resume:
			return llm.StructuredResult{JSON: response}, nil
		case <-ctx.Done():
			return llm.StructuredResult{}, ctx.Err()
		}
	}), Now: func() time.Time { return now }, TempDir: os.TempDir(), Limits: video.WorkerConfig{RequiredFreeBytes: 1, StepTimeout: time.Minute, LeaseDuration: 120 * time.Second, HeartbeatInterval: 30 * time.Second}}, permit: make(chan struct{}, 1)}
	job := database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID}
	media := database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, SHA256: hex.EncodeToString(digest[:]), VideoAttemptID: &attemptID}
	probe := video.Probe{Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", Duration: time.Second, Width: 2, Height: 2, FrameRate: 30}
	frame := database.VideoFrame{ID: frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: hex.EncodeToString(digest[:]), ObjectKey: frameKey, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: int64(frameData.Len())}
	type runOutcome struct {
		worked bool
		err    error
	}
	staleResult := make(chan runOutcome, 1)
	go func() {
		worked, err := executor.RunDue(ctx)
		staleResult <- runOutcome{worked: worked, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("worker never reached the semantic gateway")
	}
	var openTransactions int64
	if err := h.AdminDB.Raw(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND state = 'idle in transaction' AND pid <> pg_backend_pid()`).Scan(&openTransactions).Error; err != nil {
		t.Fatalf("inspect PostgreSQL transactions while gateway waits: %v", err)
	}
	if openTransactions != 0 {
		t.Fatalf("%d PostgreSQL transactions remain open while model I/O waits", openTransactions)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var claimed database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, preparationID).Take(&claimed).Error; err != nil {
			return err
		}
		if claimed.LeaseToken == nil || *claimed.LeaseToken == staleToken {
			return errors.New("RunDue did not claim a new lease")
		}
		staleToken = *claimed.LeaseToken
		result := tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=? AND lease_token=?", fixture.TenantID, preparationID, staleToken).Update("lease_token", currentToken)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("stale lease was not replaced")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	close(resume)
	if result := <-staleResult; !result.worked || !errors.Is(result.err, errLeaseLost) {
		t.Fatalf("RunDue() after lease loss = worked=%t err=%v; want lease-lost outcome", result.worked, result.err)
	}
	assertPreparationState := func(wantStatus string, wantScopes int64) {
		t.Helper()
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			var count int64
			if err := tx.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND preparation_id=?", fixture.TenantID, preparationID).Count(&count).Error; err != nil {
				return err
			}
			var persisted database.VideoPreparationJob
			if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, preparationID).Take(&persisted).Error; err != nil {
				return err
			}
			var events int64
			if err := tx.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type=? AND correlation_id=?", fixture.TenantID, "media.video_prepared.v1", preparationID.String()).Count(&events).Error; err != nil {
				return err
			}
			manifestReady := persisted.ManifestDigest != ""
			if count != wantScopes || persisted.Status != wantStatus || events != wantScopes || manifestReady != (wantStatus == "READY") {
				t.Errorf("scope count=%d status=%s events=%d manifestReady=%t; want %d/%s", count, persisted.Status, events, manifestReady, wantScopes, wantStatus)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertPreparationState("PROCESSING", 0)
	currentExecutor := &Executor{deps: Dependencies{DB: h.DB, Store: objectstore.VideoStore{Store: h.Store}, Gateway: llmFake{result: llm.StructuredResult{JSON: response}}, Now: func() time.Time { return now }}}
	if err := currentExecutor.discoverAndSeal(ctx, reservation{job: job, token: currentToken}, media, probe, hex.EncodeToString(digest[:]), []database.VideoFrame{frame}, false); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("current lease unexpectedly lost: %v", err)
		}
		t.Fatal(err)
	}
	assertPreparationState("READY", 1)
}
