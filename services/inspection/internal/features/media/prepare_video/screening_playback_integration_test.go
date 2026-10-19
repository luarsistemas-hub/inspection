//go:build integration

package prepare_video

import (
	"context"
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"inspection/services/inspection/internal/features/media/read_video_evidence"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/sensitivecontent"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

type pauseAtHalfSecondModel struct {
	seen    int
	entered chan struct{}
	release chan struct{}
}

func (m *pauseAtHalfSecondModel) Bytes() []byte { return []byte("qa-pause-at-half-second") }

func (m *pauseAtHalfSecondModel) Detect(ctx context.Context, _ image.Image) ([]sensitivecontent.Region, error) {
	m.seen++
	if m.seen == 16 {
		close(m.entered)
		select {
		case <-m.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, nil
}

func TestSourcePlaybackReturnsConflictDuringPendingScreeningIT142IT144(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	path := filepath.Join(t.TempDir(), "screening.mp4")
	command := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=30:duration=1", "-threads", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate screening clip: %v: %s", err, output)
	}
	clip, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := seedPreparationFixture(t, h, clip, "video/mp4", "CURRENT", "CAMERA")
	deferUnrelatedPreparationJobs(t, h, fixture, fixture.now.Add(48*time.Hour))
	model := &pauseAtHalfSecondModel{entered: make(chan struct{}), release: make(chan struct{})}
	executor := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, func() time.Time { return fixture.now })
	executor.deps.Detector = sensitivecontent.Detector{Model: model}
	done := make(chan error, 1)
	go func() { _, err := executor.RunDue(ctx); done <- err }()
	select {
	case <-model.entered:
	case <-time.After(20 * time.Second):
		t.Fatal("screening did not reach PTS 500ms")
	}
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var source database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=?", fixture.tenantID, fixture.mediaID).Take(&source).Error; err != nil {
			return err
		}
		if source.ScreeningStatus != "PENDING" || source.FirstFrameReadyAt != nil {
			t.Errorf("screening prematurely completed: status=%s anchor=%v", source.ScreeningStatus, source.FirstFrameReadyAt)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	metadata := requestctx.Metadata{TenantID: fixture.tenantID, Principal: requestctx.Principal{IdentityID: fixture.responsibilityID, TenantID: fixture.tenantID, Internal: true, Roles: []string{auth.Employee}}}
	mux := http.NewServeMux()
	if _, err := read_video_evidence.Setup(mux, read_video_evidence.Dependencies{DB: h.DB, Store: h.Store, Authorizer: auth.Authorizer{}, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return metadata, true }}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/media/videos/"+fixture.mediaID.String()+"/content?responsibilityId="+fixture.responsibilityID.String(), nil)
	request = request.WithContext(requestctx.WithMetadata(request.Context(), metadata))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || response.Body.Len() != 0 {
		t.Errorf("pending source playback status=%d body-bytes=%d, want 409 with no video", response.Code, response.Body.Len())
	}
	close(model.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("screening completion: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("screening did not resume")
	}
}
