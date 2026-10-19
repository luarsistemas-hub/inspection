package resolvers

import (
	"context"
	"database/sql/driver"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestVideoAdmissionGraphQLTransportIT524ToIT530(t *testing.T) {
	db := videoAdmissionTestDB(t)
	if err := db.Create(&database.VideoAdmission{ID: 1, Enabled: true, Version: 1, ChangedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	tenantID, identityID := identity.NewID(), identity.NewID()
	principal := requestctx.Principal{IdentityID: identityID, TenantID: tenantID, Issuer: "test-issuer", Subject: "platform-operator", Product: auth.AdminProduct, ProductEntitlements: []string{auth.AdminProduct}, Roles: []string{auth.TenantAdmin}}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: principal, CorrelationID: "video-admission-test"})
	resolver := &Resolver{DB: db, WithinTenant: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}, Authorizer: auth.Authorizer{}, SuperAdminIssuer: principal.Issuer, SuperAdminSubject: principal.Subject}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: resolver}))
	server.SetErrorPresenter(graph.PresentError)

	response := executeVideoAdmissionGraphQL(t, server, `mutation { configureVideoAdmission(input:{enabled:false,expectedVersion:1,reason:"Capacity incident",clientMutationId:"k1"}) { admission { enabled version reason } clientMutationId userErrors { field message } } }`, ctx)
	for _, expected := range []string{`"enabled":false`, `"version":2`, `"reason":"Capacity incident"`, `"clientMutationId":"k1"`} {
		if !strings.Contains(response, expected) {
			t.Fatalf("admission mutation response missing %s: %s", expected, response)
		}
	}

	response = executeVideoAdmissionGraphQL(t, server, `query { videoAdmission { enabled version reason } }`, ctx)
	if !strings.Contains(response, `"enabled":false`) || !strings.Contains(response, `"version":2`) {
		t.Fatalf("admission query disagrees with mutation: %s", response)
	}
	injectedTenant := executeVideoAdmissionGraphQL(t, server, `query { videoAdmission(tenantId:"00000000-0000-0000-0000-000000000002") { enabled } }`, ctx)
	if !strings.Contains(injectedTenant, "Unknown argument") || !strings.Contains(injectedTenant, `"data":null`) {
		t.Fatalf("platform admission accepted an injected tenant selector: %s", injectedTenant)
	}

	unauthenticated := executeVideoAdmissionGraphQL(t, server, `query { videoAdmission { enabled } }`, context.Background())
	if !strings.Contains(unauthenticated, "UNAUTHENTICATED") || !strings.Contains(unauthenticated, `"data":null`) {
		t.Fatalf("unauthenticated admission query was accepted: %s", unauthenticated)
	}
	unauthenticatedMutation := executeVideoAdmissionGraphQL(t, server, `mutation { configureVideoAdmission(input:{enabled:true,expectedVersion:2,reason:"Unauthorized",clientMutationId:"unauth-k"}) { admission { version } } }`, context.Background())
	if !strings.Contains(unauthenticatedMutation, "UNAUTHENTICATED") || strings.Contains(unauthenticatedMutation, `"admission":{`) {
		t.Fatalf("unauthenticated admission mutation was accepted: %s", unauthenticatedMutation)
	}
	wrongOperator := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Issuer: principal.Issuer, Subject: "another-admin", Product: auth.AdminProduct, ProductEntitlements: []string{auth.AdminProduct}, Roles: []string{auth.TenantAdmin}}, CorrelationID: "video-admission-wrong-operator"})
	for _, operation := range []struct{ name, query string }{
		{"query", `query { videoAdmission { enabled version } }`},
		{"mutation", `mutation { configureVideoAdmission(input:{enabled:true,expectedVersion:2,reason:"Unauthorized",clientMutationId:"wrong-operator-k"}) { admission { version } } }`},
	} {
		failure := executeVideoAdmissionGraphQL(t, server, operation.query, wrongOperator)
		if !strings.Contains(failure, "FORBIDDEN") || strings.Contains(failure, `"admission":{`) {
			t.Fatalf("non-operator %s was not rejected: %s", operation.name, failure)
		}
	}
	for _, failure := range []struct {
		name string
		err  error
		code string
	}{
		{"unavailable", driver.ErrBadConn, "DEPENDENCY_UNAVAILABLE"},
		{"unexpected", fmt.Errorf("database error: qa-secret-123"), "INTERNAL"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			for _, operation := range []struct{ name, query, absent string }{
				{"query", `query { videoAdmission { enabled } }`, `"videoAdmission":{`},
				{"mutation", `mutation { configureVideoAdmission(input:{enabled:true,expectedVersion:2,reason:"Adapter failure test",clientMutationId:"adapter-failure"}) { admission { version } } }`, `"admission":{`},
			} {
				callbackName := "video-admission-failure-" + failure.name + "-" + operation.name
				if err := db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) { tx.AddError(failure.err) }); err != nil {
					t.Fatal(err)
				}
				response := executeVideoAdmissionGraphQL(t, server, operation.query, ctx)
				if err := db.Callback().Query().Remove(callbackName); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(response, failure.code) || strings.Contains(response, operation.absent) {
					t.Fatalf("%s did not return safe %s without result data: %s", operation.name, failure.code, response)
				}
				if failure.code == "INTERNAL" && strings.Contains(response, "qa-secret-123") {
					t.Fatalf("%s exposed internal database detail: %s", operation.name, response)
				}
			}
		})
	}

	for _, tc := range []struct {
		name, query, code string
	}{
		{"stale version", `mutation { configureVideoAdmission(input:{enabled:true,expectedVersion:1,reason:"Capacity restored",clientMutationId:"k2"}) { admission { version } } }`, "CONFLICT"},
		{"missing reason", `mutation { configureVideoAdmission(input:{enabled:true,expectedVersion:2,reason:" ",clientMutationId:"k3"}) { admission { version } } }`, "INVALID_INPUT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := executeVideoAdmissionGraphQL(t, server, tc.query, ctx)
			if !strings.Contains(failure, tc.code) || strings.Contains(failure, `"admission":{`) {
				t.Fatalf("rejected admission update returned success data: %s", failure)
			}
		})
	}

	var auditCount int64
	if err := db.Model(&database.AuditEvent{}).Where("action = ? AND target_id = ?", "video.admission_configured", "1").Count(&auditCount).Error; err != nil || auditCount != 1 {
		t.Fatalf("admission audit count=%d err=%v", auditCount, err)
	}
}

func videoAdmissionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"platform", "audit"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.VideoAdmission{}, &database.AuditEvent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS audit.events (id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, actor_id TEXT NOT NULL, action TEXT NOT NULL, target_type TEXT NOT NULL, target_id TEXT NOT NULL, outcome TEXT NOT NULL, reason TEXT, correlation_id TEXT NOT NULL, occurred_at DATETIME NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func executeVideoAdmissionGraphQL(t *testing.T, server http.Handler, query string, ctx context.Context) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+quote(query)+`}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(w, req)
	return w.Body.String()
}
