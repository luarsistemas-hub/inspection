//go:build integration

package read_video_evidence

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestAudioAnchorInspectionScopeIT139ToIT151(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	mediaA, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	mediaOtherInspection, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	var inspectionID identity.ID
	otherInspectionID := identity.NewID()
	if err := h.WithinTenant(ctx, mediaA.TenantID, func(tx *gorm.DB) error {
		var responsibility database.Responsibility
		if err := tx.Where("tenant_id=? AND id=?", mediaA.TenantID, mediaA.ResponsibilityID).Take(&responsibility).Error; err != nil {
			return err
		}
		inspectionID = responsibility.InspectionID
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", mediaA.TenantID, inspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		inspection.ID = otherInspectionID
		inspection.SourceKey = "audio-anchor-other-inspection-" + otherInspectionID.String()
		inspection.Version = 1
		inspection.CreatedAt = time.Now().UTC()
		inspection.UpdatedAt = inspection.CreatedAt
		return tx.Create(&inspection).Error
	}); err != nil {
		t.Fatal(err)
	}
	operator := requestctx.Metadata{TenantID: mediaA.TenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: mediaA.TenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: auth.ProductsForRole(auth.TenantAdmin)}}
	denied := operator
	denied.Principal.IdentityID = identity.NewID()
	denied.Principal.Roles = []string{auth.CustomerViewer}
	denied.Principal.ProductEntitlements = auth.ProductsForRole(auth.CustomerViewer)
	setup := func(metadata requestctx.Metadata) *http.ServeMux {
		t.Helper()
		mux := http.NewServeMux()
		_, err := Setup(mux, Dependencies{DB: h.DB, Store: h.Store, Authorizer: auth.Authorizer{Scopes: auth.GORMScopeResolver{DB: h.DB}}, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return metadata, true }})
		if err != nil {
			t.Fatal(err)
		}
		return mux
	}
	request := func(mux *http.ServeMux, method, path string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(method, path, nil))
		return response
	}
	inspectionScope := "inspectionId=" + inspectionID.String()
	framePath := "/media/video-evidence/" + mediaA.FrameID.String() + "?" + inspectionScope
	sourcePath := "/media/videos/" + mediaA.MediaID.String() + "/content?" + inspectionScope
	operatorMux := setup(operator)
	if got := request(operatorMux, http.MethodGet, framePath); got.Code != http.StatusOK || got.Body.Len() == 0 {
		t.Fatalf("IT-139/IT-140 authorized operator frame status=%d bytes=%d", got.Code, got.Body.Len())
	}
	if got := request(operatorMux, http.MethodHead, sourcePath); got.Code == http.StatusNotFound {
		t.Fatalf("IT-140 authorized operator source was rejected by inspection scope: status=%d", got.Code)
	}
	deniedMux := setup(denied)
	for _, path := range []string{framePath, sourcePath} {
		if got := request(deniedMux, http.MethodGet, path); got.Code != http.StatusNotFound || got.Body.Len() != 0 {
			t.Errorf("IT-146 actor without media capability path=%s status=%d bytes=%d; want indistinguishable 404", path, got.Code, got.Body.Len())
		}
	}
	otherInspectionPath := "/media/video-evidence/" + mediaA.FrameID.String() + "?inspectionId=" + otherInspectionID.String()
	if got := request(operatorMux, http.MethodGet, otherInspectionPath); got.Code != http.StatusNotFound || got.Body.Len() != 0 {
		t.Errorf("IT-149 media from another inspection in the same tenant status=%d bytes=%d; want 404", got.Code, got.Body.Len())
	}
	foreignTenantPath := "/media/video-evidence/" + mediaOtherInspection.FrameID.String() + "?" + inspectionScope
	if got := request(operatorMux, http.MethodGet, foreignTenantPath); got.Code != http.StatusNotFound || got.Body.Len() != 0 {
		t.Errorf("IT-151 foreign tenant or unknown frame status=%d bytes=%d; want 404 without existence leak", got.Code, got.Body.Len())
	}
	if err := h.WithinTenant(ctx, mediaA.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", mediaA.TenantID, mediaA.MediaID).Updates(map[string]any{"status": "EXPIRED", "original_deleted_at": time.Now().UTC()}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if got := request(operatorMux, http.MethodGet, sourcePath); got.Code != http.StatusGone || got.Body.Len() != 0 {
		t.Errorf("IT-142/IT-240 expired source status=%d bytes=%d; want 410 without bytes", got.Code, got.Body.Len())
	}
	if got := request(operatorMux, http.MethodGet, framePath); got.Code != http.StatusOK || got.Body.Len() == 0 {
		t.Errorf("IT-142/IT-240 retained frame after source expiry status=%d bytes=%d", got.Code, got.Body.Len())
	}
}
