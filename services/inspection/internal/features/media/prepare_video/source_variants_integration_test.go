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
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/sensitivecontent"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

func TestCameraFileAndOriginUseDurablePreparationIT047IT133(t *testing.T) {
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
	var cameraDigest string
	var cameraDuration int64
	var cameraFrames int64
	for _, tc := range []struct{ name, kind, acquisition string }{
		{name: "CAMERA current", kind: "CURRENT", acquisition: "CAMERA"},
		{name: "FILE current", kind: "CURRENT", acquisition: "FILE"},
		{name: "O1 without current inspection", kind: "ORIGIN", acquisition: "CAMERA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := seedPreparationFixture(t, h, clip, "video/mp4", tc.kind, tc.acquisition)
			gateway := scopeGatewayFunc(func(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
				observations := make([]video.ScopeObservation, 0, len(request.Images))
				for _, frame := range request.Images {
					observations = append(observations, video.ScopeObservation{FrameID: frame.EvidenceID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", Landmarks: []string{}})
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
				var source database.VideoSource
				if err := tx.Where("tenant_id=? AND media_id=?", fixture.tenantID, fixture.mediaID).Take(&source).Error; err != nil {
					return err
				}
				var frames int64
				if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND source_media_id=?", fixture.tenantID, fixture.mediaID).Count(&frames).Error; err != nil {
					return err
				}
				if job.Status != "READY" || job.ManifestDigest == "" || job.SourceSHA256 == "" || source.DurationMicros <= 0 || source.FirstFrameReadyAt == nil || source.SourceExpiryAt == nil || frames == 0 {
					t.Errorf("incomplete preparation: status=%s manifest=%q source=%q duration=%d frames=%d", job.Status, job.ManifestDigest, job.SourceSHA256, source.DurationMicros, frames)
				}
				if tc.acquisition == "CAMERA" && tc.kind == "CURRENT" {
					cameraDigest, cameraDuration, cameraFrames = job.SourceSHA256, source.DurationMicros, frames
				}
				if tc.acquisition == "FILE" && (job.SourceSHA256 != cameraDigest || source.DurationMicros != cameraDuration || frames != cameraFrames) {
					t.Errorf("FILE diverged from CAMERA: digest=%q duration=%d frames=%d", job.SourceSHA256, source.DurationMicros, frames)
				}
				if tc.kind == "ORIGIN" {
					var linkedInspection int64
					if err := tx.Model(&database.Responsibility{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.responsibilityID).Count(&linkedInspection).Error; err != nil {
						return err
					}
					var version database.OriginVersion
					if err := tx.Where("tenant_id=? AND origin_id=?", fixture.tenantID, fixture.originID).Take(&version).Error; err != nil {
						return err
					}
					if linkedInspection != 0 || version.VideoPreparationID == nil || *version.VideoPreparationID != fixture.preparationID || source.RetentionDays == nil || *source.RetentionDays != 30 {
						t.Errorf("origin frame bank requires no current inspection: linked=%d versionPrep=%v retention=%v", linkedInspection, version.VideoPreparationID, source.RetentionDays)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
