//go:build integration

package read_video_evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestVV_IT_165ExpiredSourceKeepsExactF11BytesAndProvenance(t *testing.T) {
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
	metadata := requestctx.Metadata{TenantID: a.TenantID, Principal: requestctx.Principal{IdentityID: a.ResponsibilityID, TenantID: a.TenantID, Internal: true}}
	mux := http.NewServeMux()
	if _, err := Setup(mux, Dependencies{DB: h.DB, Store: h.Store, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return metadata, true }}); err != nil {
		t.Fatal(err)
	}
	var before database.VideoFrame
	if err := h.AdminDB.Where("tenant_id=? AND id=?", a.TenantID, a.FrameID).Take(&before).Error; err != nil {
		t.Fatal(err)
	}
	// C1 expires: original bytes are gone and the source is EXPIRED.
	expiredAt := time.Now().UTC().Add(-time.Minute)
	if err := h.AdminDB.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", a.TenantID, a.MediaID).Updates(map[string]any{"status": "EXPIRED", "source_expiry_at": expiredAt, "original_deleted_at": expiredAt}).Error; err != nil {
		t.Fatal(err)
	}
	// Reopening RPT1 resolves F11 through the authenticated evidence route.
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/media/video-evidence/"+a.FrameID.String()+"?responsibilityId="+a.ResponsibilityID.String(), nil))
	digest := sha256.Sum256(response.Body.Bytes())
	if response.Code != http.StatusOK || hex.EncodeToString(digest[:]) != before.SHA256 || response.Header().Get("ETag") != `"`+before.SHA256+`"` {
		t.Fatalf("F11 after C1 expiry status=%d digest=%s etag=%s; want exact retained bytes %s", response.Code, hex.EncodeToString(digest[:]), response.Header().Get("ETag"), before.SHA256)
	}
	var after database.VideoFrame
	if err := h.AdminDB.Where("tenant_id=? AND id=?", a.TenantID, a.FrameID).Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.SourceMediaID != before.SourceMediaID || after.PreparationID != before.PreparationID || after.PTSMicros != before.PTSMicros || after.SHA256 != before.SHA256 || after.ObjectKey != before.ObjectKey {
		t.Fatalf("F11 provenance changed after expiry: before=%+v after=%+v", before, after)
	}
	// The source itself is no longer playable.
	source := httptest.NewRecorder()
	mux.ServeHTTP(source, httptest.NewRequest(http.MethodGet, "/media/videos/"+a.MediaID.String()+"/content?responsibilityId="+a.ResponsibilityID.String(), nil))
	if source.Code != http.StatusGone || source.Body.Len() != 0 {
		t.Fatalf("expired C1 playback status=%d bytes=%d; want 410 without bytes", source.Code, source.Body.Len())
	}
}
