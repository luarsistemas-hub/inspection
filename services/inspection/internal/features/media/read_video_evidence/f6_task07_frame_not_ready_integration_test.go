//go:build integration

package read_video_evidence

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestVV_IT_174FrameBeforePreparationIsReadyReturns409(t *testing.T) {
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
	get := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/media/video-evidence/"+a.FrameID.String()+"?responsibilityId="+a.ResponsibilityID.String(), nil))
		return response
	}
	setPreparation := func(status, phase, screening string) {
		t.Helper()
		if err := h.AdminDB.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", a.TenantID, a.PreparationID).Updates(map[string]any{"status": status, "phase": phase}).Error; err != nil {
			t.Fatal(err)
		}
		if err := h.AdminDB.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", a.TenantID, a.MediaID).Update("screening_status", screening).Error; err != nil {
			t.Fatal(err)
		}
	}
	for name, state := range map[string][3]string{
		"extracting": {"PROCESSING", "EXTRACTING", "APPROVED"},
		"unscreened": {"READY", "READY", "PENDING"},
	} {
		t.Run(name, func(t *testing.T) {
			setPreparation(state[0], state[1], state[2])
			if response := get(); response.Code != http.StatusConflict || response.Body.Len() != 0 {
				t.Fatalf("F01 before preparation is screened/ready status=%d bytes=%d; want 409 without an image", response.Code, response.Body.Len())
			}
		})
	}
	setPreparation("READY", "READY", "APPROVED")
	if response := get(); response.Code != http.StatusOK || response.Body.Len() == 0 {
		t.Fatalf("ready F01 status=%d bytes=%d", response.Code, response.Body.Len())
	}
}
