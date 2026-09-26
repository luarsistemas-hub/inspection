package resolvers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTask06GraphQLReadsAndStableAuth(t *testing.T) {
	db := task06ResolverDB(t)
	tenantID, inspectionID, snapshotID, businessUnitID, assetID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := db.Exec("INSERT INTO inspections.inspections (id, tenant_id, business_unit_id, asset_id) VALUES (?, ?, ?, ?)", inspectionID, tenantID, businessUnitID, assetID).Error; err != nil {
		t.Fatal(err)
	}
	canonical, _ := json.Marshal(map[string]any{"classification": "CRITICAL", "findings": []any{}, "context": map[string]any{"asset": map[string]any{"id": identity.NewID().String(), "name": "Imóvel QA", "externalKey": "QA-01", "address": "Rua de teste"}, "participant": map[string]any{"id": identity.NewID().String(), "name": "Responsável QA"}, "template": map[string]any{"id": identity.NewID().String(), "name": "Modelo QA", "version": 1}, "inspection": map[string]any{"generatedAt": now.Format(time.RFC3339Nano)}}})
	markup, _ := json.Marshal("<html><body>internal</body></html>")
	if err := db.Create(&database.ReportSnapshot{ID: snapshotID, TenantID: tenantID, InspectionID: inspectionID, Mode: "HISTORICAL", Classification: "CRITICAL", JSONDigest: "json-digest", HTMLDigest: "html-digest", VersionNumber: 1, CanonicalJSON: canonical, HTML: markup, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.ReportArtifact{ID: identity.NewID(), TenantID: tenantID, SnapshotID: snapshotID, Kind: "PDF", ObjectKey: "reports/private.pdf", SHA256: "pdf-digest", Status: "READY", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.DashboardInspection{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, Classification: "CRITICAL", Status: "COMPLETED", Sequence: 1, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.Delivery{ID: identity.NewID(), TenantID: tenantID, IntentID: identity.NewID(), InspectionID: &inspectionID, Status: "DELIVERED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	inputTokens, outputTokens, cost := int64(12), int64(4), 0.25
	if err := db.Create(&database.UsageRecord{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, JobID: identity.NewID(), PromptSnapshotID: identity.NewID(), Provider: "stub", Model: "stub/vision", PromptDigest: "digest", InputTokens: &inputTokens, OutputTokens: &outputTokens, Cost: &cost, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	server := task06GraphQLServer(db)
	validContext := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "task06"})
	query := `query { report(inspectionId:"` + inspectionID.String() + `") { id classification version canonicalJSON html } reports(first:1) { nodes { id inspectionId assetName assetAddress assetExternalKey participantName classification version } pageInfo { hasNextPage } } reportDownload(snapshotId:"` + snapshotID.String() + `") { snapshotId kind objectKey url status sha256 } dashboardSummary { total normal attention critical pending invalidated } triageInspections(first:1) { nodes { inspectionId classification status } pageInfo { hasNextPage } } notificationDeliveries(first:1) { nodes { id status } pageInfo { hasNextPage } } retentionPolicies { nodes { evidenceDays operationalDays securityDays version } pageInfo { hasNextPage } } usageSummary { requests inputTokens outputTokens cost } }`
	if response := executeTask06GraphQL(t, server, query, validContext); strings.Contains(response, `"errors"`) || !strings.Contains(response, `"CRITICAL"`) || !strings.Contains(response, `"pdf-digest"`) || !strings.Contains(response, `"requests":1`) || !strings.Contains(response, `"assetName":"Imóvel QA"`) {
		t.Fatalf("unexpected authorized Task 06 response: %s", response)
	}

	viewerContext := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{"VIEWER"}}, CorrelationID: "task06-forbidden"})
	if response := executeTask06GraphQL(t, server, `query { retentionPolicies { nodes { version } } usageSummary { requests } }`, viewerContext); !strings.Contains(response, "FORBIDDEN") {
		t.Fatalf("role-restricted Task 06 reads disclosed data: %s", response)
	}
	unscopedContext := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.Manager}}, CorrelationID: "report-scope-forbidden"})
	if response := executeTask06GraphQL(t, server, `query { report(inspectionId:"`+inspectionID.String()+`") { id } }`, unscopedContext); !strings.Contains(response, "FORBIDDEN") {
		t.Fatalf("unscoped report link exposed a report: %s", response)
	}
	if response := executeTask06GraphQL(t, server, `query { reportDownload(snapshotId:"`+snapshotID.String()+`") { snapshotId } }`, unscopedContext); !strings.Contains(response, "FORBIDDEN") {
		t.Fatalf("unscoped report download exposed an artifact: %s", response)
	}
	scopedContext := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.Manager}, Scopes: []requestctx.Scope{{Kind: "ASSET", ID: assetID}}}, CorrelationID: "report-asset-scope"})
	if response := executeTask06GraphQL(t, server, `query { reportDownload(snapshotId:"`+snapshotID.String()+`") { snapshotId } }`, scopedContext); strings.Contains(response, `"errors"`) {
		t.Fatalf("asset-scoped report download was denied: %s", response)
	}
	mutationContext := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "task06-mutations"})
	mutations := []struct {
		name, query, expected string
	}{
		{"IT-527 configure retention", `mutation { configureRetentionPolicy(input:{evidenceDays:1825,operationalDays:365,securityDays:90,clientMutationId:"c"}) { policy { evidenceDays operationalDays securityDays version } } }`, `"evidenceDays":1825`},
		{"IT-529 record deletion", `mutation { recordDeletionRequest(input:{inspectionId:"` + inspectionID.String() + `",reason:"requested",clientMutationId:"c"}) { status requestId } }`, `"status":"REQUESTED"`},
		{"IT-531 apply hold", `mutation { applyLegalHold(input:{inspectionId:"` + inspectionID.String() + `",reason:"legal",clientMutationId:"c"}) { status } }`, `"status":"HELD"`},
		{"IT-533 release hold", `mutation { releaseLegalHold(input:{inspectionId:"` + inspectionID.String() + `",reason:"legal",clientMutationId:"c"}) { status } }`, `"status":"RELEASED"`},
	}
	for _, item := range mutations {
		t.Run(item.name, func(t *testing.T) {
			response := executeTask06GraphQL(t, server, item.query, mutationContext)
			if strings.Contains(response, `"errors"`) || !strings.Contains(response, item.expected) {
				t.Fatalf("unexpected mutation response: %s", response)
			}
		})
	}
	for _, item := range []struct {
		name, query string
	}{
		{"IT-528 invalid retention", `mutation { configureRetentionPolicy(input:{evidenceDays:0,operationalDays:365,clientMutationId:"c"}) { userErrors { code } } }`},
		{"IT-530 invalid deletion", `mutation { recordDeletionRequest(input:{inspectionId:"bad",clientMutationId:"c"}) { userErrors { code } } }`},
		{"IT-532 invalid hold", `mutation { applyLegalHold(input:{inspectionId:"bad",reason:"legal",clientMutationId:"c"}) { userErrors { code } } }`},
		{"IT-534 invalid release", `mutation { releaseLegalHold(input:{inspectionId:"bad",reason:"legal",clientMutationId:"c"}) { userErrors { code } } }`},
	} {
		t.Run(item.name, func(t *testing.T) {
			response := executeTask06GraphQL(t, server, item.query, mutationContext)
			if !strings.Contains(response, "INVALID_INPUT") && !strings.Contains(response, "invalid") {
				t.Fatalf("invalid mutation was not rejected safely: %s", response)
			}
		})
	}
}

func task06ResolverDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, schema := range []string{"reports", "dashboard", "notifications", "retention", "usage", "inspections"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.ReportSnapshot{}, &database.ReportArtifact{}, &database.RetentionPolicy{}, &database.UsageRecord{}} {
		if err := db.AutoMigrate(model); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`CREATE TABLE inspections.inspections (id blob primary key, tenant_id blob not null, business_unit_id blob, asset_id blob, project_id blob)`,
		`CREATE TABLE dashboard.inspections (id blob primary key, tenant_id blob not null, inspection_id blob not null, project_id blob, asset_id blob not null, classification text not null, status text not null, invalidated numeric not null default 0, sequence integer not null, updated_at datetime)`,
		`CREATE TABLE notifications.deliveries (id blob primary key, tenant_id blob not null, intent_id blob not null, inspection_id blob, invitation_id blob, status text not null, logical_template text, template_version text, correlation_id text, idempotency_key text, request_digest text, recipient_id text, selected_provider text, scheduled_at datetime, lease_expires_at datetime, created_at datetime, updated_at datetime)`,
		`CREATE TABLE notifications.channel_attempts (id blob primary key, tenant_id blob not null, delivery_id blob not null, channel text not null, destination text not null, status text not null, provider text, provider_account text, receipt_id text, attempts integer not null default 0, last_error text, template_variables blob, next_attempt_at datetime, lease_expires_at datetime, last_attempt_at datetime, created_at datetime, updated_at datetime)`,
		`CREATE TABLE retention.deletion_requests (id blob primary key, tenant_id blob not null, inspection_id blob not null, status text not null, reason text, requested_at datetime, processed_at datetime)`,
		`CREATE TABLE retention.legal_holds (id blob primary key, tenant_id blob not null, inspection_id blob not null, reason text, active numeric not null, created_at datetime, released_at datetime)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func task06GraphQLServer(db *gorm.DB) *handler.Server {
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: db, Bus: mediator.New()}}))
	server.SetErrorPresenter(graph.PresentError)
	return server
}

func executeTask06GraphQL(t *testing.T, server http.Handler, query string, ctx context.Context) string {
	t.Helper()
	w := httptest.NewRecorder()
	ctx = requestctx.WithResponseWriter(ctx, w)
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+quote(query)+`}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(w, req)
	return w.Body.String()
}
