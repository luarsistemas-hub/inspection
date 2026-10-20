//go:build integration

package read_video_evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

type api16FailingFrameStore struct {
	objectstore.Client
	fail bool
}

func (s *api16FailingFrameStore) GetRange(ctx context.Context, bucket, key string, start, end int64) (io.ReadCloser, error) {
	if s.fail {
		return nil, context.DeadlineExceeded
	}
	return s.Client.(objectstore.RangeGetter).GetRange(ctx, bucket, key, start, end)
}

func TestVV_IT_170_532_533_534_535_536_538FrameHTTPContract(t *testing.T) {
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
	b, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	var frame database.VideoFrame
	if err := h.WithinTenant(ctx, a.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", a.TenantID, a.FrameID).Take(&frame).Error
	}); err != nil {
		t.Fatal(err)
	}
	metadata := requestctx.Metadata{TenantID: a.TenantID, Principal: requestctx.Principal{IdentityID: a.ResponsibilityID, TenantID: a.TenantID, Internal: true}}
	store := &api16FailingFrameStore{Client: h.Store.Client}
	mux := http.NewServeMux()
	if _, err := Setup(mux, Dependencies{DB: h.DB, Store: objectstore.Store{Bucket: h.Store.Bucket, Client: store}, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return metadata, true }}); err != nil {
		t.Fatal(err)
	}
	url := "/media/video-evidence/" + a.FrameID.String() + "?responsibilityId=" + a.ResponsibilityID.String()
	do := func(method, path string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(method, path, nil))
		return response
	}

	get := do(http.MethodGet, url)
	digest := sha256.Sum256(get.Body.Bytes())
	if get.Code != http.StatusOK || get.Header().Get("Content-Type") != "image/jpeg" || get.Header().Get("ETag") != `"`+frame.SHA256+`"` || get.Header().Get("Content-Length") != stringInt(frame.SizeBytes) || hex.EncodeToString(digest[:]) != frame.SHA256 {
		t.Fatalf("authorized GET status=%d headers=%v bodyDigest=%s; want 200 image/jpeg, recorded digest and exact length", get.Code, get.Header(), hex.EncodeToString(digest[:]))
	}
	head := do(http.MethodHead, url)
	if head.Code != http.StatusOK || head.Header().Get("Content-Type") != get.Header().Get("Content-Type") || head.Header().Get("ETag") != get.Header().Get("ETag") || head.Header().Get("Content-Length") != get.Header().Get("Content-Length") || head.Body.Len() != 0 {
		t.Fatalf("authorized HEAD status=%d headers=%v body=%d; GET headers=%v", head.Code, head.Header(), head.Body.Len(), get.Header())
	}

	unauthMux := http.NewServeMux()
	if _, err := Setup(unauthMux, Dependencies{DB: h.DB, Store: h.Store, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return requestctx.Metadata{}, false }}); err != nil {
		t.Fatal(err)
	}
	unauth := httptest.NewRecorder()
	unauthMux.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, url, nil))
	if unauth.Code != http.StatusUnauthorized || unauth.Body.Len() != 0 {
		t.Fatalf("unauthenticated GET status=%d body=%q; want 401 and no bytes", unauth.Code, unauth.Body.String())
	}

	foreignPath := "/media/video-evidence/" + b.FrameID.String() + "?responsibilityId=" + a.ResponsibilityID.String()
	foreign, unknown := do(http.MethodGet, foreignPath), do(http.MethodGet, "/media/video-evidence/"+identity.NewID().String()+"?responsibilityId="+a.ResponsibilityID.String())
	if foreign.Code != http.StatusNotFound || unknown.Code != http.StatusNotFound || foreign.Body.String() != unknown.Body.String() {
		t.Fatalf("foreign frame status/body=%d/%q, unknown=%d/%q; want indistinguishable 404", foreign.Code, foreign.Body.String(), unknown.Code, unknown.Body.String())
	}
	foreignMetadata := do(http.MethodHead, foreignPath)
	if foreignMetadata.Code != http.StatusNotFound || foreignMetadata.Body.Len() != 0 {
		t.Fatalf("foreign frame metadata status=%d body=%q; want 404 without metadata", foreignMetadata.Code, foreignMetadata.Body.String())
	}

	if err := h.WithinTenant(ctx, a.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", a.TenantID, a.MediaID).Update("screening_status", "BLOCKED").Error
	}); err != nil {
		t.Fatal(err)
	}
	blocked := do(http.MethodGet, url)
	if blocked.Code != http.StatusConflict || blocked.Body.Len() != 0 {
		t.Fatalf("blocked screening status=%d body=%q; want 409 without bytes", blocked.Code, blocked.Body.String())
	}
	if err := h.WithinTenant(ctx, a.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", a.TenantID, a.MediaID).Update("screening_status", "APPROVED").Error
	}); err != nil {
		t.Fatal(err)
	}
	store.fail = true
	failed := do(http.MethodGet, url)
	if failed.Code != http.StatusServiceUnavailable || failed.Body.Len() != 0 {
		t.Fatalf("store failure status=%d body=%q; want 503 before image bytes", failed.Code, failed.Body.String())
	}
	noPurpose := do(http.MethodGet, "/media/video-evidence/"+a.FrameID.String())
	if noPurpose.Code != http.StatusNotFound || noPurpose.Body.Len() != 0 {
		t.Fatalf("frame without report/capture purpose status=%d body=%q; want 404", noPurpose.Code, noPurpose.Body.String())
	}
}

func stringInt(value int64) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
