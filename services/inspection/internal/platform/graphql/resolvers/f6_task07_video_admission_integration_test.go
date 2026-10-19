//go:build integration

package resolvers

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
)

func TestVV_IT_531StaleVideoAdmissionVersionIsConflict(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	h, err := harness.NewFromEnv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	seed, err := h.SeedTask06Fixture(context.Background(), harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Exec("INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, TRUE, 4, ?, 'baseline', 'vv-it531-baseline', '') ON CONFLICT (id) DO UPDATE SET enabled=TRUE, version=4, reason='baseline'", time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	principal := requestctx.Principal{IdentityID: seed.IdentityID, TenantID: seed.TenantID, Issuer: "vv-it531-issuer", Subject: "vv-it531-operator", Product: auth.AdminProduct, ProductEntitlements: []string{auth.AdminProduct}, Roles: []string{auth.TenantAdmin}}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: seed.TenantID, Principal: principal, CorrelationID: "vv-it-531"})
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Authorizer: auth.Authorizer{}, SuperAdminIssuer: principal.Issuer, SuperAdminSubject: principal.Subject}}))
	server.SetErrorPresenter(graph.PresentError)
	var auditsBefore int64
	if err := h.AdminDB.Model(&database.AuditEvent{}).Where("action=?", "video.admission_configured").Count(&auditsBefore).Error; err != nil {
		t.Fatal(err)
	}
	response := executeTask06GraphQL(t, server, `mutation { configureVideoAdmission(input:{enabled:false,expectedVersion:3,reason:"Capacity incident",clientMutationId:"vv-it531-stale"}) { admission { enabled version } userErrors { code } clientMutationId } }`, ctx)
	if !strings.Contains(response, "CONFLICT") || strings.Contains(response, `"admission":{`) {
		t.Fatalf("stale admission version was not rejected with null result: %s", response)
	}
	var admission database.VideoAdmission
	if err := h.AdminDB.Where("id=1").Take(&admission).Error; err != nil {
		t.Fatal(err)
	}
	var auditsAfter int64
	if err := h.AdminDB.Model(&database.AuditEvent{}).Where("action=?", "video.admission_configured").Count(&auditsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if !admission.Enabled || admission.Version != 4 || auditsAfter != auditsBefore {
		t.Fatalf("stale admission update changed state: enabled=%t version=%d audits %d→%d", admission.Enabled, admission.Version, auditsBefore, auditsAfter)
	}
}
