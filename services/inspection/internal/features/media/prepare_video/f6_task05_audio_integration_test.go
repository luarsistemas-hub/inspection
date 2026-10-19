//go:build integration

package prepare_video

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/video"
)

func syntheticClipWithAudio(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audio.mp4")
	command := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=5:duration=1", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-threads", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate audio fixture: %v: %s", err, output)
	}
	clip, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return clip
}

func TestVV_IT_136ImportedAudioVideoSendsOnlyImagesToTheGateway(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture := seedPreparationFixture(t, h, syntheticClipWithAudio(t), "video/mp4", "CURRENT", "FILE")
	deferUnrelatedPreparationJobs(t, h, fixture, time.Now().UTC().Add(time.Hour))
	var mu sync.Mutex
	var images []llm.NormalizedImage
	executor := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, time.Now)
	scope := successfulScopeGateway()
	executor.deps.Gateway = scopeGatewayFunc(func(ctx context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
		mu.Lock()
		images = append(images, request.Images...)
		mu.Unlock()
		return scope.CompleteStructured(ctx, request)
	})
	if worked, err := executor.RunDue(ctx); err != nil || !worked {
		t.Fatalf("RunDue = %t, %v", worked, err)
	}
	var source database.VideoSource
	if err := h.AdminDB.Where("tenant_id=? AND media_id=?", fixture.tenantID, fixture.mediaID).Take(&source).Error; err != nil {
		t.Fatal(err)
	}
	var job database.VideoPreparationJob
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.preparationID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	if !source.AudioPresent || job.Status != "READY" {
		t.Fatalf("imported audio source audio=%t preparation=%s; want audio detected and READY", source.AudioPresent, job.Status)
	}
	if len(images) == 0 {
		t.Fatal("gateway received no images")
	}
	for _, image := range images {
		if !strings.HasPrefix(image.DataURL, "data:image/jpeg;base64,") || strings.Contains(image.DataURL, "audio") {
			t.Fatalf("gateway received non-image content %.40q", image.DataURL)
		}
	}
	// Every retained frame (the input of later condition requests) is a JPEG still.
	var frames []database.VideoFrame
	if err := h.AdminDB.Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.preparationID).Find(&frames).Error; err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("no retained frames")
	}
	for _, frame := range frames {
		reader, err := h.Store.Client.Get(ctx, h.Store.Bucket, frame.ObjectKey)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}) {
			t.Fatalf("retained frame %s is not a JPEG still", frame.ID)
		}
	}
}
