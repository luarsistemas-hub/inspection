//go:build integration

package prepare_video

import (
	"context"
	"os"
	"testing"
	"time"

	origincore "inspection/services/inspection/internal/features/origins/core"
	resolvereference "inspection/services/inspection/internal/features/origins/resolve_reference"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/video"
)

func TestVV_IT_057InvalidatedDuringDiscoveryStaysInvalidatedAfterCompletion(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture := seedPreparationFixture(t, h, syntheticH264Clip(t), "video/mp4", "ORIGIN", "CAMERA")
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	var o1 database.OriginVersion
	if err := h.AdminDB.Where("tenant_id=? AND origin_id=?", fixture.tenantID, fixture.originID).Take(&o1).Error; err != nil {
		t.Fatal(err)
	}
	origins := origincore.Service{DB: h.DB}
	invalidated := false
	executor := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return fixture.now })
	scope := successfulScopeGateway()
	// The manager invalidates O1 while its preparation is in semantic discovery.
	executor.deps.Gateway = scopeGatewayFunc(func(ctx context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
		if !invalidated {
			var job database.VideoPreparationJob
			if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
				return llm.StructuredResult{}, err
			}
			// Semantic discovery runs after the frame bank is anchored and before
			// the preparation becomes READY.
			if job.Status != "PROCESSING" || job.Phase == "READY" {
				t.Errorf("O1 invalidated with status=%s phase=%s, want in-flight discovery", job.Status, job.Phase)
			}
			if _, err := origins.Invalidate(context.Background(), fixture.tenantID, o1.ID); err != nil {
				return llm.StructuredResult{}, err
			}
			invalidated = true
		}
		return scope.CompleteStructured(ctx, request)
	})
	if worked, err := executor.RunDue(ctx); err != nil || !worked {
		t.Fatalf("RunDue = %t, %v", worked, err)
	}
	if !invalidated {
		t.Fatal("preparation never reached semantic discovery")
	}
	var job database.VideoPreparationJob
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != "READY" {
		t.Fatalf("preparation status=%s code=%s, want READY", job.Status, job.ErrorCode)
	}
	// Completion activation must refuse the invalidated version.
	if o1.ResponsibilityID == nil {
		t.Fatal("origin version lost its responsibility")
	}
	if _, err := origins.ActivateCompleted(ctx, fixture.tenantID, *o1.ResponsibilityID); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("completion activation of invalidated O1 err=%v, want INVALID_STATE", err)
	}
	var after database.OriginVersion
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, o1.ID).Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	var origin database.Origin
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.originID).Take(&origin).Error; err != nil {
		t.Fatal(err)
	}
	if after.Status != "INVALIDATED" || after.ActivatedAt != nil || origin.ActiveVersionID != nil {
		t.Fatalf("completion reactivated O1: status=%s activatedAt=%v active=%v", after.Status, after.ActivatedAt, origin.ActiveVersionID)
	}
	// O1 is not selectable as a reference for a new inspection.
	bus := mediator.New()
	if err := resolvereference.Setup(resolvereference.Dependencies{DB: h.DB, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Ask(ctx, resolvereference.Query{TenantID: fixture.tenantID, AssetID: origin.AssetID, MediaKind: "VIDEO"}); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("resolving the asset reference after O1 invalidation err=%v, want INVALID_INPUT (no active origin)", err)
	}
	if _, err := bus.Ask(ctx, resolvereference.Query{TenantID: fixture.tenantID, AssetID: origin.AssetID, VersionID: &o1.ID, MediaKind: "VIDEO"}); err == nil {
		t.Fatal("invalidated O1 was selectable by explicit version")
	}
}
