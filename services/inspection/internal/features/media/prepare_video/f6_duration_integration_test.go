//go:build integration

package prepare_video

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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

func TestCurrentAttemptDurationBoundaryIT109(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })

	for _, tc := range []struct {
		name       string
		duration   string
		frameRate  string
		wantStatus string
		wantCode   string
	}{
		{name: "exactly 900 seconds", duration: "900.000", frameRate: "30", wantStatus: "READY"},
		{name: "900.001 seconds", duration: "900.001", frameRate: "1000", wantStatus: "FAILED", wantCode: "DURATION_EXCEEDED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "current-boundary.mp4")
			command := exec.CommandContext(ctx, "ffmpeg", "-y", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=black:s=16x16:r="+tc.frameRate+":d="+tc.duration, "-t", tc.duration, "-threads", "1", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-video_track_timescale", "1000000", path)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("generate %s-second current clip: %v: %s", tc.duration, err, output)
			}
			clip, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			fixture := seedPreparationFixture(t, h, clip, "video/mp4", "CURRENT", "CAMERA")
			deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
			executor, err := Setup(Dependencies{
				DB: h.DB, ClaimDB: h.AdminDB, Store: objectstore.VideoStore{Store: h.Store}, Decoder: video.FFmpeg{Threads: 1},
				Detector: sensitivecontent.NewDisabledDetector(), Gateway: scopeGatewayFunc(func(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
					observations := make([]video.ScopeObservation, 0, len(request.Images))
					for _, frame := range request.Images {
						observations = append(observations, video.ScopeObservation{FrameID: frame.EvidenceID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", Landmarks: []string{}})
					}
					payload, err := json.Marshal(map[string]any{"observations": observations, "unresolvedFrameIds": []string{}})
					return llm.StructuredResult{JSON: payload}, err
				}),
				Now: time.Now, TempDir: os.TempDir(), Limits: video.WorkerConfig{Concurrency: 1, DecoderThreads: 1, MaxBytes: 1 << 30, ScratchBytes: 4 << 30, RequiredFreeBytes: 8 << 30, StepTimeout: 5 * time.Minute, LeaseDuration: 120 * time.Second, HeartbeatInterval: 30 * time.Second, PollInterval: 2 * time.Second, ShutdownTimeout: 30 * time.Second},
			})
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
				if job.Status != tc.wantStatus || job.ErrorCode != tc.wantCode {
					t.Errorf("duration %s produced status=%s error=%s; want status=%s error=%s", tc.duration, job.Status, job.ErrorCode, tc.wantStatus, tc.wantCode)
				}
				if tc.duration == "900.000" {
					var source database.VideoSource
					if err := tx.Where("tenant_id=? AND media_id=?", fixture.tenantID, fixture.mediaID).Take(&source).Error; err != nil {
						return err
					}
					if source.DurationMicros != 900_000_000 {
						t.Errorf("server measured exact-boundary current duration as %d microseconds", source.DurationMicros)
					}
					var scopes []database.VideoScopeItem
					if err := tx.Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Find(&scopes).Error; err != nil {
						return err
					}
					if len(scopes) == 0 {
						t.Error("900-second current-only video produced no observed scope")
					}
					for _, scope := range scopes {
						if scope.EnvironmentCandidate != "kitchen" || scope.ComponentLabel != "sink" {
							t.Errorf("current-only scope claimed unobserved coverage: environment=%q component=%q", scope.EnvironmentCandidate, scope.ComponentLabel)
						}
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPrepareOriginRejectsActualDurationAboveFrozenLimitIT036(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	path := filepath.Join(t.TempDir(), "origin-over-limit.mp4")
	command := exec.CommandContext(ctx, "ffmpeg", "-y", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=black:s=16x16:r=1000:d=900.001", "-t", "900.001", "-threads", "1", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-video_track_timescale", "1000000", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate 900.001-second origin clip: %v: %s", err, output)
	}
	clip, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := seedPreparationFixture(t, h, clip, "video/mp4", "ORIGIN", "CAMERA")
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	executor, err := Setup(Dependencies{
		DB: h.DB, ClaimDB: h.AdminDB, Store: objectstore.VideoStore{Store: h.Store}, Decoder: video.FFmpeg{Threads: 1},
		Detector: sensitivecontent.NewDisabledDetector(), Gateway: scopeGatewayFunc(func(context.Context, llm.StructuredRequest) (llm.StructuredResult, error) {
			t.Error("over-limit clip reached scope analysis")
			return llm.StructuredResult{}, nil
		}), Now: time.Now, TempDir: os.TempDir(),
		Limits: video.WorkerConfig{Concurrency: 1, DecoderThreads: 1, MaxBytes: 1 << 30, ScratchBytes: 4 << 30, RequiredFreeBytes: 8 << 30, StepTimeout: time.Minute, LeaseDuration: 120 * time.Second, HeartbeatInterval: 30 * time.Second, PollInterval: 2 * time.Second, ShutdownTimeout: 30 * time.Second},
	})
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
		if job.Status != "FAILED" || job.ErrorCode != "DURATION_EXCEEDED" {
			t.Errorf("origin over frozen duration was not rejected: status=%s error=%s", job.Status, job.ErrorCode)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestVV_IT_243SupplementDurationAboveFrozen900IsRejected(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	path := filepath.Join(t.TempDir(), "supplement-over-limit.mp4")
	command := exec.CommandContext(ctx, "ffmpeg", "-y", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=black:s=16x16:r=1000:d=900.001", "-t", "900.001", "-threads", "1", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-video_track_timescale", "1000000", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate 900.001-second supplement clip: %v: %s", err, output)
	}
	clip, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := seedPreparationFixture(t, h, clip, "video/mp4", "SUPPLEMENT", "CAMERA")
	requestID := markPreparationAsSupplement(t, h, fixture)
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	executor, err := Setup(Dependencies{DB: h.DB, ClaimDB: h.AdminDB, Store: objectstore.VideoStore{Store: h.Store}, Decoder: video.FFmpeg{Threads: 1}, Detector: sensitivecontent.NewDisabledDetector(), Gateway: scopeGatewayFunc(func(context.Context, llm.StructuredRequest) (llm.StructuredResult, error) {
		t.Error("over-limit supplement reached scope analysis")
		return llm.StructuredResult{}, nil
	}), Now: time.Now, TempDir: os.TempDir(), Limits: video.WorkerConfig{Concurrency: 1, DecoderThreads: 1, MaxBytes: 1 << 30, ScratchBytes: 4 << 30, RequiredFreeBytes: 8 << 30, StepTimeout: time.Minute, LeaseDuration: 120 * time.Second, HeartbeatInterval: 30 * time.Second, PollInterval: 2 * time.Second, ShutdownTimeout: 30 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := executor.RunDue(ctx); err != nil || !worked {
		t.Fatalf("RunDue = %t, %v", worked, err)
	}
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		var attempt database.VideoAttempt
		if err := tx.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Order("created_at DESC").Take(&attempt).Error; err != nil {
			return err
		}
		var request database.RecaptureRequest
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, requestID).Take(&request).Error; err != nil {
			return err
		}
		if attempt.Kind != "SUPPLEMENT" || attempt.MaxDurationSeconds != 900 || job.Status != "FAILED" || job.ErrorCode != "DURATION_EXCEEDED" || request.Status != "REQUESTED" {
			t.Errorf("supplement over frozen limit: kind=%s frozen=%d job=%s/%s RC1=%s", attempt.Kind, attempt.MaxDurationSeconds, job.Status, job.ErrorCode, request.Status)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
