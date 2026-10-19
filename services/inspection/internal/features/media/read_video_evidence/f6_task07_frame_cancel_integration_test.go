//go:build integration

package read_video_evidence

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"
)

// cancelAfterFirstReadClient serves one chunk, then cancels the request and
// blocks the transfer until its context ends.
type cancelAfterFirstReadClient struct {
	objectstore.Client
	cancel        context.CancelFunc
	reads, closes atomic.Int64
}

type cancelAfterFirstReadReader struct {
	ctx    context.Context
	client *cancelAfterFirstReadClient
	served bool
}

func (r *cancelAfterFirstReadReader) Read(p []byte) (int, error) {
	r.client.reads.Add(1)
	if !r.served {
		r.served = true
		r.client.cancel()
		return copy(p, []byte{0xff}), nil
	}
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func (r *cancelAfterFirstReadReader) Close() error {
	r.client.closes.Add(1)
	return nil
}

func (c *cancelAfterFirstReadClient) GetRange(ctx context.Context, _, _ string, _, _ int64) (io.ReadCloser, error) {
	return &cancelAfterFirstReadReader{ctx: ctx, client: c}, nil
}

func TestVV_IT_537CancelledFrameRequestClosesStorageReaderAndStops(t *testing.T) {
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
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	client := &cancelAfterFirstReadClient{Client: h.Store.Client, cancel: cancel}
	metadata := requestctx.Metadata{TenantID: a.TenantID, Principal: requestctx.Principal{IdentityID: a.ResponsibilityID, TenantID: a.TenantID, Internal: true}}
	mux := http.NewServeMux()
	if _, err := Setup(mux, Dependencies{DB: h.DB, Store: objectstore.Store{Bucket: h.Store.Bucket, Client: client}, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return metadata, true }}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/media/video-evidence/"+a.FrameID.String()+"?responsibilityId="+a.ResponsibilityID.String(), nil).WithContext(requestCtx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		mux.ServeHTTP(response, request)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled frame transfer did not stop")
	}
	if client.reads.Load() < 1 || client.closes.Load() != 1 {
		t.Fatalf("storage reader reads=%d closes=%d; want at least one read and exactly one close", client.reads.Load(), client.closes.Load())
	}
	if response.Body.Len() != 0 || response.Code == http.StatusOK {
		t.Fatalf("cancelled transfer still answered: status=%d bytes=%d", response.Code, response.Body.Len())
	}
}

// TestVV_IT_539UnpublishedCustomerSnapshotFrameIsNotFound records IT-539: the
// customer snapshot route answers 404 for an unpublished snapshot even when
// the frame UUID is known, without reading the object store.
func TestVV_IT_539UnpublishedCustomerSnapshotFrameIsNotFound(t *testing.T) {
	TestCustomerSnapshotFrameRequiresPublicationAndExactEvidence(t)
}
