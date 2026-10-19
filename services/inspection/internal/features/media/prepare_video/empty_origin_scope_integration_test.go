//go:build integration

package prepare_video

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	origincore "inspection/services/inspection/internal/features/origins/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/sensitivecontent"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

func TestEmptyBlackVideoOriginScopeRequiresReplacementIT050(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	path := filepath.Join(t.TempDir(), "black.mp4")
	command := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=black:s=96x64:r=5:d=1", "-threads", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate black.mp4: %v: %s", err, output)
	}
	clip, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := seedPreparationFixture(t, h, clip, "video/mp4", "ORIGIN", "CAMERA")
	executor, err := Setup(Dependencies{
		DB: h.DB, ClaimDB: h.AdminDB, Store: objectstore.VideoStore{Store: h.Store}, Decoder: video.FFmpeg{Threads: 1},
		Detector: sensitivecontent.NewDisabledDetector(), Gateway: scopeGatewayFunc(func(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
			if len(request.Images) == 0 {
				t.Error("scope discovery received no retained frame")
			}
			unresolved := make([]string, 0, len(request.Images))
			for _, frame := range request.Images {
				unresolved = append(unresolved, frame.EvidenceID)
			}
			payload, err := json.Marshal(map[string]any{"observations": []any{}, "unresolvedFrameIds": unresolved})
			return llm.StructuredResult{JSON: payload}, err
		}), Now: time.Now, TempDir: os.TempDir(),
		Limits: video.WorkerConfig{Concurrency: 1, DecoderThreads: 1, MaxBytes: 1 << 30, ScratchBytes: 4 << 30, RequiredFreeBytes: 8 << 30, StepTimeout: time.Minute, LeaseDuration: 120 * time.Second, HeartbeatInterval: 30 * time.Second, PollInterval: 2 * time.Second, ShutdownTimeout: 30 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	worked, err := executor.RunDue(ctx)
	if err != nil || !worked {
		t.Fatalf("prepare black origin: worked=%t err=%v", worked, err)
	}
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
			return err
		}
		var scopeCount int64
		if err := tx.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Count(&scopeCount).Error; err != nil {
			return err
		}
		if job.Status != "FAILED" || job.ErrorCode != "NO_USABLE_SCOPE" || job.ManifestDigest != "" || scopeCount != 0 {
			t.Errorf("empty origin scope was not rejected coherently: status=%s code=%s digest=%q scope=%d", job.Status, job.ErrorCode, job.ManifestDigest, scopeCount)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err = (origincore.Service{DB: h.DB, Within: h.WithinTenant}).ActivateCompleted(ctx, fixture.tenantID, fixture.responsibilityID)
	code, _, safe := apperror.Public(err)
	if code != apperror.InvalidState || !strings.Contains(safe, "replacement") {
		t.Fatalf("empty prepared origin activation = %v, want INVALID_STATE with replacement guidance", err)
	}
}
