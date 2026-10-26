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
)

// TestTask07InventoryReviewFrameAuthorizedThroughOriginVersion covers the link
// returned by inventoryDraft: a retained frame is readable through the origin
// version that cites it, only by internal readers of the asset's unit.
func TestTask07InventoryReviewFrameAuthorizedThroughOriginVersion(t *testing.T) {
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
	var asset database.Asset
	if err := h.AdminDB.Where("tenant_id=? AND id=?", a.TenantID, a.AssetID).Take(&asset).Error; err != nil {
		t.Fatal(err)
	}
	// The origin version carries an inventory whose item cites the frame; a
	// second frame of the same source stays outside the inventory.
	now := time.Now().UTC().Truncate(time.Microsecond)
	environmentID, itemID, uncitedFrameID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", a.TenantID, a.OriginVersionID).Updates(map[string]any{"has_inventory": true, "purpose": "MOVE_IN"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Create(&database.InventoryEnvironment{ID: environmentID, TenantID: a.TenantID, OriginVersionID: a.OriginVersionID, IdentityID: identity.NewID(), TypeCode: "LIVING_ROOM", Name: "Sala", Expected: true, Decision: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Create(&database.InventoryItem{ID: itemID, TenantID: a.TenantID, OriginVersionID: a.OriginVersionID, EnvironmentID: environmentID, IdentityID: identity.NewID(), TypeCode: "SOFA", Name: "Sofá", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	frameID := a.FrameID
	if err := h.AdminDB.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: a.TenantID, ItemID: itemID, Kind: "VIDEO_FRAME", MediaID: a.MediaID, FrameID: &frameID, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	var cited database.VideoFrame
	if err := h.AdminDB.Where("tenant_id=? AND id=?", a.TenantID, a.FrameID).Take(&cited).Error; err != nil {
		t.Fatal(err)
	}
	uncited := cited
	uncited.ID, uncited.PTSMicros, uncited.ObjectKey = uncitedFrameID, cited.PTSMicros+1000, cited.ObjectKey+".uncited"
	if err := h.AdminDB.Create(&uncited).Error; err != nil {
		t.Fatal(err)
	}

	scoped := func(roles []string, scope requestctx.Scope) requestctx.Principal {
		return requestctx.Principal{IdentityID: identity.NewID(), MembershipID: identity.NewID(), TenantID: a.TenantID, ProductEntitlements: []string{auth.DashboardProduct}, Roles: roles, Scopes: []requestctx.Scope{scope}}
	}
	member := func(roles []string, unit identity.ID) requestctx.Principal {
		return scoped(roles, requestctx.Scope{Kind: "BUSINESS_UNIT", ID: unit})
	}
	cases := []struct {
		name      string
		principal requestctx.Principal
		frame     identity.ID
		snapshot  identity.ID
		status    int
	}{
		{"reviewer of the asset unit", member([]string{auth.Manager}, asset.BusinessUnitID), a.FrameID, a.OriginVersionID, http.StatusOK},
		{"internal capture session", requestctx.Principal{IdentityID: identity.NewID(), TenantID: a.TenantID, Internal: true}, a.FrameID, a.OriginVersionID, http.StatusNotFound},
		{"reviewer scoped to the asset only", scoped([]string{auth.Employee}, requestctx.Scope{Kind: "ASSET", ID: a.AssetID}), a.FrameID, a.OriginVersionID, http.StatusOK},
		{"reviewer outside the asset unit", member([]string{auth.Manager}, identity.NewID()), a.FrameID, a.OriginVersionID, http.StatusNotFound},
		{"viewer of the asset unit on an activated version", member([]string{auth.Viewer}, asset.BusinessUnitID), a.FrameID, a.OriginVersionID, http.StatusOK},
		{"customer viewer before the item is included", member([]string{auth.CustomerViewer}, asset.BusinessUnitID), a.FrameID, a.OriginVersionID, http.StatusNotFound},
		{"frame not cited by the inventory", member([]string{auth.Manager}, asset.BusinessUnitID), uncitedFrameID, a.OriginVersionID, http.StatusNotFound},
		{"unknown version", member([]string{auth.Manager}, asset.BusinessUnitID), a.FrameID, identity.NewID(), http.StatusNotFound},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			metadata := requestctx.Metadata{TenantID: a.TenantID, Principal: item.principal}
			mux := http.NewServeMux()
			if _, err := Setup(mux, Dependencies{DB: h.DB, Store: h.Store, Authorizer: auth.Authorizer{}, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return metadata, true }}); err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/media/video-evidence/"+item.frame.String()+"?snapshotId="+item.snapshot.String(), nil))
			if response.Code != item.status {
				t.Fatalf("status=%d, want %d", response.Code, item.status)
			}
			if item.status != http.StatusOK && response.Body.Len() != 0 {
				t.Fatalf("denied request leaked %d bytes", response.Body.Len())
			}
		})
	}

	// Once the item is included in the active version the customer sees its frame.
	if err := h.AdminDB.Model(&database.InventoryItem{}).Where("tenant_id=? AND id=?", a.TenantID, itemID).Update("decision", "ACCEPTED").Error; err != nil {
		t.Fatal(err)
	}
	customer := requestctx.Metadata{TenantID: a.TenantID, Principal: member([]string{auth.CustomerViewer}, asset.BusinessUnitID)}
	customerMux := http.NewServeMux()
	if _, err := Setup(customerMux, Dependencies{DB: h.DB, Store: h.Store, Authorizer: auth.Authorizer{}, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return customer, true }}); err != nil {
		t.Fatal(err)
	}
	included := httptest.NewRecorder()
	customerMux.ServeHTTP(included, httptest.NewRequest(http.MethodGet, "/media/video-evidence/"+a.FrameID.String()+"?snapshotId="+a.OriginVersionID.String(), nil))
	if included.Code != http.StatusOK {
		t.Fatalf("customer frame of an included active item status=%d, want 200", included.Code)
	}

	// After the source expires the retained frame remains readable for review.
	expiredAt := now.Add(-time.Minute)
	if err := h.AdminDB.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", a.TenantID, a.MediaID).Updates(map[string]any{"status": "EXPIRED", "source_expiry_at": expiredAt, "original_deleted_at": expiredAt}).Error; err != nil {
		t.Fatal(err)
	}
	metadata := requestctx.Metadata{TenantID: a.TenantID, Principal: member([]string{auth.Manager}, asset.BusinessUnitID)}
	mux := http.NewServeMux()
	if _, err := Setup(mux, Dependencies{DB: h.DB, Store: h.Store, Authorizer: auth.Authorizer{}, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return metadata, true }}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/media/video-evidence/"+a.FrameID.String()+"?snapshotId="+a.OriginVersionID.String(), nil))
	if response.Code != http.StatusOK || response.Body.Len() == 0 {
		t.Fatalf("expired-source review frame status=%d bytes=%d; want preserved frame", response.Code, response.Body.Len())
	}
}
