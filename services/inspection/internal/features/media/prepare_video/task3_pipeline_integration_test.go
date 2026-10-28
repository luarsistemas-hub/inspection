//go:build integration

package prepare_video

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

func TestUT151UT152EnvironmentClipPreparationUsesFixedEnvironmentAndPersistsFrameCounts(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	key := "env:" + identity.NewID().String()
	fixture := seedPreparationFixture(t, h, syntheticH264Clip(t), "video/mp4", "CURRENT", "CAMERA", key)
	deferUnrelatedPreparationJobs(t, h, fixture, time.Now().UTC().Add(time.Hour))
	const environment = "Sala de estar 7"
	var captured []llm.StructuredRequest
	executor := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, time.Now)
	executor.deps.FreeScratchBytes = func(string) (uint64, error) { return 9 << 30, nil }
	if err := h.AdminDB.Model(&database.VideoPreparationJob{}).Where("id=?", fixture.preparationID).Update("profile_digest", video.DefaultProfile().Digest()).Error; err != nil {
		t.Fatal(err)
	}
	executor.deps.Gateway = scopeGatewayFunc(func(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
		captured = append(captured, request)
		observations := make([]video.ScopeObservation, 0, len(request.Images))
		for _, frame := range request.Images {
			observations = append(observations, video.ScopeObservation{FrameID: frame.EvidenceID, EnvironmentCandidate: "model-discovered", ComponentLabel: "window", ViewLabel: "wide", Visibility: "VISIBLE", Landmarks: []string{}})
		}
		payload, err := json.Marshal(map[string]any{"observations": observations, "unresolvedFrameIds": []string{}})
		return llm.StructuredResult{JSON: payload}, err
	})
	worked, err := executor.RunDue(ctx)
	if err != nil || !worked {
		t.Fatalf("RunDue=%t err=%v", worked, err)
	}
	if len(captured) == 0 {
		t.Fatal("executor did not request VIDEO_SCOPE from the LLM stub")
	}
	for _, request := range captured {
		if request.Mode != "VIDEO_SCOPE" || strings.Contains(string(request.JSONSchema), "environmentCandidate") || strings.Contains(request.UserPrompt, environment) {
			t.Fatalf("fixed environment was sent through discovery: mode=%q schema=%s prompt=%q", request.Mode, request.JSONSchema, request.UserPrompt)
		}
	}
	var job database.VideoPreparationJob
	var scope []database.VideoScopeItem
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Find(&scope).Error
	}); err != nil {
		t.Fatal(err)
	}
	if job.Status != "READY" || job.FramesSampled <= 0 || job.FramesRetained <= 0 || job.FramesRepresentative <= 0 {
		t.Fatalf("UT-152 frame counters/status: status=%s sampled=%d retained=%d representative=%d", job.Status, job.FramesSampled, job.FramesRetained, job.FramesRepresentative)
	}
	if len(scope) == 0 {
		t.Fatal("executor persisted no scope items")
	}
	for _, item := range scope {
		if item.EnvironmentCandidate != environment {
			t.Fatalf("UT-151 scope environment=%q; want requirement value %q", item.EnvironmentCandidate, environment)
		}
	}
}

type noFrameDecoderUT146 struct{}

func (noFrameDecoderUT146) Probe(context.Context, string, video.Limits) (video.Probe, error) {
	return video.Probe{Container: "mp4", Codec: "h264", Duration: time.Second, Width: 96, Height: 64, FrameRate: 25, VideoTracks: 1}, nil
}

func (noFrameDecoderUT146) Decode(context.Context, string, video.Limits, func(video.Frame) error) error {
	return nil
}

func (noFrameDecoderUT146) Playback(context.Context, string, string, video.Limits) error { return nil }

func TestPreparationFailsWithNoFramesCodeUT146(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture := seedPreparationFixture(t, h, syntheticH264Clip(t), "video/mp4", "CURRENT", "CAMERA")
	deferUnrelatedPreparationJobs(t, h, fixture, time.Now().UTC().Add(time.Hour))
	executor := checkpointExecutor(t, h, noFrameDecoderUT146{}, time.Now)
	executor.deps.FreeScratchBytes = func(string) (uint64, error) { return 9 << 30, nil }
	if worked, err := executor.RunDue(ctx); err != nil || !worked {
		t.Fatalf("RunDue=%t err=%v", worked, err)
	}
	var job database.VideoPreparationJob
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error
	}); err != nil {
		t.Fatal(err)
	}
	if job.Status != "FAILED" || job.ErrorCode != "NO_FRAMES" || job.ManifestDigest != "" {
		t.Fatalf("empty frame bank preparation status=%s code=%q manifest=%q; want FAILED/NO_FRAMES without a manifest", job.Status, job.ErrorCode, job.ManifestDigest)
	}
}
