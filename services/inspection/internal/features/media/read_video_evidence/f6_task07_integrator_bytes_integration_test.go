//go:build integration

package read_video_evidence

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

type integratorBytesFailingStore struct{ objectstore.Client }

type integratorBytesCountingStore struct {
	objectstore.Client
	reads int
}

func (s *integratorBytesCountingStore) Get(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	s.reads++
	return s.Client.Get(ctx, bucket, key)
}

func (s *integratorBytesCountingStore) GetRange(ctx context.Context, bucket, key string, start, end int64) (io.ReadCloser, error) {
	s.reads++
	return s.Client.(objectstore.RangeGetter).GetRange(ctx, bucket, key, start, end)
}

func (integratorBytesFailingStore) Get(context.Context, string, string) (io.ReadCloser, error) {
	return nil, errors.New("storage backend failed qa-secret-123")
}

func (integratorBytesFailingStore) GetRange(context.Context, string, string, int64, int64) (io.ReadCloser, error) {
	return nil, errors.New("storage backend failed qa-secret-123")
}

func TestVV_IT_339IntegratorFrameAndSourceBytesKeepRoleAndInspectionScope(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", fixture.TenantID, fixture.MediaID).Updates(map[string]any{"status": "RETAINED", "source_expiry_at": now.Add(time.Hour), "screening_status": "APPROVED"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	sourceBytes := []byte("authorized integrator source bytes")
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, "tenant/"+fixture.TenantID.String()+"/media/"+fixture.MediaID.String(), strings.NewReader(string(sourceBytes)), int64(len(sourceBytes)), "video/mp4"); err != nil {
		t.Fatal(err)
	}
	var membership database.Membership
	if err := h.AdminDB.Where("tenant_id=?", fixture.TenantID).Take(&membership).Error; err != nil {
		t.Fatal(err)
	}
	var inspection database.Inspection
	if err := h.AdminDB.Where("tenant_id=? AND asset_id=?", fixture.TenantID, fixture.AssetID).Take(&inspection).Error; err != nil {
		t.Fatal(err)
	}
	metadata := requestctx.Metadata{TenantID: fixture.TenantID, Principal: requestctx.Principal{
		IdentityID: membership.IdentityID, MembershipID: membership.ID, TenantID: fixture.TenantID,
		Roles: []string{auth.Manager}, Scopes: []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: inspection.BusinessUnitID}},
	}}
	newMux := func(principal requestctx.Metadata) *http.ServeMux {
		mux := http.NewServeMux()
		if _, err := Setup(mux, Dependencies{DB: h.DB, Store: h.Store, Authorizer: auth.Authorizer{}, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return principal, true }}); err != nil {
			t.Fatal(err)
		}
		return mux
	}
	request := func(mux *http.ServeMux, principal requestctx.Metadata, path string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(requestctx.WithMetadata(req.Context(), principal))
		mux.ServeHTTP(response, req)
		return response
	}
	mux := newMux(metadata)
	frame := request(mux, metadata, "/media/video-evidence/"+fixture.FrameID.String()+"?responsibilityId="+fixture.ResponsibilityID.String())
	source := request(mux, metadata, "/media/videos/"+fixture.MediaID.String()+"/content?responsibilityId="+fixture.ResponsibilityID.String())
	if frame.Code != http.StatusOK || frame.Body.Len() == 0 {
		t.Fatalf("in-scope frame read status/body=%d/%d; want 200 with bytes", frame.Code, frame.Body.Len())
	}
	if source.Code != http.StatusOK || source.Body.Len() == 0 {
		t.Fatalf("in-scope source read status/body=%d/%d; want 200 with bytes", source.Code, source.Body.Len())
	}
	// A valid tenant principal without the integrator capability cannot use the
	// bytes routes, even when its tenant and responsibility IDs are otherwise valid.
	unauthorized := metadata
	unauthorized.Principal.Internal = false
	unauthorized.Principal.Roles = []string{auth.Viewer}
	unauthorized.Principal.Scopes = nil
	blocked := request(newMux(unauthorized), unauthorized, "/media/video-evidence/"+fixture.FrameID.String()+"?responsibilityId="+fixture.ResponsibilityID.String())
	if blocked.Code != http.StatusForbidden && blocked.Code != http.StatusNotFound {
		t.Fatalf("viewer read frame: status=%d; want 403/404", blocked.Code)
	}
	outside := metadata
	outside.Principal.Internal = false
	outside.Principal.Roles = []string{auth.Manager}
	outside.Principal.Scopes = []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: identity.NewID()}}
	blocked = request(newMux(outside), outside, "/media/videos/"+fixture.MediaID.String()+"/content?responsibilityId="+fixture.ResponsibilityID.String())
	if blocked.Code != http.StatusForbidden && blocked.Code != http.StatusNotFound {
		t.Fatalf("manager outside inspection scope read source: status=%d; want 403/404", blocked.Code)
	}
}

func TestVV_IT_342IntegratorByteStorageFailureIsGeneric(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", fixture.TenantID, fixture.MediaID).Updates(map[string]any{"status": "RETAINED", "source_expiry_at": now.Add(time.Hour), "screening_status": "APPROVED"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, "tenant/"+fixture.TenantID.String()+"/media/"+fixture.MediaID.String(), strings.NewReader("source-bytes"), int64(len("source-bytes")), "video/mp4"); err != nil {
		t.Fatal(err)
	}
	metadata := requestctx.Metadata{TenantID: fixture.TenantID, Principal: requestctx.Principal{IdentityID: fixture.ResponsibilityID, TenantID: fixture.TenantID, Internal: true}}
	mux := http.NewServeMux()
	if _, err := Setup(mux, Dependencies{DB: h.DB, Store: objectstore.Store{Bucket: h.Store.Bucket, Client: integratorBytesFailingStore{Client: h.Store.Client}}, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return metadata, true }}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		"/media/video-evidence/" + fixture.FrameID.String() + "?responsibilityId=" + fixture.ResponsibilityID.String(),
		"/media/videos/" + fixture.MediaID.String() + "/content?responsibilityId=" + fixture.ResponsibilityID.String(),
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "qa-secret-123") || response.Body.Len() != 0 {
			t.Fatalf("byte storage failure for %s returned status/body=%d/%q; want generic 503 without body or secret", target, response.Code, response.Body.String())
		}
	}
}

func TestVV_IT_346ForeignTenantFrameAndSourceIDsAreHiddenBeforeStorageRead(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	owner, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	store := &integratorBytesCountingStore{Client: h.Store.Client}
	// Tenant B (foreign fixture) submits tenant A's frame and source IDs.
	metadata := requestctx.Metadata{TenantID: foreign.TenantID, Principal: requestctx.Principal{IdentityID: foreign.ResponsibilityID, TenantID: foreign.TenantID, Internal: true}}
	mux := http.NewServeMux()
	if _, err := Setup(mux, Dependencies{DB: h.DB, Store: objectstore.Store{Bucket: h.Store.Bucket, Client: store}, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return metadata, true }}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		"/media/video-evidence/" + owner.FrameID.String() + "?responsibilityId=" + foreign.ResponsibilityID.String(),
		"/media/videos/" + owner.MediaID.String() + "/content?responsibilityId=" + foreign.ResponsibilityID.String(),
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusNotFound || response.Body.Len() != 0 {
			t.Fatalf("foreign ID %s returned status/body=%d/%q; want indistinguishable 404", target, response.Code, response.Body.String())
		}
	}
	if store.reads != 0 {
		t.Fatalf("foreign frame/source identifiers caused %d storage reads; want zero", store.reads)
	}
}
