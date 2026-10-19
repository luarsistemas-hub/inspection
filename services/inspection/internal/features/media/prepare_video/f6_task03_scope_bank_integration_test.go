//go:build integration

package prepare_video

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/video"
)

// labelledScopeGateway labels every frame with one environment and component.
func labelledScopeGateway(environment, component string) llm.Gateway {
	return scopeGatewayFunc(func(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
		observations := make([]video.ScopeObservation, 0, len(request.Images))
		for _, frame := range request.Images {
			observations = append(observations, video.ScopeObservation{FrameID: frame.EvidenceID, EnvironmentCandidate: environment, ComponentLabel: component, ViewLabel: "wide", Visibility: "VISIBLE", Landmarks: []string{}})
		}
		payload, err := json.Marshal(map[string]any{"observations": observations, "unresolvedFrameIds": []string{}})
		return llm.StructuredResult{JSON: payload}, err
	})
}

func prepareTask03Fixture(t *testing.T, kind, environment, component string) (*harness.Harness, preparationFixture) {
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
	fixture := seedPreparationFixture(t, h, syntheticH264Clip(t), "video/mp4", kind, "CAMERA")
	deferUnrelatedPreparationJobs(t, h, fixture, time.Now().UTC().Add(time.Hour))
	executor := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, time.Now)
	executor.deps.Gateway = labelledScopeGateway(environment, component)
	if worked, err := executor.RunDue(ctx); err != nil || !worked {
		t.Fatalf("RunDue = %t, %v", worked, err)
	}
	return h, fixture
}

func TestVV_IT_089CurrentOnlyKitchenScopeDerivesFromItsCurrentFrames(t *testing.T) {
	h, fixture := prepareTask03Fixture(t, "CURRENT", "kitchen", "sink")
	var job database.VideoPreparationJob
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	var frames []database.VideoFrame
	if err := h.AdminDB.Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Find(&frames).Error; err != nil {
		t.Fatal(err)
	}
	var scope []database.VideoScopeItem
	if err := h.AdminDB.Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Find(&scope).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != "READY" || len(frames) == 0 || len(scope) == 0 {
		t.Fatalf("current-only preparation=%s frames=%d scope=%d", job.Status, len(frames), len(scope))
	}
	own := map[string]database.VideoFrame{}
	for _, frame := range frames {
		own[frame.ID.String()] = frame
	}
	for _, item := range scope {
		frame, ok := own[item.FrameID.String()]
		if !ok || frame.SourceMediaID != fixture.mediaID || item.EnvironmentCandidate != "kitchen" || item.ComponentLabel != "sink" {
			t.Fatalf("scope item %+v is not derived from the current preparation's frames", item)
		}
	}
	var originItems int64
	if err := h.AdminDB.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND preparation_id<>?", fixture.tenantID, fixture.preparationID).Count(&originItems).Error; err != nil {
		t.Fatal(err)
	}
	if originItems != 0 {
		t.Fatalf("current-only scope pulled %d items from another preparation", originItems)
	}
}

func TestVV_IT_166UnchangedWallBankRetainsViewsWithoutFindings(t *testing.T) {
	h, fixture := prepareTask03Fixture(t, "ORIGIN", "living room", "wall")
	var frames []database.VideoFrame
	if err := h.AdminDB.Where("tenant_id=? AND preparation_id=? AND role='BASELINE'", fixture.tenantID, fixture.preparationID).Find(&frames).Error; err != nil {
		t.Fatal(err)
	}
	var wall int64
	if err := h.AdminDB.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND preparation_id=? AND component_label='wall'", fixture.tenantID, fixture.preparationID).Count(&wall).Error; err != nil {
		t.Fatal(err)
	}
	var findings int64
	if err := h.AdminDB.Model(&database.FindingRecord{}).Where("tenant_id=?", fixture.tenantID).Count(&findings).Error; err != nil {
		t.Fatal(err)
	}
	var job database.VideoPreparationJob
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != "READY" || job.ManifestDigest == "" || len(frames) == 0 || wall == 0 || findings != 0 {
		t.Fatalf("unchanged wall bank: preparation=%s manifest=%t baseline frames=%d wall scope=%d findings=%d; want a retained bank with no findings", job.Status, job.ManifestDigest != "", len(frames), wall, findings)
	}
	for _, frame := range frames {
		if frame.SHA256 == "" || frame.ObjectKey == "" {
			t.Fatalf("baseline wall view %s is not retained", frame.ID)
		}
	}
}
