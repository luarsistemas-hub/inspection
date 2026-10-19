//go:build integration

package read_video_evidence

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestVV_IT_540_541_542_543_544_545_546_547SourceHTTPContract(t *testing.T) {
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

	const contentType = "video/mp4"
	body := make([]byte, 100)
	for index := range body {
		body[index] = byte(index)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	expiresAt := now.Add(time.Hour)
	objectKey := "tenant/" + a.TenantID.String() + "/media/" + a.MediaID.String()
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, objectKey, bytes.NewReader(body), int64(len(body)), contentType); err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, a.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", a.TenantID, a.MediaID).Updates(map[string]any{"status": "RETAINED", "source_expiry_at": expiresAt, "screening_status": "APPROVED"}).Error
	}); err != nil {
		t.Fatal(err)
	}

	metadata := requestctx.Metadata{TenantID: a.TenantID, Principal: requestctx.Principal{IdentityID: a.ResponsibilityID, TenantID: a.TenantID, Internal: true}}
	newMux := func(authenticated bool, principal requestctx.Metadata) *http.ServeMux {
		mux := http.NewServeMux()
		_, setupErr := Setup(mux, Dependencies{DB: h.DB, Store: h.Store, Now: func() time.Time { return now }, Authenticate: func(*http.Request) (requestctx.Metadata, bool) {
			return principal, authenticated
		}})
		if setupErr != nil {
			t.Fatal(setupErr)
		}
		return mux
	}
	path := "/media/videos/" + a.MediaID.String() + "/content?responsibilityId=" + a.ResponsibilityID.String()
	do := func(mux *http.ServeMux, method, target, rangeHeader string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, nil)
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		return response
	}
	mux := newMux(true, metadata)

	get := do(mux, http.MethodGet, path, "")
	if get.Code != http.StatusOK || get.Header().Get("Content-Type") != contentType || get.Header().Get("Content-Length") != "100" || !bytes.Equal(get.Body.Bytes(), body) || get.Header().Get("Location") != "" || bytes.Contains(get.Body.Bytes(), []byte("X-Amz-Signature")) {
		t.Fatalf("source GET status=%d headers=%v body=%q; want exact 100 bytes, %s, no redirect or signed URL", get.Code, get.Header(), get.Body.Bytes(), contentType)
	}
	if get.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("source GET Accept-Ranges=%q; want bytes", get.Header().Get("Accept-Ranges"))
	}

	ranged := do(mux, http.MethodGet, path, "bytes=10-19")
	if ranged.Code != http.StatusPartialContent || ranged.Header().Get("Content-Range") != "bytes 10-19/100" || ranged.Header().Get("Accept-Ranges") != "bytes" || ranged.Header().Get("Content-Length") != "10" || !bytes.Equal(ranged.Body.Bytes(), body[10:20]) {
		t.Fatalf("range GET status=%d headers=%v body=%q; want 206 bytes 10-19/100 length 10 and exact bytes", ranged.Code, ranged.Header(), ranged.Body.Bytes())
	}

	head := do(mux, http.MethodHead, path, "")
	if head.Code != http.StatusOK || head.Header().Get("Content-Type") != contentType || head.Header().Get("Content-Length") != "100" || head.Header().Get("Accept-Ranges") != "bytes" || head.Body.Len() != 0 {
		t.Fatalf("source HEAD status=%d headers=%v body=%d; want source headers and no body", head.Code, head.Header(), head.Body.Len())
	}

	unauth := do(newMux(false, requestctx.Metadata{}), http.MethodGet, path, "")
	if unauth.Code != http.StatusUnauthorized || unauth.Body.Len() != 0 {
		t.Fatalf("unauthenticated source GET status=%d body=%q; want 401 without bytes", unauth.Code, unauth.Body.String())
	}
	foreignPath := "/media/videos/" + b.MediaID.String() + "/content?responsibilityId=" + a.ResponsibilityID.String()
	foreign := do(mux, http.MethodGet, foreignPath, "")
	unknown := do(mux, http.MethodGet, "/media/videos/"+identity.NewID().String()+"/content?responsibilityId="+a.ResponsibilityID.String(), "")
	inaccessibleMetadata := metadata
	inaccessibleMetadata.Principal.IdentityID = identity.NewID()
	inaccessible := do(newMux(true, inaccessibleMetadata), http.MethodGet, path, "")
	if foreign.Code != http.StatusNotFound || unknown.Code != http.StatusNotFound || inaccessible.Code != http.StatusNotFound {
		t.Fatalf("source access statuses: cross-tenant=%d unknown=%d inaccessible=%d; want all 404", foreign.Code, unknown.Code, inaccessible.Code)
	}

	if err := h.WithinTenant(ctx, a.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", a.TenantID, a.MediaID).Update("screening_status", "PENDING").Error
	}); err != nil {
		t.Fatal(err)
	}
	pending := do(mux, http.MethodGet, path, "")
	if pending.Code != http.StatusConflict || pending.Body.Len() != 0 {
		t.Fatalf("pending-screening GET status=%d body=%q; want 409 without bytes", pending.Code, pending.Body.String())
	}
	if err := h.WithinTenant(ctx, a.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", a.TenantID, a.MediaID).Update("screening_status", "APPROVED").Error
	}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"bytes=bad", "bytes=1-2,4-5", "bytes=100-", "bytes=10-9"} {
		t.Run("invalid_range_"+value, func(t *testing.T) {
			response := do(mux, http.MethodGet, path, value)
			if response.Code != http.StatusRequestedRangeNotSatisfiable || response.Header().Get("Content-Range") != "bytes */100" || response.Body.Len() != 0 {
				t.Fatalf("Range %q status=%d headers=%v body=%q; want 416 Content-Range bytes */100 and no body", value, response.Code, response.Header(), response.Body.String())
			}
		})
	}

	expiredAt := now.Add(-time.Hour)
	if err := h.WithinTenant(ctx, a.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", a.TenantID, a.MediaID).Updates(map[string]any{"status": "EXPIRED", "source_expiry_at": expiredAt, "screening_status": "APPROVED"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	expired := do(mux, http.MethodGet, path, "")
	framePath := "/media/video-evidence/" + a.FrameID.String() + "?responsibilityId=" + a.ResponsibilityID.String()
	retainedFrame := do(mux, http.MethodGet, framePath, "")
	if expired.Code != http.StatusGone || retainedFrame.Code != http.StatusOK || retainedFrame.Body.Len() == 0 {
		t.Fatalf("expired source status=%d; retained F11 status/body=%d/%d; want 410 and readable retained frame", expired.Code, retainedFrame.Code, retainedFrame.Body.Len())
	}
}
