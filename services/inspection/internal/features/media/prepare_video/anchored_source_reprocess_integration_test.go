//go:build integration

package prepare_video

import (
	"context"
	"os"
	"testing"
	"time"

	managepreparation "inspection/services/inspection/internal/features/media/manage_video_preparation"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/video"
)

// TestReprocessingAnchoredSourceFinalizesWithoutExpiry is the ADR-019 regression
// for re-preparation: once an active baseline anchors the source (no
// source_expiry_at), a new preparation generation still seals as READY and the
// anchor is preserved instead of failing the readiness-anchor check.
func TestReprocessingAnchoredSourceFinalizesWithoutExpiry(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture := seedPreparationFixture(t, h, variedH264Clip(t), "video/mp4", "ORIGIN", "CAMERA")
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
		t.Fatalf("first preparation RunDue=%t err=%v", worked, err)
	}
	var source database.VideoSource
	if err := h.AdminDB.Where("tenant_id=? AND media_id=?", fixture.tenantID, fixture.mediaID).Take(&source).Error; err != nil || source.SourceExpiryAt == nil {
		t.Fatalf("first preparation did not assign the fixed expiry: %+v err=%v", source, err)
	}
	floor := *source.SourceExpiryAt
	if err := h.AdminDB.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", fixture.tenantID, fixture.mediaID).Updates(map[string]any{"retention_anchor": "ACTIVE_BASELINE", "source_expiry_at": nil, "retention_floor_at": floor}).Error; err != nil {
		t.Fatal(err)
	}
	var original database.VideoPreparationJob
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&original).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := managepreparation.Setup(managepreparation.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Within: h.WithinTenant}); err != nil {
		t.Fatal(err)
	}
	requestCtx := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.tenantID, Principal: requestctx.Principal{IdentityID: fixture.actorID, TenantID: fixture.tenantID, Roles: []string{auth.TenantAdmin}}})
	statusRaw, err := bus.Send(requestCtx, managepreparation.ReprocessCommand{TenantID: fixture.tenantID, MediaID: fixture.mediaID, ExpectedPreparationVersion: original.PreparationVersion, ProfileDigest: video.DefaultProfile().Digest(), MutationID: "anchored-reprocess"})
	if err != nil {
		t.Fatalf("reprocess anchored source: %v", err)
	}
	status := statusRaw.(managepreparation.Status)
	executor = checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, time.Now)
	executor.deps.FreeScratchBytes = func(string) (uint64, error) { return 9 << 30, nil }
	if worked, err := executor.RunDue(ctx); err != nil || !worked {
		t.Fatalf("anchored re-preparation RunDue=%t err=%v", worked, err)
	}
	var reprocessed database.VideoPreparationJob
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, status.PreparationID).Take(&reprocessed).Error; err != nil {
		t.Fatal(err)
	}
	if reprocessed.Status != "READY" || reprocessed.ErrorCode != "" {
		t.Fatalf("anchored re-preparation status=%s error=%q; want READY", reprocessed.Status, reprocessed.ErrorCode)
	}
	source = database.VideoSource{}
	if err := h.AdminDB.Where("tenant_id=? AND media_id=?", fixture.tenantID, fixture.mediaID).Take(&source).Error; err != nil {
		t.Fatal(err)
	}
	if source.RetentionAnchor != "ACTIVE_BASELINE" || source.SourceExpiryAt != nil || source.Status != "RETAINED" || source.RetentionFloorAt == nil || !source.RetentionFloorAt.Equal(floor) {
		t.Fatalf("re-preparation changed the anchor: anchor=%s expiry=%v status=%s floor=%v; want ACTIVE_BASELINE/nil/RETAINED/%v", source.RetentionAnchor, source.SourceExpiryAt, source.Status, source.RetentionFloorAt, floor)
	}
}
