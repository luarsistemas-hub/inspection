//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
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

func TestITInspectionCoverageAuthorizationTenantIsolationAndPinnedSummary(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	a, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{SchemaVersion: 3, CaptureDraftStatus: "OPEN"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{SchemaVersion: 3, CaptureDraftStatus: "OPEN"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, f := range []harness.Task06Fixture{a, b} {
		document, _ := json.Marshal(map[string]any{"rooms": []any{map[string]any{"key": "kitchen", "typeCode": "KITCHEN", "name": "Cozinha", "components": []any{map[string]any{"key": "floor", "label": "Piso", "views": []string{"geral"}}}}}, "handoverRecords": []any{}})
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			return tx.Create(&database.InspectionScopeSnapshot{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, TemplateVersionID: f.TemplateVersionID, Profile: "PROPERTY_COMPONENTS", Document: document, Digest: identity.NewID().String(), Version: 1, CreatedAt: now}).Error
		}); err != nil {
			t.Fatal(err)
		}
	}
	resolver := &queryResolver{Resolver: &Resolver{DB: h.DB, WithinTenant: h.WithinTenant, Authorizer: auth.Authorizer{}}}
	manager := requestctx.Principal{IdentityID: identity.NewID(), TenantID: a.TenantID, Roles: []string{auth.Manager}, ProductEntitlements: []string{auth.DashboardProduct}, Scopes: []requestctx.Scope{{Kind: "INSPECTION", ID: a.InspectionID}}}
	managerCtx := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: a.TenantID, Principal: manager})
	got, err := resolver.InspectionCoverage(managerCtx, a.InspectionID.String())
	if err != nil || got == nil || got.Expected != 1 || got.NotObserved != 1 || got.UnknownScope {
		t.Fatalf("authorized pinned coverage = %+v, %v", got, err)
	}
	if _, err := resolver.InspectionCoverage(managerCtx, b.InspectionID.String()); err == nil {
		t.Fatal("cross-tenant inspection coverage was returned")
	}
	unauthorized := manager
	unauthorized.Roles = []string{"TENANT_PARTICIPANT"}
	unauthorizedCtx := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: a.TenantID, Principal: unauthorized})
	if _, err := resolver.InspectionCoverage(unauthorizedCtx, a.InspectionID.String()); err == nil {
		t.Fatal("participant was authorized to read dashboard coverage")
	}
}
