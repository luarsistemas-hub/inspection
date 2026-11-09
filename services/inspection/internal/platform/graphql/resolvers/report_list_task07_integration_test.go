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
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestTask07ReportHistoryUsesRuntimeTenantRLSAndEmployeeScope(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx, h, fixtureA, resolver, created := newTask04ResolverFixture(t)
	fixtureB, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	if err != nil {
		t.Fatal(err)
	}
	var reportA database.ReportSnapshot
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixtureA.TenantID, created.SnapshotID).Take(&reportA).Error; err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(map[string]any{
		"context":    map[string]any{"asset": map[string]any{"name": "Laudo do tenant B", "address": "Endereço B", "externalKey": "B"}, "participant": map[string]any{"name": "Pessoa B"}},
		"checklist":  map[string]any{"coverage": map[string]any{"expected": 1, "unknownScope": false}, "rooms": []any{}},
		"processing": map[string]any{"status": "COMPLETED"},
	})
	if err != nil {
		t.Fatal(err)
	}
	reportB := reportA
	reportB.ID, reportB.TenantID, reportB.InspectionID = identity.NewID(), fixtureB.TenantID, fixtureB.InspectionID
	reportB.VersionNumber, reportB.CanonicalJSON, reportB.CreatedAt = 1, canonical, time.Now().UTC()
	reportB.JSONDigest, reportB.HTMLDigest = "task07-tenant-b-json", "task07-tenant-b-html"
	if err := h.AdminDB.Create(&reportB).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.Membership{}).Where("tenant_id=? AND id=?", fixtureA.TenantID, fixtureA.MembershipID).Update("role", auth.Employee).Error; err != nil {
		t.Fatal(err)
	}
	meta := requestctx.Metadata{TenantID: fixtureA.TenantID, Principal: requestctx.Principal{
		IdentityID: fixtureA.IdentityID, MembershipID: fixtureA.MembershipID, TenantID: fixtureA.TenantID,
		Roles: []string{auth.Employee}, ProductEntitlements: auth.ProductsForRole(auth.Employee),
		Scopes: []requestctx.Scope{{Kind: "INSPECTION", ID: fixtureA.InspectionID}},
	}}
	ctx = requestctx.WithMetadata(context.Background(), meta)
	first := 100
	visible, err := resolver.Reports(ctx, &first, nil, nil, nil, nil)
	if err != nil || visible == nil || len(visible.Nodes) != 1 || visible.Nodes[0].InspectionID != fixtureA.InspectionID.String() {
		t.Fatalf("employee-scoped reports=%+v err=%v", visible, err)
	}
	otherID := fixtureB.InspectionID.String()
	crossTenant, err := resolver.Reports(ctx, &first, nil, nil, nil, &otherID)
	if err != nil || crossTenant == nil || len(crossTenant.Nodes) != 0 {
		t.Fatalf("tenant A received tenant B history: %+v err=%v", crossTenant, err)
	}
	meta.Principal.Scopes = nil
	unscoped := requestctx.WithMetadata(context.Background(), meta)
	inspectionA := fixtureA.InspectionID.String()
	withoutScope, err := resolver.Reports(unscoped, &first, nil, nil, nil, &inspectionA)
	if err != nil || withoutScope == nil || len(withoutScope.Nodes) != 0 {
		t.Fatalf("employee without inspection scope received history: %+v err=%v", withoutScope, err)
	}
	invalid := "not-an-id"
	if _, err := resolver.Reports(ctx, &first, nil, nil, nil, &invalid); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("invalid inspectionId error=%v, want safe input error", err)
	}
}
