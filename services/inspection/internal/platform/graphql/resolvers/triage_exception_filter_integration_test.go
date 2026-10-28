//go:build integration

package resolvers

import (
	"context"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
)

// TestTriageWorkspaceListsOnlyExceptionsBeforePagination keeps NORMAL cases
// out of the default triage queue on the server, so pages are never empty.
func TestTriageWorkspaceListsOnlyExceptionsBeforePagination(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fx, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	var base database.Inspection
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fx.TenantID, fx.InspectionID).Take(&base).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	want := map[string]string{}
	for index, classification := range []string{"NORMAL", "ATTENTION", "NORMAL", "CRITICAL", "NORMAL"} {
		inspection := base
		inspection.ID = identity.NewID()
		inspection.SourceKey = base.SourceKey + "-triage-" + inspection.ID.String()
		if err := h.AdminDB.Create(&inspection).Error; err != nil {
			t.Fatal(err)
		}
		updated := now.Add(time.Duration(index) * time.Second)
		if err := h.AdminDB.Create(&database.TriageCase{ID: identity.NewID(), TenantID: fx.TenantID, InspectionID: inspection.ID, Status: "NEW", Classification: classification, ReasonCodes: []byte(`[]`), Version: 1, UpdatedAt: updated, CreatedAt: updated}).Error; err != nil {
			t.Fatal(err)
		}
		if classification != "NORMAL" {
			want[inspection.ID.String()] = classification
		}
	}
	metadata := requestctx.Metadata{TenantID: fx.TenantID, Principal: requestctx.Principal{IdentityID: fx.IdentityID, MembershipID: fx.MembershipID, TenantID: fx.TenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: []string{auth.DashboardProduct}}}
	query := &queryResolver{&Resolver{DB: h.DB}}
	first := 1
	var after *string
	seen := map[string]string{}
	for page := 0; page < 5; page++ {
		workspace, err := query.triageWorkspace(requestctx.WithMetadata(ctx, metadata), &first, after, nil, nil, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(workspace.Nodes) == 0 && workspace.PageInfo.HasNextPage {
			t.Fatalf("page %d is empty but reports more pages", page)
		}
		for _, node := range workspace.Nodes {
			if node.Classification == "NORMAL" {
				t.Fatalf("triage listed a NORMAL case: %+v", node)
			}
			seen[node.InspectionID] = node.Classification
		}
		if !workspace.PageInfo.HasNextPage {
			break
		}
		after = workspace.PageInfo.EndCursor
	}
	for inspectionID, classification := range want {
		if seen[inspectionID] != classification {
			t.Fatalf("exception %s (%s) missing from triage pages: %v", inspectionID, classification, seen)
		}
	}
}
