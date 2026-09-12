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
	tenantID, inspectionID, snapshotID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := db.Exec("INSERT INTO inspections.inspections (id, tenant_id) VALUES (?, ?)", inspectionID, tenantID).Error; err != nil {
		t.Fatal(err)
	}
	canonical, _ := json.Marshal(map[string]any{"classification": "CRITICAL", "findings": []any{}})
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
	if err := db.Create(&database.UsageRecord{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, JobID: identity.NewID(), Provider: "stub", Model: "stub/vision", PromptVersion: "analysis-v1", InputTokens: &inputTokens, OutputTokens: &outputTokens, Cost: &cost, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	server := task06GraphQLServer(db)
	validContext := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "task06"})
	query := `query { report(inspectionId:"` + inspectionID.String() + `") { id classification version canonicalJSON html } reportDownload(snapshotId:"` + snapshotID.String() + `") { snapshotId kind objectKey url status sha256 } dashboardSummary { total normal attention critical pending invalidated } triageInspections(first:1) { nodes { inspectionId classification status } pageInfo { hasNextPage } } notificationDeliveries(first:1) { nodes { id status } pageInfo { hasNextPage } } retentionPolicies { nodes { evidenceDays operationalDays securityDays version } pageInfo { hasNextPage } } usageSummary { requests inputTokens outputTokens cost } }`
	if response := executeTask06GraphQL(t, server, query, validContext); strings.Contains(response, `"errors"`) || !strings.Contains(response, `"CRITICAL"`) || !strings.Contains(response, `"pdf-digest"`) || !strings.Contains(response, `"requests":1`) {
		t.Fatalf("unexpected authorized Task 06 response: %s", response)
	}

	viewerContext := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{"VIEWER"}}, CorrelationID: "task06-forbidden"})
	if response := executeTask06GraphQL(t, server, `query { retentionPolicies { nodes { version } } usageSummary { requests } }`, viewerContext); !strings.Contains(response, "FORBIDDEN") {
		t.Fatalf("role-restricted Task 06 reads disclosed data: %s", response)
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
		`CREATE TABLE inspections.inspections (id blob primary key, tenant_id blob not null)`,
		`CREATE TABLE dashboard.inspections (id blob primary key, tenant_id blob not null, inspection_id blob not null, project_id blob, asset_id blob not null, classification text not null, status text not null, invalidated numeric not null default 0, sequence integer not null, updated_at datetime)`,
		`CREATE TABLE notifications.deliveries (id blob primary key, tenant_id blob not null, intent_id blob not null, inspection_id blob, status text not null, logical_template text, template_version text, correlation_id text, idempotency_key text, request_digest text, recipient_id text, selected_provider text, scheduled_at datetime, lease_expires_at datetime, created_at datetime, updated_at datetime)`,
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
