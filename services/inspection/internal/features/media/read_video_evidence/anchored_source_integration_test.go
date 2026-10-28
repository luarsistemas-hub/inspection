//go:build integration

package read_video_evidence

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"inspection/services/inspection/internal/platform/database"
)

// TestAnchoredSourceRemainsPlayableWithoutExpiry is the ADR-019 regression for
// playback: a source anchored by an active baseline has no source_expiry_at and
// must stay readable through GET and HEAD instead of being treated as expired.
func TestAnchoredSourceRemainsPlayableWithoutExpiry(t *testing.T) {
	p := newTask07Playback(t)
	if err := p.h.AdminDB.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", p.a.TenantID, p.a.MediaID).Updates(map[string]any{"retention_anchor": "ACTIVE_BASELINE", "source_expiry_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
	if got := p.seek(context.Background(), "bytes=0-9"); got.Code != http.StatusPartialContent || !bytes.Equal(got.Body.Bytes(), p.source[:10]) {
		t.Fatalf("anchored GET status=%d body=%q; want 206 with source bytes", got.Code, got.Body.Bytes())
	}
	head := func() int {
		response := httptest.NewRecorder()
		p.mux.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/media/videos/"+p.a.MediaID.String()+"/content?responsibilityId="+p.a.ResponsibilityID.String(), nil))
		return response.Code
	}
	if code := head(); code != http.StatusOK {
		t.Fatalf("anchored HEAD status=%d; want 200", code)
	}
	if err := p.h.AdminDB.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", p.a.TenantID, p.a.MediaID).Updates(map[string]any{"retention_anchor": "FIXED", "source_expiry_at": time.Now().UTC().Add(-time.Minute)}).Error; err != nil {
		t.Fatal(err)
	}
	// The fixture's ACTIVE origin version still references this source. A passed
	// FIXED expiry therefore must not revoke playback.
	if code := head(); code != http.StatusOK {
		t.Fatalf("expired FIXED source referenced by active baseline HEAD status=%d; want 200", code)
	}
}
