//go:build integration

package read_video_evidence

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestVV_IT_215ResponsibilityScopedReaderCannotOpenAnotherResponsibilityGap(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	// R2 owns the gap frame; the reader is limited to responsibility R1.
	r2, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	var r2Responsibility database.Responsibility
	if err := h.AdminDB.Where("tenant_id=? AND id=?", r2.TenantID, r2.ResponsibilityID).Take(&r2Responsibility).Error; err != nil {
		t.Fatal(err)
	}
	r1 := identity.NewID()
	now := time.Now().UTC()
	if err := h.AdminDB.Create(&database.Responsibility{ID: r1, TenantID: r2.TenantID, InspectionID: r2Responsibility.InspectionID, ParticipantID: r2Responsibility.ParticipantID, Status: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	reader := requestctx.Metadata{TenantID: r2.TenantID, Principal: requestctx.Principal{IdentityID: r1, TenantID: r2.TenantID, Internal: true}}
	mux := http.NewServeMux()
	if _, err := Setup(mux, Dependencies{DB: h.DB, Store: h.Store, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return reader, true }}); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"R2 gap frame by R2":  "/media/video-evidence/" + r2.FrameID.String() + "?responsibilityId=" + r2.ResponsibilityID.String(),
		"R2 gap frame via R1": "/media/video-evidence/" + r2.FrameID.String() + "?responsibilityId=" + r1.String(),
		"R2 source by R2":     "/media/videos/" + r2.MediaID.String() + "/content?responsibilityId=" + r2.ResponsibilityID.String(),
		"R2 source via R1":    "/media/videos/" + r2.MediaID.String() + "/content?responsibilityId=" + r1.String(),
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			body := response.Body.String()
			if response.Code != http.StatusNotFound || response.Body.Len() != 0 || strings.Contains(body, "sink") || strings.Contains(body, "kitchen") {
				t.Fatalf("R1 reader opened R2 evidence: status=%d body=%q", response.Code, body)
			}
		})
	}
}
