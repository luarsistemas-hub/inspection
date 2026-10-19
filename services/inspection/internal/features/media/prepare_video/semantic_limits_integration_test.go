//go:build integration

package prepare_video

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/sensitivecontent"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

func TestSemanticScopeRejectsExcessAndOversizedLabelsIT623(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	clip := syntheticH264Clip(t)
	for _, tc := range []struct {
		name, expectedCode string
		excess             bool
	}{
		{name: "201 components", expectedCode: "RESOURCE_LIMIT", excess: true},
		{name: "121 Unicode code points", expectedCode: "MODEL_OUTPUT_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := seedPreparationFixture(t, h, clip, "video/mp4", "CURRENT", "CAMERA")
			gateway := scopeGatewayFunc(func(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
				observations := make([]video.ScopeObservation, 0, 201)
				frameID := request.Images[0].EvidenceID
				if tc.excess {
					for i := 0; i < 201; i++ {
						observations = append(observations, video.ScopeObservation{FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", Landmarks: []string{}})
					}
				} else {
					observations = append(observations, video.ScopeObservation{FrameID: frameID, EnvironmentCandidate: strings.Repeat("á", 121), ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", Landmarks: []string{}})
				}
				payload, err := json.Marshal(map[string]any{"observations": observations, "unresolvedFrameIds": []string{}})
				return llm.StructuredResult{JSON: payload}, err
			})
			executor, err := Setup(Dependencies{DB: h.DB, ClaimDB: h.AdminDB, Store: objectstore.VideoStore{Store: h.Store}, Decoder: video.FFmpeg{Threads: 1}, Detector: sensitivecontent.NewDisabledDetector(), Gateway: gateway, Now: time.Now, TempDir: os.TempDir(), Limits: video.WorkerConfig{Concurrency: 1, DecoderThreads: 1, MaxBytes: 1 << 30, ScratchBytes: 4 << 30, RequiredFreeBytes: 8 << 30, StepTimeout: time.Minute, LeaseDuration: 120 * time.Second, HeartbeatInterval: 30 * time.Second, PollInterval: 2 * time.Second, ShutdownTimeout: 30 * time.Second}})
			if err != nil {
				t.Fatal(err)
			}
			worked, err := executor.RunDue(ctx)
			if err != nil || !worked {
				t.Fatalf("RunDue = %t, %v", worked, err)
			}
			if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
				var job database.VideoPreparationJob
				if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
					return err
				}
				var scope, events int64
				if err := tx.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Count(&scope).Error; err != nil {
					return err
				}
				if err := tx.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='media.video_prepared.v1' AND correlation_id=?", fixture.tenantID, fixture.preparationID.String()).Count(&events).Error; err != nil {
					return err
				}
				if job.Status != "FAILED" || job.ErrorCode != tc.expectedCode || job.ManifestDigest != "" || scope != 0 || events != 0 {
					t.Errorf("oversized scope committed: status=%s code=%s digest=%q scope=%d events=%d", job.Status, job.ErrorCode, job.ManifestDigest, scope, events)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
