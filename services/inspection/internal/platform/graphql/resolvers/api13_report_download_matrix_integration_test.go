//go:build integration

package resolvers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/media/read_video_evidence"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/testfailure"

	"github.com/99designs/gqlgen/graphql/handler"
)

type failingPresignClient struct{ objectstore.Client }

func (failingPresignClient) PresignGet(context.Context, string, string, time.Duration) (string, error) {
	return "", errors.New("presign endpoint unavailable")
}

func api13Query(snapshotID identity.ID) string {
	return `query { reportDownload(snapshotId:"` + snapshotID.String() + `") { snapshotId kind status url sha256 } }`
}

// publishAPI13PDF records the completed PDF object for RPT1.
func publishAPI13PDF(t *testing.T, fx api11Fixture, kind string) {
	t.Helper()
	key := "vv-api13/" + fx.snapshotID.String() + "-" + kind + ".pdf"
	if err := fx.h.Store.Client.Put(context.Background(), fx.h.Store.Bucket, key, strings.NewReader("%PDF-1.7"), 8, "application/pdf"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fx.h.Store.Delete(context.Background(), key) })
	if err := fx.h.AdminDB.Create(&database.ReportArtifact{ID: identity.NewID(), TenantID: fx.seed.TenantID, SnapshotID: fx.snapshotID, Kind: kind, ObjectKey: key, SHA256: strings.Repeat("d", 64), Status: "READY", CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestVV_IT_506ReportDownloadExposesPDFOnlyAfterPublication(t *testing.T) {
	fx := newAPI11Fixture(t)
	pending := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api13Query(fx.snapshotID), fx.ctx, "", "")
	if strings.Contains(pending, `"errors"`) || !strings.Contains(pending, `"status":"PENDING"`) || !strings.Contains(pending, `"url":""`) {
		t.Fatalf("download before object publication = %s", pending)
	}
	publishAPI13PDF(t, fx, "PDF")
	ready := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api13Query(fx.snapshotID), fx.ctx, "", "")
	if strings.Contains(ready, `"errors"`) || !strings.Contains(ready, `"status":"READY"`) || strings.Contains(ready, `"url":""`) || !strings.Contains(ready, `"sha256":"`+strings.Repeat("d", 64)+`"`) {
		t.Fatalf("download after object publication = %s", ready)
	}
}

func TestVV_IT_301CustomerCannotDownloadUnpublishedRPT1(t *testing.T) {
	fx := newAPI11Fixture(t)
	unpublished := createAPI12Snapshot(t, fx)
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api13Query(unpublished), fx.customer, "", "")
	assertAPI11Rejected(t, response, "NOT_FOUND")
	if strings.Contains(response, "objectKey") || strings.Contains(response, "url") {
		t.Fatalf("unpublished report exposed download metadata: %s", response)
	}
}

func TestVV_IT_305ReportDownloadBeforePDFSnapshotArtifactIsPending(t *testing.T) {
	fx := newAPI11Fixture(t)
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api13Query(fx.snapshotID), fx.ctx, "", "")
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"status":"PENDING"`) || !strings.Contains(response, `"url":""`) || strings.Contains(response, `"status":"READY"`) {
		t.Fatalf("download without completed PDF must remain pending without an empty artifact: %s", response)
	}
}

func TestVV_IT_306PublishedPDFEvidenceRouteRefusesAfterAccessRevocation(t *testing.T) {
	fx := newAPI11Fixture(t)
	metadata, ok := requestctx.FromContext(fx.customer)
	if !ok {
		t.Fatal("customer fixture has no authenticated metadata")
	}
	mux := http.NewServeMux()
	if _, err := read_video_evidence.Setup(mux, read_video_evidence.Dependencies{DB: fx.h.DB, Store: fx.h.Store, Authorizer: auth.Authorizer{}, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return metadata, true }}); err != nil {
		t.Fatal(err)
	}
	if err := fx.h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id=? AND status='PUBLISHED'", fx.seed.TenantID, fx.snapshotID).Updates(map[string]any{"status": "INVALIDATED", "invalidated_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/media/video-evidence/"+fx.frameID.String()+"?snapshotId="+fx.snapshotID.String(), nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || response.Body.Len() != 0 {
		t.Fatalf("revoked PDF evidence route status=%d bytes=%d, want 404 with no media", response.Code, response.Body.Len())
	}
}

func TestVV_IT_303FailedPDFRenderIsNotExposedAsReady(t *testing.T) {
	fx := newAPI11Fixture(t)
	job := database.ReportPDFJob{ID: identity.NewID(), TenantID: fx.seed.TenantID, SnapshotID: fx.snapshotID, Audience: "PDF", RendererVersion: 1, Status: "FAILED", Attempts: 1, LastError: "document_invalid_or_limit_exceeded", NextAttemptAt: time.Now().UTC(), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := fx.h.AdminDB.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api13Query(fx.snapshotID), fx.ctx, "", "")
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"status":"FAILED"`) || !strings.Contains(response, `"url":""`) || strings.Contains(response, `"status":"READY"`) {
		t.Fatalf("failed renderer was exposed as a completed PDF: %s", response)
	}
}

func TestVV_IT_507ReportDownloadRequiresAuthentication(t *testing.T) {
	fx := newAPI11Fixture(t)
	assertAPI11Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api13Query(fx.snapshotID), context.Background(), "", ""), "UNAUTHENTICATED")
}

func TestVV_IT_508ReportDownloadRequiresReadCapability(t *testing.T) {
	fx := newAPI11Fixture(t)
	actor := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: fx.seed.TenantID, Roles: []string{auth.OrganizationAdmin}}, CorrelationID: "vv-it-508"})
	assertAPI11Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api13Query(fx.snapshotID), actor, "", ""), "FORBIDDEN")
}

func TestVV_IT_509ForeignReportDownloadIsNotFound(t *testing.T) {
	fx := newAPI11Fixture(t)
	foreign := newAPI11FixtureWithHarness(t, fx.h)
	publishAPI13PDF(t, foreign, "PDF")
	for name, actor := range map[string]context.Context{"internal": fx.ctx, "customer": fx.customer} {
		t.Run(name, func(t *testing.T) {
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api13Query(foreign.snapshotID), actor, "", "")
			assertAPI11Rejected(t, response, "NOT_FOUND")
			if strings.Contains(response, foreign.seed.TenantID.String()) || strings.Contains(response, "vv-api13/") {
				t.Fatalf("foreign download metadata leaked: %s", response)
			}
		})
	}
}

func TestVV_IT_510ReportDownloadAdapterUnavailable(t *testing.T) {
	assertAPI13Failure(t, "dependency", "DEPENDENCY_UNAVAILABLE")
}

func TestVV_IT_511ReportDownloadInternalErrorHidesSecret(t *testing.T) {
	assertAPI13Failure(t, "internal", "INTERNAL")
}

func TestVV_IT_513MissingStoreDependencyIsDependencyUnavailable(t *testing.T) {
	fx := newAPI11Fixture(t)
	publishAPI13PDF(t, fx, "PDF")
	store := objectstore.Store{Bucket: fx.h.Store.Bucket, Client: fx.h.Store.Client, PublicClient: failingPresignClient{Client: fx.h.Store.Client}}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: fx.h.DB, Bus: mediator.New(), Store: store}}))
	server.SetErrorPresenter(graph.PresentError)
	fx.server = server
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api13Query(fx.snapshotID), fx.ctx, "", "")
	assertAPI11Rejected(t, response, "DEPENDENCY_UNAVAILABLE")
	if strings.Contains(response, `"url"`) {
		t.Fatalf("unavailable store returned a download: %s", response)
	}
}

func TestVV_IT_514RevokedCustomerAccessIsNotFound(t *testing.T) {
	fx := newAPI11Fixture(t)
	publishAPI13PDF(t, fx, "PDF_CUSTOMER")
	if response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api13Query(fx.snapshotID), fx.customer, "", ""); strings.Contains(response, `"errors"`) || strings.Contains(response, `"url":""`) {
		t.Fatalf("published customer download unavailable: %s", response)
	}
	if err := fx.h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, fx.snapshotID).Updates(map[string]any{"status": "INVALIDATED", "invalidated_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api13Query(fx.snapshotID), fx.customer, "", "")
	assertAPI11Rejected(t, response, "NOT_FOUND")
	if strings.Contains(response, "vv-api13/") {
		t.Fatalf("revoked download leaked the object: %s", response)
	}
}

func assertAPI13Failure(t *testing.T, failure, code string) {
	t.Helper()
	fx := newAPI11Fixture(t)
	publishAPI13PDF(t, fx, "PDF")
	if err := testfailure.Install(fx.h.DB); err != nil {
		t.Fatal(err)
	}
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api13Query(fx.snapshotID), fx.ctx, "reportDownload", failure)
	assertAPI11Rejected(t, response, code)
	if strings.Contains(response, testfailure.Secret) || strings.Contains(response, "vv-api13/") {
		t.Fatalf("adapter error leaked internals: %s", response)
	}
}
