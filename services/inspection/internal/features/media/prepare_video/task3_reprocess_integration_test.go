//go:build integration

package prepare_video

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	managepreparation "inspection/services/inspection/internal/features/media/manage_video_preparation"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

func TestIT049ReprocessEnvironmentClipV2PreservesSnapshotAndSourceFrames(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	requirementKey := "env:" + identity.NewID().String()
	fixture := seedPreparationFixture(t, h, variedH264Clip(t), "video/mp4", "CURRENT", "CAMERA", requirementKey)
	deferUnrelatedPreparationJobs(t, h, fixture, time.Now().UTC().Add(time.Hour))
	legacyProfile, err := video.ProfileByNameVersion("standard", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Update("profile_digest", legacyProfile.Digest()).Error; err != nil {
		t.Fatal(err)
	}
	executor := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, time.Now)
	executor.deps.FreeScratchBytes = func(string) (uint64, error) { return 9 << 30, nil }
	if worked, err := executor.RunDue(ctx); err != nil || !worked {
		t.Fatalf("v1 RunDue=%t err=%v", worked, err)
	}
	var original database.VideoPreparationJob
	var frames []database.VideoFrame
	var responsibility database.Responsibility
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&original).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Order("pts_us,id").Find(&frames).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.responsibilityID).Take(&responsibility).Error
	}); err != nil {
		t.Fatal(err)
	}
	if original.Status != "READY" || original.ProfileDigest != legacyProfile.Digest() || len(frames) < 1 {
		t.Fatalf("v1 preparation=%+v frames=%d", original, len(frames))
	}
	frameIDs := make([]string, len(frames))
	for i, frame := range frames {
		frameIDs[i] = frame.ID.String()
	}
	canonical, _ := json.Marshal(map[string]any{"frameIds": frameIDs, "preparationId": original.ID.String()})
	digestBytes := sha256.Sum256(canonical)
	digest := hex.EncodeToString(digestBytes[:])
	snapshot := database.ReportSnapshot{ID: identity.NewID(), TenantID: fixture.tenantID, InspectionID: responsibility.InspectionID, Mode: "VIDEO", Classification: "ATTENTION", JSONDigest: digest, HTMLDigest: digest, VersionNumber: 1, CanonicalJSON: canonical, HTML: json.RawMessage(`"<p>snapshot</p>"`), CreatedAt: time.Now().UTC()}
	if err := h.AdminDB.Create(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := managepreparation.Setup(managepreparation.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Within: h.WithinTenant}); err != nil {
		t.Fatal(err)
	}
	requestCtx := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.tenantID, Principal: requestctx.Principal{IdentityID: fixture.actorID, TenantID: fixture.tenantID, Roles: []string{auth.TenantAdmin}}})
	statusRaw, err := bus.Send(requestCtx, managepreparation.ReprocessCommand{TenantID: fixture.tenantID, MediaID: fixture.mediaID, ExpectedPreparationVersion: original.PreparationVersion, ProfileDigest: video.DefaultProfile().Digest(), MutationID: "it049-profile-v2"})
	if err != nil {
		t.Fatalf("reprocess v2: %v", err)
	}
	status := statusRaw.(managepreparation.Status)
	if status.PreparationID == original.ID {
		t.Fatal("reprocess reused the v1 preparation")
	}
	store := &failNthFramePutIT013{Client: h.Store.Client, failAt: 2}
	executor = checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, time.Now)
	executor.deps.FreeScratchBytes = func(string) (uint64, error) { return 9 << 30, nil }
	executor.deps.Store.Store.Client = store
	if worked, err := executor.RunDue(ctx); err != nil || !worked {
		t.Fatalf("failed v2 RunDue=%t err=%v", worked, err)
	}
	if !store.failed {
		t.Fatal("test did not inject failure after writing a partial v2 frame bank")
	}
	var partialCount int64
	var failedRetry database.VideoPreparationJob
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, status.PreparationID).Take(&failedRetry).Error; err != nil {
			return err
		}
		return tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, status.PreparationID).Count(&partialCount).Error
	}); err != nil {
		t.Fatal(err)
	}
	if failedRetry.Status != "QUEUED" || failedRetry.Phase == "FRAME_BANK_READY" || partialCount == 0 {
		t.Fatalf("partial retry checkpoint status=%s phase=%s retained=%d; want queued partial frames without bank-ready checkpoint", failedRetry.Status, failedRetry.Phase, partialCount)
	}
	executor = checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return time.Now().UTC().Add(time.Minute) })
	executor.deps.FreeScratchBytes = func(string) (uint64, error) { return 9 << 30, nil }
	if worked, err := executor.RunDue(ctx); err != nil || !worked {
		t.Fatalf("v2 retry RunDue=%t err=%v", worked, err)
	}
	var reprocessed database.VideoPreparationJob
	var reprocessedFrameCount int64
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, status.PreparationID).Take(&reprocessed).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, status.PreparationID).Count(&reprocessedFrameCount).Error; err != nil {
			return err
		}
		var stored database.ReportSnapshot
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, snapshot.ID).Take(&stored).Error; err != nil {
			return err
		}
		var storedManifest map[string]any
		var expectedManifest map[string]any
		_ = json.Unmarshal(stored.CanonicalJSON, &storedManifest)
		_ = json.Unmarshal(canonical, &expectedManifest)
		if storedManifest["preparationId"] != expectedManifest["preparationId"] || stored.JSONDigest != digest {
			t.Errorf("existing report snapshot changed during reprocess: %+v", stored)
		}
		var storedFrameIDs []string
		_ = json.Unmarshal(stored.CanonicalJSON, &storedManifest)
		if raw, ok := storedManifest["frameIds"].([]any); ok {
			for _, value := range raw {
				if id, ok := value.(string); ok {
					storedFrameIDs = append(storedFrameIDs, id)
				}
			}
		}
		if len(storedFrameIDs) != len(frameIDs) {
			t.Errorf("snapshot retained frame count=%d after reprocess, want %d", len(storedFrameIDs), len(frameIDs))
		}
		for i := range frameIDs {
			if i >= len(storedFrameIDs) || storedFrameIDs[i] != frameIDs[i] {
				t.Errorf("snapshot frame list changed: got %v want %v", storedFrameIDs, frameIDs)
				break
			}
		}
		var oldCount int64
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, original.ID).Count(&oldCount).Error; err != nil {
			return err
		}
		if oldCount != int64(len(frames)) {
			t.Errorf("v1 frame bank count=%d after reprocess, want %d", oldCount, len(frames))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if reprocessed.Status != "READY" || reprocessed.ProfileDigest != video.DefaultProfile().Digest() || reprocessed.RequirementKey == nil || *reprocessed.RequirementKey != requirementKey || reprocessed.Generation != 1 || reprocessed.FramesSampled <= reprocessed.FramesRetained {
		t.Fatalf("v2 preparation lost lineage: %+v", reprocessed)
	}
	if reprocessedFrameCount != int64(reprocessed.FramesRetained) || reprocessedFrameCount <= partialCount || reprocessed.FramesRepresentative > 4 {
		t.Fatalf("v2 retry sealed an incomplete or over-budget frame bank: rows=%d sampled=%d retained=%d representative=%d partial=%d", reprocessedFrameCount, reprocessed.FramesSampled, reprocessed.FramesRetained, reprocessed.FramesRepresentative, partialCount)
	}
	// The source remains available, so a frame omitted by v1 dedup can be extracted
	// again under the new selection profile instead of depending on a deleted JPEG.
	var media database.MediaObject
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.mediaID).Take(&media).Error
	}); err != nil {
		t.Fatal(err)
	}
	if media.ObjectKey == "" {
		t.Fatal("original video source was removed before discarded-frame re-extraction")
	}
	source, err := h.Store.Client.Get(ctx, h.Store.Bucket, media.ObjectKey)
	if err != nil {
		t.Fatalf("original source cannot be re-extracted: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("close original source stream: %v", err)
	}
}
