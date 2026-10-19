//go:build integration

package read_video_evidence

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

// pausingSourceClient pauses the first full source transfer after its first
// chunk until the test releases it.
type pausingSourceClient struct {
	objectstore.Client
	once    sync.Once
	paused  chan struct{}
	release chan struct{}
}

type pausingReader struct {
	io.ReadCloser
	client *pausingSourceClient
	first  bool
}

func (r *pausingReader) Read(p []byte) (int, error) {
	if !r.first {
		r.first = true
		n, err := r.ReadCloser.Read(p[:min(len(p), 10)])
		r.client.once.Do(func() { close(r.client.paused) })
		return n, err
	}
	<-r.client.release
	return r.ReadCloser.Read(p)
}

func (c *pausingSourceClient) Get(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	reader, err := c.Client.Get(ctx, bucket, key)
	if err != nil {
		return nil, err
	}
	return &pausingReader{ReadCloser: reader, client: c}, nil
}

type task07SourceFixture struct {
	h      *harness.Harness
	origin harness.VideoOriginFixture
	body   []byte
	path   string
	now    time.Time
}

func newTask07SourceFixture(t *testing.T) task07SourceFixture {
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
	origin, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte("0123456789"), 10)
	now := time.Now().UTC().Truncate(time.Microsecond)
	objectKey := "tenant/" + origin.TenantID.String() + "/media/" + origin.MediaID.String()
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, objectKey, bytes.NewReader(body), int64(len(body)), "video/mp4"); err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, origin.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", origin.TenantID, origin.MediaID).Updates(map[string]any{"status": "RETAINED", "source_expiry_at": now.Add(time.Hour), "screening_status": "APPROVED"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	return task07SourceFixture{h: h, origin: origin, body: body, path: "/media/videos/" + origin.MediaID.String() + "/content?responsibilityId=" + origin.ResponsibilityID.String(), now: now}
}

func (f task07SourceFixture) request(mux *http.ServeMux, rangeHeader string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, f.path, nil)
	if rangeHeader != "" {
		request.Header.Set("Range", rangeHeader)
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func TestVV_IT_550ActiveStreamFinishesButNextSeekAfterExpiryIs410(t *testing.T) {
	f := newTask07SourceFixture(t)
	client := &pausingSourceClient{Client: f.h.Store.Client, paused: make(chan struct{}), release: make(chan struct{})}
	metadata := requestctx.Metadata{TenantID: f.origin.TenantID, Principal: requestctx.Principal{IdentityID: f.origin.ResponsibilityID, TenantID: f.origin.TenantID, Internal: true}}
	mux := http.NewServeMux()
	if _, err := Setup(mux, Dependencies{DB: f.h.DB, Store: objectstore.Store{Bucket: f.h.Store.Bucket, Client: client}, Now: time.Now, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return metadata, true }}); err != nil {
		t.Fatal(err)
	}
	streamed := make(chan *httptest.ResponseRecorder, 1)
	go func() { streamed <- f.request(mux, "") }()
	select {
	case <-client.paused:
	case <-time.After(5 * time.Second):
		t.Fatal("authorized stream did not start")
	}
	// The source expires while the authorized stream holds its lease.
	expiredAt := time.Now().UTC()
	if err := f.h.AdminDB.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", f.origin.TenantID, f.origin.MediaID).Updates(map[string]any{"status": "EXPIRED", "original_deleted_at": expiredAt, "source_expiry_at": expiredAt}).Error; err != nil {
		t.Fatal(err)
	}
	close(client.release)
	first := <-streamed
	if first.Code != http.StatusOK || !bytes.Equal(first.Body.Bytes(), f.body) {
		t.Fatalf("active stream status=%d bytes=%d; want the complete source", first.Code, first.Body.Len())
	}
	if next := f.request(mux, "bytes=10-19"); next.Code != http.StatusGone {
		t.Fatalf("seek after expiry status=%d, want 410", next.Code)
	}
}

func TestVV_IT_607EverySeekRechecksSessionAndTenantWithoutURLCredential(t *testing.T) {
	f := newTask07SourceFixture(t)
	foreign, err := f.h.SeedVideoOriginFixture(context.Background(), harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	var authCalls atomic.Int64
	var mu sync.Mutex
	current := requestctx.Metadata{TenantID: f.origin.TenantID, Principal: requestctx.Principal{IdentityID: f.origin.ResponsibilityID, TenantID: f.origin.TenantID, Internal: true}}
	authenticated := true
	mux := http.NewServeMux()
	if _, err := Setup(mux, Dependencies{DB: f.h.DB, Store: f.h.Store, Now: func() time.Time { return f.now }, Authenticate: func(*http.Request) (requestctx.Metadata, bool) {
		authCalls.Add(1)
		mu.Lock()
		defer mu.Unlock()
		return current, authenticated
	}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.path, "token") || strings.Contains(f.path, "signature") || strings.Contains(f.path, "X-Amz") {
		t.Fatalf("playback URL carries a credential: %s", f.path)
	}
	if first := f.request(mux, "bytes=0-9"); first.Code != http.StatusPartialContent || first.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("first seek status=%d cache=%q", first.Code, first.Header().Get("Cache-Control"))
	}
	mu.Lock()
	authenticated = false
	mu.Unlock()
	if ended := f.request(mux, "bytes=10-19"); ended.Code != http.StatusUnauthorized {
		t.Fatalf("seek after session end status=%d, want 401", ended.Code)
	}
	mu.Lock()
	authenticated = true
	current = requestctx.Metadata{TenantID: foreign.TenantID, Principal: requestctx.Principal{IdentityID: foreign.ResponsibilityID, TenantID: foreign.TenantID, Internal: true}}
	mu.Unlock()
	if switched := f.request(mux, "bytes=20-29"); switched.Code != http.StatusNotFound {
		t.Fatalf("seek under another tenant status=%d, want 404", switched.Code)
	}
	if authCalls.Load() != 3 {
		t.Fatalf("authentication ran %d times for 3 seeks", authCalls.Load())
	}
}
