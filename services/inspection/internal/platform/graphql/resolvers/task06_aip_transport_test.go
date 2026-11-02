package resolvers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

// aipTransport is the GraphQL transport fixture of autonomous-inspection-platform
// task 06. It owns its database so these cases do not depend on how other
// slices evolve the shared sqlite fixture.
type aipTransport struct {
	db                                                   *gorm.DB
	server                                               http.Handler
	tenantID, inspectionID, snapshotID, assetID, otherID identity.ID
	admin, viewer, manager, scoped, customer, otherAdmin context.Context
	anonymous                                            context.Context
	// Admin-product principals of the retention policy operations (ADR-003).
	adminProduct, governance, auditor, accessAdmin context.Context
}

type aipGraphQLError struct {
	Message    string `json:"message"`
	Extensions struct {
		Code  string `json:"code"`
		Field string `json:"field"`
	} `json:"extensions"`
}

type aipResponse struct {
	Data   json.RawMessage   `json:"data"`
	Errors []aipGraphQLError `json:"errors"`
}

// aipAdminProductContext is an internal user of the Admin product.
func aipAdminProductContext(tenantID identity.ID, correlation string, roles []string) context.Context {
	return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, CorrelationID: correlation, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: roles, Audience: "inspection-admin", Product: auth.AdminProduct, ProductEntitlements: []string{auth.AdminProduct}}})
}

func aipPrincipalContext(tenantID identity.ID, correlation string, roles []string, scopes ...requestctx.Scope) context.Context {
	return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, CorrelationID: correlation, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: roles, Scopes: scopes, Audience: "inspection-dashboard", Product: auth.DashboardProduct, ProductEntitlements: []string{auth.DashboardProduct}}})
}

func newAIPTransport(t *testing.T) *aipTransport {
	t.Helper()
	db := task06ResolverDB(t)
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS reports.report_publications (id blob primary key, tenant_id blob not null, snapshot_id blob not null, inspection_id blob not null, policy_version integer not null default 0, client_mutation_id text not null default '', status text not null, actor_id blob, reason text, version integer not null default 1, published_at datetime, invalidated_at datetime, invalidated_by blob, superseded_by blob, created_at datetime, updated_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	f := &aipTransport{db: db, server: task06GraphQLServer(db), tenantID: identity.NewID(), inspectionID: identity.NewID(), snapshotID: identity.NewID(), assetID: identity.NewID(), otherID: identity.NewID()}
	businessUnitID := identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := db.Exec("INSERT INTO inspections.inspections (id, tenant_id, business_unit_id, asset_id) VALUES (?, ?, ?, ?)", f.inspectionID, f.tenantID, businessUnitID, f.assetID).Error; err != nil {
		t.Fatal(err)
	}
	canonical, _ := json.Marshal(map[string]any{"classification": "CRITICAL", "findings": []any{}, "context": map[string]any{"asset": map[string]any{"id": identity.NewID().String(), "name": "Imóvel QA", "externalKey": "QA-01", "address": "Rua de teste"}, "participant": map[string]any{"id": identity.NewID().String(), "name": "Responsável QA"}, "template": map[string]any{"id": identity.NewID().String(), "name": "Modelo QA", "version": 1}, "inspection": map[string]any{"generatedAt": now.Format(time.RFC3339Nano)}}})
	markup, _ := json.Marshal("<html><body>internal</body></html>")
	for _, row := range []any{
		&database.ReportSnapshot{ID: f.snapshotID, TenantID: f.tenantID, InspectionID: f.inspectionID, Mode: "HISTORICAL", Classification: "CRITICAL", JSONDigest: "json-digest", HTMLDigest: "html-digest", VersionNumber: 1, CanonicalJSON: canonical, HTML: markup, CreatedAt: now},
		&database.ReportArtifact{ID: identity.NewID(), TenantID: f.tenantID, SnapshotID: f.snapshotID, Kind: "PDF", ObjectKey: "reports/private.pdf", SHA256: "pdf-digest", Status: "READY", CreatedAt: now},
		&database.DashboardInspection{ID: identity.NewID(), TenantID: f.tenantID, InspectionID: f.inspectionID, AssetID: f.assetID, Classification: "CRITICAL", Status: "COMPLETED", Sequence: 1, UpdatedAt: now},
		&database.Delivery{ID: identity.NewID(), TenantID: f.tenantID, IntentID: identity.NewID(), InspectionID: &f.inspectionID, Status: "DELIVERED", CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	inputTokens, outputTokens, cost := int64(12), int64(4), 0.25
	if err := db.Create(&database.UsageRecord{ID: identity.NewID(), TenantID: f.tenantID, InspectionID: f.inspectionID, JobID: identity.NewID(), PromptSnapshotID: identity.NewID(), Provider: "stub", Model: "stub/vision", PromptDigest: "digest", InputTokens: &inputTokens, OutputTokens: &outputTokens, Cost: &cost, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	f.admin = aipPrincipalContext(f.tenantID, "aip-admin", []string{auth.TenantAdmin})
	f.viewer = aipPrincipalContext(f.tenantID, "aip-viewer", []string{auth.Viewer})
	f.manager = aipPrincipalContext(f.tenantID, "aip-manager-unscoped", []string{auth.Manager})
	f.scoped = aipPrincipalContext(f.tenantID, "aip-manager-scoped", []string{auth.Manager}, requestctx.Scope{Kind: "ASSET", ID: f.assetID})
	f.customer = aipPrincipalContext(f.tenantID, "aip-customer", []string{auth.CustomerViewer})
	f.otherAdmin = aipPrincipalContext(identity.NewID(), "aip-other-tenant", []string{auth.TenantAdmin})
	f.anonymous = context.Background()
	f.adminProduct = aipAdminProductContext(f.tenantID, "aip-admin-product", []string{auth.TenantAdmin})
	f.governance = aipAdminProductContext(f.tenantID, "aip-governance", []string{auth.GovernanceAdmin})
	f.auditor = aipAdminProductContext(f.tenantID, "aip-auditor", []string{auth.Auditor})
	f.accessAdmin = aipAdminProductContext(f.tenantID, "aip-access-admin", []string{auth.AccessAdmin})
	return f
}

// run executes a query and decodes the transport envelope.
func (f *aipTransport) run(t *testing.T, ctx context.Context, query string) (aipResponse, string) {
	t.Helper()
	raw := executeTask06GraphQL(t, f.server, query, ctx)
	var response aipResponse
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatalf("transport returned invalid JSON: %v: %s", err, raw)
	}
	return response, raw
}

// success asserts a schema-valid payload (data present, no errors) and decodes it.
func (f *aipTransport) success(t *testing.T, ctx context.Context, query string, into any) string {
	t.Helper()
	response, raw := f.run(t, ctx, query)
	if len(response.Errors) != 0 || len(response.Data) == 0 || string(response.Data) == "null" {
		t.Fatalf("authorized request failed: %s", raw)
	}
	if err := json.Unmarshal(response.Data, into); err != nil {
		t.Fatalf("payload does not match the schema shape: %v: %s", err, raw)
	}
	return raw
}

// failure asserts one stable error code (and field when given) and that no
// record data or internal detail is present in the response.
func (f *aipTransport) failure(t *testing.T, ctx context.Context, query, code, field string) aipGraphQLError {
	t.Helper()
	response, raw := f.run(t, ctx, query)
	if len(response.Errors) == 0 {
		t.Fatalf("request was accepted, want %s: %s", code, raw)
	}
	got := response.Errors[0]
	if got.Extensions.Code != code || (field != "" && got.Extensions.Field != field) {
		t.Fatalf("error code/field = %s/%s, want %s/%s: %s", got.Extensions.Code, got.Extensions.Field, code, field, raw)
	}
	for _, leak := range []string{"Imóvel QA", "Responsável QA", "QA-01", "Rua de teste", "pdf-digest", "reports/private.pdf", "json-digest", f.snapshotID.String(), "stub/vision", "SELECT ", "sqlite", "pq:", "gorm"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("error response leaked %q: %s", leak, raw)
		}
	}
	if string(response.Data) != "" && string(response.Data) != "null" && !strings.Contains(string(response.Data), "null") {
		t.Fatalf("failed request still returned data: %s", raw)
	}
	return got
}

func (f *aipTransport) reportQuery(inspectionID string) string {
	return `query { report(inspectionId:"` + inspectionID + `") { id inspectionId version mode classification jsonDigest htmlDigest canonicalJSON html advisory pdfStatus publication { status } } }`
}

// TestAIP_IT_423ReportReturnsSchemaValidPayloadForAuthorizedUser covers IT-423.
func TestAIP_IT_423ReportReturnsSchemaValidPayloadForAuthorizedUser(t *testing.T) {
	f := newAIPTransport(t)
	var data struct {
		Report struct {
			ID             string          `json:"id"`
			InspectionID   string          `json:"inspectionId"`
			Version        int             `json:"version"`
			Mode           string          `json:"mode"`
			Classification string          `json:"classification"`
			JSONDigest     string          `json:"jsonDigest"`
			HTMLDigest     string          `json:"htmlDigest"`
			CanonicalJSON  json.RawMessage `json:"canonicalJSON"`
			HTML           string          `json:"html"`
			PDFStatus      string          `json:"pdfStatus"`
		} `json:"report"`
		Reports struct {
			Nodes []struct {
				ID              string `json:"id"`
				InspectionID    string `json:"inspectionId"`
				AssetName       string `json:"assetName"`
				ParticipantName string `json:"participantName"`
				Classification  string `json:"classification"`
				Version         int    `json:"version"`
			} `json:"nodes"`
			PageInfo struct {
				HasNextPage bool `json:"hasNextPage"`
			} `json:"pageInfo"`
		} `json:"reports"`
	}
	f.success(t, f.admin, `query { report(inspectionId:"`+f.inspectionID.String()+`") { id inspectionId version mode classification jsonDigest htmlDigest canonicalJSON html pdfStatus } reports(first:5) { nodes { id inspectionId assetName participantName classification version } pageInfo { hasNextPage } } }`, &data)
	report := data.Report
	if report.ID != f.snapshotID.String() || report.InspectionID != f.inspectionID.String() || report.Version != 1 || report.Mode != "HISTORICAL" || report.Classification != "CRITICAL" || report.JSONDigest != "json-digest" || report.HTMLDigest != "html-digest" || report.PDFStatus != "READY" || !strings.Contains(report.HTML, "internal") || !json.Valid(report.CanonicalJSON) {
		t.Fatalf("unexpected report payload: %+v", report)
	}
	if len(data.Reports.Nodes) != 1 || data.Reports.Nodes[0].AssetName != "Imóvel QA" || data.Reports.Nodes[0].ParticipantName != "Responsável QA" || data.Reports.Nodes[0].InspectionID != f.inspectionID.String() || data.Reports.PageInfo.HasNextPage {
		t.Fatalf("unexpected report list: %+v", data.Reports)
	}
	// An asset-scoped manager sees the same report (scope check by asset grant).
	var scoped struct {
		Report struct {
			ID string `json:"id"`
		} `json:"report"`
	}
	f.success(t, f.scoped, f.reportQuery(f.inspectionID.String()), &scoped)
	if scoped.Report.ID != f.snapshotID.String() {
		t.Fatalf("scoped manager did not receive the granted report: %+v", scoped)
	}
}

// TestAIP_IT_424ReportRejectsInvalidProofScopeInputAndState covers IT-424.
func TestAIP_IT_424ReportRejectsInvalidProofScopeInputAndState(t *testing.T) {
	f := newAIPTransport(t)
	valid := f.reportQuery(f.inspectionID.String())
	for _, tc := range []struct {
		name, query, code, field string
		ctx                      context.Context
	}{
		{"missing proof", valid, "UNAUTHENTICATED", "", f.anonymous},
		{"malformed identifier", f.reportQuery("not-a-uuid"), "INVALID_INPUT", "inspectionId", f.admin},
		{"external customer role", valid, "FORBIDDEN", "", f.customer},
		{"unscoped internal user", valid, "NOT_FOUND", "inspectionId", f.manager},
		{"another tenant", valid, "NOT_FOUND", "inspectionId", f.otherAdmin},
		{"unknown inspection", f.reportQuery(identity.NewID().String()), "NOT_FOUND", "inspectionId", f.admin},
		{"missing explicit version", `query { report(inspectionId:"` + f.inspectionID.String() + `", version: 9) { id } }`, "NOT_FOUND", "version", f.admin},
	} {
		t.Run(tc.name, func(t *testing.T) { f.failure(t, tc.ctx, tc.query, tc.code, tc.field) })
	}
	t.Run("report still being produced", func(t *testing.T) {
		pending := identity.NewID()
		if err := f.db.Exec("INSERT INTO inspections.inspections (id, tenant_id, business_unit_id, asset_id) VALUES (?, ?, ?, ?)", pending, f.tenantID, identity.NewID(), f.assetID).Error; err != nil {
			t.Fatal(err)
		}
		if got := f.failure(t, f.admin, f.reportQuery(pending.String()), "INVALID_STATE", "inspectionId"); got.Message != "O relatório ainda não está pronto." {
			t.Fatalf("pending report message = %q, want the Portuguese text shown to the user", got.Message)
		}
	})
}

// TestAIP_IT_425ReportDownloadReturnsSchemaValidPayloadForAuthorizedUser covers IT-425.
func TestAIP_IT_425ReportDownloadReturnsSchemaValidPayloadForAuthorizedUser(t *testing.T) {
	f := newAIPTransport(t)
	var data struct {
		ReportDownload struct {
			SnapshotID string  `json:"snapshotId"`
			Kind       string  `json:"kind"`
			ObjectKey  string  `json:"objectKey"`
			URL        string  `json:"url"`
			Status     string  `json:"status"`
			SHA256     *string `json:"sha256"`
		} `json:"reportDownload"`
	}
	query := `query { reportDownload(snapshotId:"` + f.snapshotID.String() + `") { snapshotId kind objectKey url status sha256 } }`
	for name, ctx := range map[string]context.Context{"tenant administrator": f.admin, "asset-scoped manager": f.scoped} {
		f.success(t, ctx, query, &data)
		download := data.ReportDownload
		if download.SnapshotID != f.snapshotID.String() || download.Kind != "PDF" || download.Status != "READY" || download.SHA256 == nil || *download.SHA256 != "pdf-digest" || download.ObjectKey != "" {
			t.Fatalf("%s received an unexpected download payload: %+v", name, download)
		}
	}
	// A viewer without an asset grant gets the same stable not-found answer.
	f.failure(t, f.viewer, query, "NOT_FOUND", "snapshotId")
	// HTML kind has no stored artifact in this fixture: the answer is a visible status, not an error.
	f.success(t, f.admin, `query { reportDownload(snapshotId:"`+f.snapshotID.String()+`", kind:"HTML") { snapshotId kind objectKey url status } }`, &data)
	if data.ReportDownload.Kind != "HTML" || data.ReportDownload.Status != "PENDING" || data.ReportDownload.URL != "" || data.ReportDownload.ObjectKey != "" {
		t.Fatalf("missing artifact was not reported as pending: %+v", data.ReportDownload)
	}
}

// TestReportDownloadWithoutObjectStoreIsUnavailableNotAnEmptyURL guards the
// deployment without object storage: a READY artifact must not be answered OK
// with an empty URL.
func TestReportDownloadWithoutObjectStoreIsUnavailableNotAnEmptyURL(t *testing.T) {
	f := newAIPTransport(t)
	f.server = task06GraphQLServerWithStore(f.db, objectstore.Store{})
	f.failure(t, f.admin, `query { reportDownload(snapshotId:"`+f.snapshotID.String()+`") { snapshotId url status } }`, "DEPENDENCY_UNAVAILABLE", "store")
}

// TestAIP_IT_426ReportDownloadRejectsInvalidProofScopeInputAndState covers IT-426.
func TestAIP_IT_426ReportDownloadRejectsInvalidProofScopeInputAndState(t *testing.T) {
	f := newAIPTransport(t)
	query := func(id, extra string) string {
		return `query { reportDownload(snapshotId:"` + id + `"` + extra + `) { snapshotId } }`
	}
	valid := query(f.snapshotID.String(), "")
	for _, tc := range []struct {
		name, query, code, field string
		ctx                      context.Context
	}{
		{"missing proof", valid, "UNAUTHENTICATED", "", f.anonymous},
		{"malformed identifier", query("bad", ""), "INVALID_INPUT", "snapshotId", f.admin},
		{"unsupported format", query(f.snapshotID.String(), `, kind:"DOCX"`), "INVALID_INPUT", "kind", f.admin},
		{"unscoped internal user", valid, "NOT_FOUND", "snapshotId", f.manager},
		{"another tenant", valid, "NOT_FOUND", "snapshotId", f.otherAdmin},
		{"unknown snapshot", query(identity.NewID().String(), ""), "NOT_FOUND", "snapshotId", f.admin},
		{"customer without a published release", valid, "NOT_FOUND", "snapshotId", f.customer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := f.failure(t, tc.ctx, tc.query, tc.code, tc.field)
			if tc.code == "NOT_FOUND" && got.Message != "Relatório não encontrado." {
				t.Fatalf("hidden-resource message = %q, want the stable Portuguese text", got.Message)
			}
		})
	}
}

// TestAIP_IT_427DashboardSummaryReturnsSchemaValidPayloadForAuthorizedUser covers IT-427.
func TestAIP_IT_427DashboardSummaryReturnsSchemaValidPayloadForAuthorizedUser(t *testing.T) {
	f := newAIPTransport(t)
	var data struct {
		DashboardSummary struct {
			Total, Normal, Attention, Critical, Pending, Invalidated int
		} `json:"dashboardSummary"`
	}
	query := `query { dashboardSummary { total normal attention critical pending invalidated } }`
	f.success(t, f.admin, query, &data)
	if got := data.DashboardSummary; got.Total != 1 || got.Critical != 1 || got.Normal != 0 || got.Attention != 0 || got.Pending != 0 || got.Invalidated != 0 {
		t.Fatalf("unexpected summary: %+v", got)
	}
	f.success(t, f.scoped, query, &data)
	if data.DashboardSummary.Total != 1 {
		t.Fatalf("asset-scoped manager lost the granted inspection: %+v", data.DashboardSummary)
	}
}

// TestAIP_IT_428DashboardSummaryRejectsInvalidProofScopeInputAndState covers IT-428.
func TestAIP_IT_428DashboardSummaryRejectsInvalidProofScopeInputAndState(t *testing.T) {
	f := newAIPTransport(t)
	f.failure(t, f.anonymous, `query { dashboardSummary { total } }`, "UNAUTHENTICATED", "")
	f.failure(t, f.admin, `query { dashboardSummary(projectId:"bad") { total } }`, "INVALID_INPUT", "projectId")
	f.failure(t, f.admin, `query { dashboardSummary(businessUnitId:"bad") { total } }`, "INVALID_INPUT", "businessUnitId")
	f.failure(t, f.customer, `query { dashboardSummary { total } }`, "FORBIDDEN", "")
	// Wrong product or audience is refused before any count is read.
	wrongAudience := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: f.tenantID, CorrelationID: "aip-audience", Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: f.tenantID, Roles: []string{auth.TenantAdmin}, Audience: "inspection-capture", ProductEntitlements: auth.ProductsForRole(auth.TenantAdmin)}})
	f.failure(t, wrongAudience, `query { dashboardSummary { total } }`, "FORBIDDEN", "")
	// Scope and tenant boundaries never reveal counts: they answer with an empty portfolio.
	var data struct {
		DashboardSummary struct{ Total int } `json:"dashboardSummary"`
	}
	for name, ctx := range map[string]context.Context{"unscoped manager": f.manager, "another tenant": f.otherAdmin} {
		f.success(t, ctx, `query { dashboardSummary { total } }`, &data)
		if data.DashboardSummary.Total != 0 {
			t.Fatalf("%s saw %d inspections outside its scope", name, data.DashboardSummary.Total)
		}
	}
}

// TestAIP_IT_429TriageInspectionsReturnsSchemaValidPayloadForAuthorizedUser covers IT-429.
func TestAIP_IT_429TriageInspectionsReturnsSchemaValidPayloadForAuthorizedUser(t *testing.T) {
	f := newAIPTransport(t)
	var data struct {
		TriageInspections struct {
			Nodes []struct {
				InspectionID   string `json:"inspectionId"`
				AssetID        string `json:"assetId"`
				Classification string `json:"classification"`
				Status         string `json:"status"`
				UpdatedAt      string `json:"updatedAt"`
			} `json:"nodes"`
			PageInfo struct {
				HasNextPage bool `json:"hasNextPage"`
			} `json:"pageInfo"`
		} `json:"triageInspections"`
	}
	f.success(t, f.admin, `query { triageInspections(first:10, classification:"CRITICAL") { nodes { inspectionId assetId classification status updatedAt } pageInfo { hasNextPage } } }`, &data)
	nodes := data.TriageInspections.Nodes
	if len(nodes) != 1 || nodes[0].InspectionID != f.inspectionID.String() || nodes[0].AssetID != f.assetID.String() || nodes[0].Classification != "CRITICAL" || nodes[0].Status != "COMPLETED" || nodes[0].UpdatedAt == "" || data.TriageInspections.PageInfo.HasNextPage {
		t.Fatalf("unexpected triage payload: %+v", data.TriageInspections)
	}
	f.success(t, f.admin, `query { triageInspections(first:10, classification:"NORMAL") { nodes { inspectionId } } }`, &data)
	if len(data.TriageInspections.Nodes) != 0 {
		t.Fatalf("classification filter returned %d unrelated rows", len(data.TriageInspections.Nodes))
	}
}

// TestAIP_IT_430TriageInspectionsRejectsInvalidProofScopeInputAndState covers IT-430.
func TestAIP_IT_430TriageInspectionsRejectsInvalidProofScopeInputAndState(t *testing.T) {
	f := newAIPTransport(t)
	f.failure(t, f.anonymous, `query { triageInspections(first:1) { nodes { inspectionId } } }`, "UNAUTHENTICATED", "")
	f.failure(t, f.admin, `query { triageInspections(first:1, classification:"URGENT") { nodes { inspectionId } } }`, "INVALID_INPUT", "classification")
	f.failure(t, f.admin, `query { triageInspections(first:1, after:"not-a-cursor") { nodes { inspectionId } } }`, "INVALID_INPUT", "after")
	f.failure(t, f.customer, `query { triageInspections(first:1) { nodes { inspectionId } } }`, "FORBIDDEN", "")
	var data struct {
		TriageInspections struct {
			Nodes []struct{ InspectionID string } `json:"nodes"`
		} `json:"triageInspections"`
	}
	for name, ctx := range map[string]context.Context{"unscoped manager": f.manager, "another tenant": f.otherAdmin} {
		f.success(t, ctx, `query { triageInspections(first:10) { nodes { inspectionId } } }`, &data)
		if len(data.TriageInspections.Nodes) != 0 {
			t.Fatalf("%s listed %d inspections outside its scope", name, len(data.TriageInspections.Nodes))
		}
	}
}

// TestAIP_IT_433NotificationDeliveriesReturnsSchemaValidPayloadForAuthorizedUser covers IT-433.
func TestAIP_IT_433NotificationDeliveriesReturnsSchemaValidPayloadForAuthorizedUser(t *testing.T) {
	f := newAIPTransport(t)
	var data struct {
		NotificationDeliveries struct {
			Nodes []struct {
				ID           string `json:"id"`
				Status       string `json:"status"`
				InspectionID string `json:"inspectionId"`
			} `json:"nodes"`
			PageInfo struct {
				HasNextPage bool `json:"hasNextPage"`
			} `json:"pageInfo"`
		} `json:"notificationDeliveries"`
	}
	f.success(t, f.admin, `query { notificationDeliveries(first:5) { nodes { id status inspectionId } pageInfo { hasNextPage } } }`, &data)
	nodes := data.NotificationDeliveries.Nodes
	if len(nodes) != 1 || nodes[0].ID == "" || nodes[0].Status != "DELIVERED" || nodes[0].InspectionID != f.inspectionID.String() || data.NotificationDeliveries.PageInfo.HasNextPage {
		t.Fatalf("unexpected deliveries: %+v", data.NotificationDeliveries)
	}
}

// TestAIP_IT_434NotificationDeliveriesRejectsInvalidProofScopeInputAndState covers IT-434.
func TestAIP_IT_434NotificationDeliveriesRejectsInvalidProofScopeInputAndState(t *testing.T) {
	f := newAIPTransport(t)
	f.failure(t, f.anonymous, `query { notificationDeliveries(first:1) { nodes { id } } }`, "UNAUTHENTICATED", "")
	f.failure(t, f.customer, `query { notificationDeliveries(first:1) { nodes { id } } }`, "FORBIDDEN", "")
	f.failure(t, f.admin, `query { notificationDeliveries(first:1, after:"not-a-cursor") { nodes { id } } }`, "INVALID_INPUT", "after")
	var data struct {
		NotificationDeliveries struct {
			Nodes []struct{ ID string } `json:"nodes"`
		} `json:"notificationDeliveries"`
	}
	f.success(t, f.otherAdmin, `query { notificationDeliveries(first:10) { nodes { id } } }`, &data)
	if len(data.NotificationDeliveries.Nodes) != 0 {
		t.Fatalf("another tenant listed %d deliveries", len(data.NotificationDeliveries.Nodes))
	}
}

// TestAIP_IT_435RetentionPoliciesReturnsSchemaValidPayloadForAuthorizedUser covers IT-435.
func TestAIP_IT_435RetentionPoliciesReturnsSchemaValidPayloadForAuthorizedUser(t *testing.T) {
	f := newAIPTransport(t)
	var data struct {
		RetentionPolicies struct {
			Nodes []struct {
				EvidenceDays, OperationalDays, SecurityDays, Version int
			} `json:"nodes"`
		} `json:"retentionPolicies"`
	}
	f.success(t, f.adminProduct, `query { retentionPolicies { nodes { evidenceDays operationalDays securityDays version } pageInfo { hasNextPage } } }`, &data)
	// Without a tenant policy the platform defaults (five years / one year) are shown.
	if len(data.RetentionPolicies.Nodes) != 1 || data.RetentionPolicies.Nodes[0].EvidenceDays != 1825 || data.RetentionPolicies.Nodes[0].OperationalDays != 365 {
		t.Fatalf("unexpected default policy: %+v", data.RetentionPolicies.Nodes)
	}
	// The governance administrator and the auditor read the policy in the Admin product.
	f.success(t, f.governance, `query { retentionPolicies { nodes { version } } }`, &data)
	f.success(t, f.auditor, `query { retentionPolicies { nodes { version } } }`, &data)
}

// TestAIP_IT_436RetentionPoliciesRejectsInvalidProofScopeInputAndState covers IT-436.
func TestAIP_IT_436RetentionPoliciesRejectsInvalidProofScopeInputAndState(t *testing.T) {
	f := newAIPTransport(t)
	query := `query { retentionPolicies { nodes { version } } }`
	f.failure(t, f.anonymous, query, "UNAUTHENTICATED", "")
	f.failure(t, f.viewer, query, "FORBIDDEN", "")
	f.failure(t, f.customer, query, "FORBIDDEN", "")
	// The Admin product is required: a tenant administrator or a manager of the
	// Dashboard product, and an Admin role without retention authority, are refused.
	f.failure(t, f.admin, query, "FORBIDDEN", "")
	f.failure(t, f.manager, query, "FORBIDDEN", "")
	f.failure(t, f.accessAdmin, query, "FORBIDDEN", "")
}

// TestAIP_IT_437UsageSummaryReturnsSchemaValidPayloadForAuthorizedUser covers IT-437.
func TestAIP_IT_437UsageSummaryReturnsSchemaValidPayloadForAuthorizedUser(t *testing.T) {
	f := newAIPTransport(t)
	var data struct {
		UsageSummary struct {
			From, To     string
			Requests     int
			InputTokens  int
			OutputTokens int
			Cost         *float64
		} `json:"usageSummary"`
	}
	raw := f.success(t, f.admin, `query { usageSummary { from to requests inputTokens outputTokens cost } }`, &data)
	summary := data.UsageSummary
	if summary.Requests != 1 || summary.InputTokens != 12 || summary.OutputTokens != 4 || summary.From == "" || summary.To == "" {
		t.Fatalf("unexpected usage summary: %s", raw)
	}
	// Cost is visible only to the configured platform super administrator.
	if summary.Cost != nil {
		t.Fatalf("a tenant administrator received provider cost: %s", raw)
	}
	window := fmt.Sprintf(`query { usageSummary(from:%q, to:%q) { requests } }`, time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), time.Now().UTC().Add(time.Hour).Format(time.RFC3339))
	f.success(t, f.admin, window, &data)
	if data.UsageSummary.Requests != 1 {
		t.Fatalf("window around the record returned %d requests", data.UsageSummary.Requests)
	}
}

// TestAIP_IT_438UsageSummaryRejectsInvalidProofScopeInputAndState covers IT-438.
func TestAIP_IT_438UsageSummaryRejectsInvalidProofScopeInputAndState(t *testing.T) {
	f := newAIPTransport(t)
	f.failure(t, f.anonymous, `query { usageSummary { requests } }`, "UNAUTHENTICATED", "")
	f.failure(t, f.viewer, `query { usageSummary { requests } }`, "FORBIDDEN", "")
	f.failure(t, f.customer, `query { usageSummary { requests } }`, "FORBIDDEN", "")
	f.failure(t, f.admin, `query { usageSummary(from:"yesterday") { requests } }`, "INVALID_INPUT", "from")
	f.failure(t, f.admin, `query { usageSummary(to:"31/12/2026") { requests } }`, "INVALID_INPUT", "to")
	var data struct {
		UsageSummary struct{ Requests int } `json:"usageSummary"`
	}
	f.success(t, f.otherAdmin, `query { usageSummary { requests } }`, &data)
	if data.UsageSummary.Requests != 0 {
		t.Fatalf("another tenant counted %d provider requests", data.UsageSummary.Requests)
	}
}

func (f *aipTransport) retentionRows(t *testing.T, model any) int64 {
	t.Helper()
	var count int64
	if err := f.db.Model(model).Where("tenant_id=?", f.tenantID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

// TestAIP_IT_527ConfigureRetentionPolicyReturnsSchemaValidPayloadForAuthorizedUser covers IT-527.
func TestAIP_IT_527ConfigureRetentionPolicyReturnsSchemaValidPayloadForAuthorizedUser(t *testing.T) {
	f := newAIPTransport(t)
	var data struct {
		ConfigureRetentionPolicy struct {
			Policy struct {
				EvidenceDays, OperationalDays, SecurityDays, Version int
			} `json:"policy"`
			UserErrors       []struct{ Code string } `json:"userErrors"`
			ClientMutationID string                  `json:"clientMutationId"`
		} `json:"configureRetentionPolicy"`
	}
	save := func(evidence, operational int, mutation string) {
		f.success(t, f.adminProduct, fmt.Sprintf(`mutation { configureRetentionPolicy(input:{evidenceDays:%d,operationalDays:%d,securityDays:90,clientMutationId:%q}) { policy { evidenceDays operationalDays securityDays version } userErrors { code } clientMutationId } }`, evidence, operational, mutation), &data)
	}
	save(1825, 365, "aip-527-a")
	first := data.ConfigureRetentionPolicy
	if first.Policy.EvidenceDays != 1825 || first.Policy.OperationalDays != 365 || first.Policy.SecurityDays != 90 || first.Policy.Version != 1 || len(first.UserErrors) != 0 || first.ClientMutationID != "aip-527-a" {
		t.Fatalf("unexpected first policy: %+v", first)
	}
	save(730, 180, "aip-527-b")
	if second := data.ConfigureRetentionPolicy; second.Policy.Version != 2 || second.Policy.EvidenceDays != 730 {
		t.Fatalf("a new save must append the next immutable version: %+v", second)
	}
	if rows := f.retentionRows(t, &database.RetentionPolicy{}); rows != 2 {
		t.Fatalf("policy history has %d rows, want 2", rows)
	}
	var listed struct {
		RetentionPolicies struct {
			Nodes []struct{ Version, EvidenceDays int } `json:"nodes"`
		} `json:"retentionPolicies"`
	}
	f.success(t, f.adminProduct, `query { retentionPolicies { nodes { version evidenceDays } } }`, &listed)
	if len(listed.RetentionPolicies.Nodes) != 2 || listed.RetentionPolicies.Nodes[0].Version != 2 {
		t.Fatalf("the query does not show the newest policy first: %+v", listed.RetentionPolicies.Nodes)
	}
}

// TestAIP_IT_528ConfigureRetentionPolicyRejectsInvalidProofScopeInputAndState covers IT-528.
func TestAIP_IT_528ConfigureRetentionPolicyRejectsInvalidProofScopeInputAndState(t *testing.T) {
	f := newAIPTransport(t)
	mutation := func(evidence, operational, security int) string {
		return fmt.Sprintf(`mutation { configureRetentionPolicy(input:{evidenceDays:%d,operationalDays:%d,securityDays:%d,clientMutationId:"aip-528"}) { policy { version } } }`, evidence, operational, security)
	}
	for _, tc := range []struct {
		name, query, field string
	}{
		{"zero evidence period", mutation(0, 365, 0), "evidenceDays"},
		{"negative evidence period", mutation(-5, 365, 0), "evidenceDays"},
		{"zero operational period", mutation(1825, 0, 0), "operationalDays"},
		{"period beyond the supported range", mutation(1825, 40000, 0), "operationalDays"},
		{"negative security period", mutation(1825, 365, -1), "securityDays"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := f.failure(t, f.adminProduct, tc.query, "INVALID_INPUT", tc.field)
			if !strings.Contains(got.Message, "Informe um período de retenção") {
				t.Fatalf("validation message is not the Portuguese guidance: %q", got.Message)
			}
		})
	}
	valid := mutation(1825, 365, 0)
	f.failure(t, f.anonymous, valid, "UNAUTHENTICATED", "")
	f.failure(t, f.manager, valid, "FORBIDDEN", "")
	f.failure(t, f.viewer, valid, "FORBIDDEN", "")
	f.failure(t, f.customer, valid, "FORBIDDEN", "")
	f.failure(t, f.admin, valid, "FORBIDDEN", "")
	f.failure(t, f.auditor, valid, "FORBIDDEN", "")
	f.failure(t, f.accessAdmin, valid, "FORBIDDEN", "")
	if rows := f.retentionRows(t, &database.RetentionPolicy{}); rows != 0 {
		t.Fatalf("rejected requests persisted %d policy versions", rows)
	}
}

func aipRetentionMutation(name, inspectionID string) string {
	return fmt.Sprintf(`mutation { %s(input:{inspectionId:%q,reason:"motivo",clientMutationId:"aip"}) { status userErrors { code } clientMutationId } }`, name, inspectionID)
}

// TestAIP_IT_529RecordDeletionRequestReturnsSchemaValidPayloadForAuthorizedUser covers IT-529.
func TestAIP_IT_529RecordDeletionRequestReturnsSchemaValidPayloadForAuthorizedUser(t *testing.T) {
	f := newAIPTransport(t)
	var data struct {
		RecordDeletionRequest struct {
			Status           string
			RequestID        string
			UserErrors       []struct{ Code string }
			ClientMutationID string
		} `json:"recordDeletionRequest"`
	}
	query := `mutation { recordDeletionRequest(input:{inspectionId:"` + f.inspectionID.String() + `",reason:"solicitação do titular",clientMutationId:"aip-529"}) { status requestId userErrors { code } clientMutationId } }`
	for name, ctx := range map[string]context.Context{"tenant administrator": f.admin, "manager": aipPrincipalContext(f.tenantID, "aip-529-manager", []string{auth.Manager})} {
		f.success(t, ctx, query, &data)
		got := data.RecordDeletionRequest
		if got.Status != "REQUESTED" || got.RequestID == "" || len(got.UserErrors) != 0 || got.ClientMutationID != "aip-529" {
			t.Fatalf("%s received an unexpected payload: %+v", name, got)
		}
	}
	var stored []database.DeletionRequest
	if err := f.db.Where("tenant_id=? AND inspection_id=?", f.tenantID, f.inspectionID).Find(&stored).Error; err != nil || len(stored) == 0 {
		t.Fatalf("deletion request was not recorded: %v", err)
	}
	for _, row := range stored {
		if row.Status != "REQUESTED" || row.Reason != "solicitação do titular" || row.ProcessedAt != nil {
			t.Fatalf("a request only records intent; premature deletion must wait for the clock: %+v", row)
		}
	}
}

// TestAIP_IT_530RecordDeletionRequestRejectsInvalidProofScopeInputAndState covers IT-530.
func TestAIP_IT_530RecordDeletionRequestRejectsInvalidProofScopeInputAndState(t *testing.T) {
	f := newAIPTransport(t)
	valid := aipRetentionMutation("recordDeletionRequest", f.inspectionID.String())
	f.failure(t, f.anonymous, valid, "UNAUTHENTICATED", "")
	f.failure(t, f.viewer, valid, "FORBIDDEN", "")
	f.failure(t, f.customer, valid, "FORBIDDEN", "")
	f.failure(t, f.admin, aipRetentionMutation("recordDeletionRequest", "bad"), "INVALID_INPUT", "inspectionId")
	f.failure(t, f.admin, `mutation { recordDeletionRequest(input:{clientMutationId:"aip"}) { status } }`, "INVALID_INPUT", "scope")
	two := fmt.Sprintf(`mutation { recordDeletionRequest(input:{inspectionId:%q,sourceMediaId:%q,clientMutationId:"aip"}) { status } }`, f.inspectionID.String(), identity.NewID().String())
	f.failure(t, f.admin, two, "INVALID_INPUT", "scope")
	f.failure(t, f.otherAdmin, valid, "NOT_FOUND", "")
	f.failure(t, f.admin, aipRetentionMutation("recordDeletionRequest", identity.NewID().String()), "NOT_FOUND", "")
	if rows := f.retentionRows(t, &database.DeletionRequest{}); rows != 0 {
		t.Fatalf("rejected requests persisted %d deletion requests", rows)
	}
}

// TestAIP_IT_531ApplyLegalHoldReturnsSchemaValidPayloadForAuthorizedUser covers IT-531.
func TestAIP_IT_531ApplyLegalHoldReturnsSchemaValidPayloadForAuthorizedUser(t *testing.T) {
	f := newAIPTransport(t)
	var data struct {
		ApplyLegalHold struct {
			Status           string
			UserErrors       []struct{ Code string }
			ClientMutationID string
		} `json:"applyLegalHold"`
	}
	for attempt := 0; attempt < 2; attempt++ {
		f.success(t, f.admin, aipRetentionMutation("applyLegalHold", f.inspectionID.String()), &data)
		if data.ApplyLegalHold.Status != "HELD" || len(data.ApplyLegalHold.UserErrors) != 0 || data.ApplyLegalHold.ClientMutationID != "aip" {
			t.Fatalf("attempt %d returned %+v", attempt+1, data.ApplyLegalHold)
		}
	}
	var active int64
	if err := f.db.Model(&database.LegalHold{}).Where("tenant_id=? AND inspection_id=? AND active", f.tenantID, f.inspectionID).Count(&active).Error; err != nil || active != 1 {
		t.Fatalf("applying the hold twice must keep one active hold: %d (%v)", active, err)
	}
}

// TestAIP_IT_532ApplyLegalHoldRejectsInvalidProofScopeInputAndState covers IT-532.
func TestAIP_IT_532ApplyLegalHoldRejectsInvalidProofScopeInputAndState(t *testing.T) {
	f := newAIPTransport(t)
	valid := aipRetentionMutation("applyLegalHold", f.inspectionID.String())
	f.failure(t, f.anonymous, valid, "UNAUTHENTICATED", "")
	f.failure(t, f.viewer, valid, "FORBIDDEN", "")
	f.failure(t, f.customer, valid, "FORBIDDEN", "")
	f.failure(t, f.admin, aipRetentionMutation("applyLegalHold", "bad"), "INVALID_INPUT", "inspectionId")
	f.failure(t, f.admin, `mutation { applyLegalHold(input:{reason:"x",clientMutationId:"aip"}) { status } }`, "INVALID_INPUT", "scope")
	f.failure(t, f.otherAdmin, valid, "NOT_FOUND", "")
	f.failure(t, f.admin, aipRetentionMutation("applyLegalHold", identity.NewID().String()), "NOT_FOUND", "")
	if rows := f.retentionRows(t, &database.LegalHold{}); rows != 0 {
		t.Fatalf("rejected requests persisted %d holds", rows)
	}
}

// TestAIP_IT_533ReleaseLegalHoldReturnsSchemaValidPayloadForAuthorizedUser covers IT-533.
func TestAIP_IT_533ReleaseLegalHoldReturnsSchemaValidPayloadForAuthorizedUser(t *testing.T) {
	f := newAIPTransport(t)
	var data struct {
		ReleaseLegalHold struct {
			Status           string
			UserErrors       []struct{ Code string }
			ClientMutationID string
		} `json:"releaseLegalHold"`
	}
	var applied struct {
		ApplyLegalHold struct{ Status string } `json:"applyLegalHold"`
	}
	f.success(t, f.admin, aipRetentionMutation("applyLegalHold", f.inspectionID.String()), &applied)
	for attempt := 0; attempt < 2; attempt++ {
		f.success(t, f.admin, aipRetentionMutation("releaseLegalHold", f.inspectionID.String()), &data)
		if data.ReleaseLegalHold.Status != "RELEASED" || len(data.ReleaseLegalHold.UserErrors) != 0 || data.ReleaseLegalHold.ClientMutationID != "aip" {
			t.Fatalf("attempt %d returned %+v", attempt+1, data.ReleaseLegalHold)
		}
	}
	var hold database.LegalHold
	if err := f.db.Where("tenant_id=? AND inspection_id=?", f.tenantID, f.inspectionID).Take(&hold).Error; err != nil || hold.Active || hold.ReleasedAt == nil {
		t.Fatalf("hold was not released with an audit instant: %+v (%v)", hold, err)
	}
}

// TestAIP_IT_534ReleaseLegalHoldRejectsInvalidProofScopeInputAndState covers IT-534.
func TestAIP_IT_534ReleaseLegalHoldRejectsInvalidProofScopeInputAndState(t *testing.T) {
	f := newAIPTransport(t)
	var applied struct {
		ApplyLegalHold struct{ Status string } `json:"applyLegalHold"`
	}
	f.success(t, f.admin, aipRetentionMutation("applyLegalHold", f.inspectionID.String()), &applied)
	valid := aipRetentionMutation("releaseLegalHold", f.inspectionID.String())
	f.failure(t, f.anonymous, valid, "UNAUTHENTICATED", "")
	f.failure(t, f.viewer, valid, "FORBIDDEN", "")
	f.failure(t, f.customer, valid, "FORBIDDEN", "")
	f.failure(t, f.admin, aipRetentionMutation("releaseLegalHold", "bad"), "INVALID_INPUT", "inspectionId")
	f.failure(t, f.admin, `mutation { releaseLegalHold(input:{reason:"x",clientMutationId:"aip"}) { status } }`, "INVALID_INPUT", "scope")
	f.failure(t, f.otherAdmin, valid, "NOT_FOUND", "")
	var active int64
	if err := f.db.Model(&database.LegalHold{}).Where("tenant_id=? AND inspection_id=? AND active", f.tenantID, f.inspectionID).Count(&active).Error; err != nil || active != 1 {
		t.Fatalf("rejected releases changed the hold: active=%d (%v)", active, err)
	}
}

// TestAIP_IT_268ReportRequestedBeforeComparisonsAreTerminalIsPendingNotAnError
// covers IT-268: until a snapshot exists the report answers with the stable
// INVALID_STATE (pending) code and leaks nothing, the list shows only terminal
// reports and the same request succeeds once the snapshot is stored.
func TestAIP_IT_268ReportRequestedBeforeComparisonsAreTerminalIsPendingNotAnError(t *testing.T) {
	f := newAIPTransport(t)
	pending := identity.NewID()
	if err := f.db.Exec("INSERT INTO inspections.inspections (id, tenant_id, business_unit_id, asset_id) VALUES (?, ?, ?, ?)", pending, f.tenantID, identity.NewID(), f.assetID).Error; err != nil {
		t.Fatal(err)
	}
	f.failure(t, f.admin, f.reportQuery(pending.String()), "INVALID_STATE", "inspectionId")
	var list struct {
		Reports struct {
			Nodes []struct {
				InspectionID string `json:"inspectionId"`
			} `json:"nodes"`
		} `json:"reports"`
	}
	f.success(t, f.admin, `query { reports(first:10) { nodes { inspectionId } } }`, &list)
	for _, node := range list.Reports.Nodes {
		if node.InspectionID == pending.String() {
			t.Fatalf("a pending inspection was listed as a report: %+v", list.Reports.Nodes)
		}
	}
	// A terminal inspection of the same tenant is unaffected while the other is pending.
	var ready struct {
		Report struct{ Classification string } `json:"report"`
	}
	f.success(t, f.admin, f.reportQuery(f.inspectionID.String()), &ready)
	if ready.Report.Classification != "CRITICAL" {
		t.Fatalf("terminal report = %+v", ready.Report)
	}
	// The comparisons finish: the snapshot appears and the very same request now succeeds.
	snapshotID := identity.NewID()
	canonical, _ := json.Marshal(map[string]any{"classification": "ATTENTION", "findings": []any{}, "context": map[string]any{"asset": map[string]any{"id": identity.NewID().String(), "name": "Imóvel QA", "externalKey": "QA-01", "address": "Rua de teste"}, "participant": map[string]any{"id": identity.NewID().String(), "name": "Responsável QA"}, "template": map[string]any{"id": identity.NewID().String(), "name": "Modelo QA", "version": 1}, "inspection": map[string]any{"generatedAt": time.Now().UTC().Format(time.RFC3339Nano)}}})
	markup, _ := json.Marshal("<html></html>")
	if err := f.db.Create(&database.ReportSnapshot{ID: snapshotID, TenantID: f.tenantID, InspectionID: pending, Mode: "HISTORICAL", Classification: "ATTENTION", JSONDigest: "d2", HTMLDigest: "h2", VersionNumber: 1, CanonicalJSON: canonical, HTML: markup, CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	f.success(t, f.admin, f.reportQuery(pending.String()), &ready)
	if ready.Report.Classification != "ATTENTION" {
		t.Fatalf("report after the comparisons finished = %+v", ready.Report)
	}
}
