//go:build integration

package resolvers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestApplyHoldAfterSourceExpiryProtectsFramesAndPreservesExpiredStatusIT329(t *testing.T) {
	f := newVideoHoldFixture(t, "EXPIRED")
	query := `mutation { applyLegalHold(input:{sourceMediaId:"` + f.mediaID.String() + `",reason:"post-expiry hold",clientMutationId:"it329-hold"}) { status } }`
	response := executeTask06GraphQL(t, f.server, query, f.adminContext)
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"status":"HELD"`) {
		t.Fatalf("apply hold after expiry failed: %s", response)
	}
	var source database.VideoSource
	var hold database.LegalHold
	if err := f.h.WithinTenant(context.Background(), f.tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND media_id=?", f.tenantID, f.mediaID).Take(&source).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND source_media_id=? AND active=true", f.tenantID, f.mediaID).Take(&hold).Error
	}); err != nil {
		t.Fatal(err)
	}
	if source.Status != "EXPIRED" {
		t.Fatalf("hold changed source status to %s; want EXPIRED", source.Status)
	}
	if hold.SourceMediaID == nil || *hold.SourceMediaID != f.mediaID {
		t.Fatalf("hold does not cover expired source: %+v", hold)
	}
	assertVideoHoldFrameRetained(t, f)
}

func TestReplayHoldAndDeletionRequestKeepsSameLogicalScopeIT334(t *testing.T) {
	f := newVideoHoldFixture(t, "RETAINED")
	holdMutation := `mutation { applyLegalHold(input:{sourceMediaId:"` + f.mediaID.String() + `",reason:"H1",clientMutationId:"it334-h1"}) { status } }`
	for i := 0; i < 2; i++ {
		response := executeTask06GraphQL(t, f.server, holdMutation, f.adminContext)
		if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"status":"HELD"`) {
			t.Fatalf("hold replay %d failed: %s", i+1, response)
		}
	}
	deletionMutation := `mutation { recordDeletionRequest(input:{sourceMediaId:"` + f.mediaID.String() + `",reason:"purge C1",clientMutationId:"it334-purge"}) { status requestId } }`
	for i := 0; i < 2; i++ {
		response := executeTask06GraphQL(t, f.server, deletionMutation, f.adminContext)
		if !strings.Contains(response, "INVALID_STATE") || !strings.Contains(response, "legal hold prevents source deletion") {
			t.Fatalf("purge request replay %d did not preserve the held source scope: %s", i+1, response)
		}
	}
	var holds, requests int64
	if err := f.h.WithinTenant(context.Background(), f.tenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.LegalHold{}).Where("tenant_id=? AND source_media_id=? AND active=true", f.tenantID, f.mediaID).Count(&holds).Error; err != nil {
			return err
		}
		return tx.Model(&database.DeletionRequest{}).Where("tenant_id=? AND source_media_id=? AND status='REQUESTED'", f.tenantID, f.mediaID).Count(&requests).Error
	}); err != nil {
		t.Fatal(err)
	}
	if holds != 1 || requests != 0 {
		t.Fatalf("logical scope changed under hold: active holds=%d deletion requests=%d; want one hold and no accepted purge request", holds, requests)
	}
	assertVideoHoldFrameRetained(t, f)
}

type videoHoldFixture struct {
	h                                  *harness.Harness
	tenantID, mediaID, prepID, frameID identity.ID
	frameKey                           string
	server                             http.Handler
	adminContext                       context.Context
}

func newVideoHoldFixture(t *testing.T, status string) videoHoldFixture {
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
	seed, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f := videoHoldFixture{h: h, tenantID: seed.TenantID, mediaID: identity.NewID(), prepID: identity.NewID(), frameID: identity.NewID()}
	now := time.Now().UTC().Truncate(time.Microsecond)
	f.frameKey = fmt.Sprintf("it329-334/%s/%s.jpg", f.tenantID, f.frameID)
	frameBytes := "retained-frame"
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, f.frameKey, strings.NewReader(frameBytes), int64(len(frameBytes)), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Store.Delete(context.Background(), f.frameKey) })
	ready, expiry := now.Add(-8*24*time.Hour), now.Add(-time.Hour)
	if err := h.WithinTenant(ctx, f.tenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.MediaObject{ID: f.mediaID, TenantID: f.tenantID, ResponsibilityID: seed.ResponsibilityID, ObjectKey: "it329-334/source/" + f.mediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("f", 64), SizeBytes: 20, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "it329-334-media-" + f.mediaID.String(), AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: ready}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: f.prepID, TenantID: f.tenantID, MediaID: f.mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: ready, SourceSHA256: strings.Repeat("f", 64), ManifestDigest: strings.Repeat("f", 64), PendingObjectKeys: []byte(`[]`), FirstReadyAt: &ready, CreatedAt: ready}).Error; err != nil {
			return err
		}
		days := 5
		var readyAt, expiryAt *time.Time
		if status != "EXPIRED" {
			readyAt, expiryAt = &ready, &expiry
		}
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: f.tenantID, MediaID: f.mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: readyAt, RetentionDays: &days, SourceExpiryAt: expiryAt, Status: status, OriginalDeletedAt: func() *time.Time {
			if status == "EXPIRED" {
				return &now
			}
			return nil
		}(), CreatedAt: ready, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoFrame{ID: f.frameID, TenantID: f.tenantID, SourceMediaID: f.mediaID, PreparationID: f.prepID, PTSMicros: 0, SHA256: strings.Repeat("f", 64), ObjectKey: f.frameKey, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: int64(len(frameBytes)), CreatedAt: ready}).Error
	}); err != nil {
		t.Fatal(err)
	}
	f.server = task06GraphQLServer(h.DB)
	f.adminContext = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.tenantID, Principal: requestctx.Principal{IdentityID: seed.IdentityID, TenantID: f.tenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "it329-334"})
	return f
}

func assertVideoHoldFrameRetained(t *testing.T, f videoHoldFixture) {
	t.Helper()
	var count int64
	if err := f.h.WithinTenant(context.Background(), f.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND id=? AND object_key=?", f.tenantID, f.frameID, f.frameKey).Count(&count).Error
	}); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("held frame rows=%d; want 1", count)
	}
	reader, _, err := (objectstore.VideoStore{Store: f.h.Store}).Open(context.Background(), f.frameKey)
	if err != nil {
		t.Fatalf("held frame bytes were deleted: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
}
