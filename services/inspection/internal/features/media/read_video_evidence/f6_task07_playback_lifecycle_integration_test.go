//go:build integration

package read_video_evidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
)

type task07Playback struct {
	h      *harness.Harness
	a      harness.VideoOriginFixture
	mux    *http.ServeMux
	source []byte
	frame  database.VideoFrame
}

func newTask07Playback(t *testing.T) task07Playback {
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
	a, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	p := task07Playback{h: h, a: a, source: bytes.Repeat([]byte("0123456789"), 10)}
	// Playback reads the VIDEO_PLAYBACK proxy, which the route prefers.
	proxyKey := "vv-task07-playback/" + a.MediaID.String() + ".mp4"
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, proxyKey, bytes.NewReader(p.source), int64(len(p.source)), "video/mp4"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Store.Delete(context.Background(), proxyKey) })
	sum := sha256.Sum256(p.source)
	if err := h.AdminDB.Create(&database.MediaDerivative{ID: identity.NewID(), TenantID: a.TenantID, MediaID: a.MediaID, ObjectKey: proxyKey, Kind: "VIDEO_PLAYBACK", SHA256: hex.EncodeToString(sum[:]), ContentType: "video/mp4", Width: 2, Height: 2, SizeBytes: int64(len(p.source)), CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().UTC().Add(24 * time.Hour)
	if err := h.AdminDB.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", a.TenantID, a.MediaID).Updates(map[string]any{"status": "RETAINED", "source_expiry_at": expiry}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Where("tenant_id=? AND id=?", a.TenantID, a.FrameID).Take(&p.frame).Error; err != nil {
		t.Fatal(err)
	}
	metadata := requestctx.Metadata{TenantID: a.TenantID, Principal: requestctx.Principal{IdentityID: a.ResponsibilityID, TenantID: a.TenantID, Internal: true}}
	p.mux = http.NewServeMux()
	if _, err := Setup(p.mux, Dependencies{DB: h.DB, Store: h.Store, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return metadata, true }}); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p task07Playback) seek(ctx context.Context, rangeHeader string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/media/videos/"+p.a.MediaID.String()+"/content?responsibilityId="+p.a.ResponsibilityID.String(), nil).WithContext(ctx)
	request.Header.Set("Range", rangeHeader)
	response := httptest.NewRecorder()
	p.mux.ServeHTTP(response, request)
	return response
}

func (p task07Playback) assertFrameVisible(t *testing.T, stage string) {
	t.Helper()
	response := httptest.NewRecorder()
	p.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/media/video-evidence/"+p.a.FrameID.String()+"?responsibilityId="+p.a.ResponsibilityID.String(), nil))
	sum := sha256.Sum256(response.Body.Bytes())
	if response.Code != http.StatusOK || hex.EncodeToString(sum[:]) != p.frame.SHA256 {
		t.Fatalf("%s: frame status=%d digest=%s; want the retained still %s", stage, response.Code, hex.EncodeToString(sum[:]), p.frame.SHA256)
	}
}

func TestVV_IT_288SourceExpiringWhileReportOpenReturns410AndFrameStaysVisible(t *testing.T) {
	p := newTask07Playback(t)
	if first := p.seek(context.Background(), "bytes=0-9"); first.Code != http.StatusPartialContent || !bytes.Equal(first.Body.Bytes(), p.source[:10]) {
		t.Fatalf("seek before expiry status=%d body=%q; want 206 with the first bytes", first.Code, first.Body.Bytes())
	}
	p.assertFrameVisible(t, "before expiry")
	expired := time.Now().UTC().Add(-time.Minute)
	if err := p.h.AdminDB.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", p.a.TenantID, p.a.MediaID).Updates(map[string]any{"status": "EXPIRED", "source_expiry_at": expired, "original_deleted_at": expired}).Error; err != nil {
		t.Fatal(err)
	}
	if next := p.seek(context.Background(), "bytes=50-59"); next.Code != http.StatusGone || next.Body.Len() != 0 {
		t.Fatalf("seek after expiry status=%d bytes=%d; want 410 without media", next.Code, next.Body.Len())
	}
	p.assertFrameVisible(t, "after expiry")
}

func TestVV_IT_289InterruptedPlaybackRetryResumesAndKeepsStills(t *testing.T) {
	p := newTask07Playback(t)
	// The client disconnects before the response is consumed.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if dropped := p.seek(ctx, "bytes=20-39"); dropped.Code == http.StatusPartialContent && bytes.Equal(dropped.Body.Bytes(), p.source[20:40]) {
		t.Log("server finished the range before observing the disconnect")
	}
	p.assertFrameVisible(t, "after disconnect")
	retry := p.seek(context.Background(), "bytes=20-39")
	if retry.Code != http.StatusPartialContent || retry.Header().Get("Content-Range") != "bytes 20-39/100" || !bytes.Equal(retry.Body.Bytes(), p.source[20:40]) {
		t.Fatalf("retry status=%d range=%q body=%q; want resumed 206 bytes 20-39", retry.Code, retry.Header().Get("Content-Range"), retry.Body.Bytes())
	}
	var leases int64
	if err := p.h.AdminDB.Model(&database.VideoSourceLease{}).Where("tenant_id=? AND source_media_id=?", p.a.TenantID, p.a.MediaID).Count(&leases).Error; err != nil {
		t.Fatal(err)
	}
	if leases != 0 {
		t.Fatalf("playback left %d source leases after the streams ended", leases)
	}
	p.assertFrameVisible(t, "after retry")
}
