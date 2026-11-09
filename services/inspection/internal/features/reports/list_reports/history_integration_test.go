//go:build integration

package list_reports

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
)

func TestInspectionHistoryPostgresReturnsEveryVersionWithinTenant(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewPostgresFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	first, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, item := range []struct {
		fixture harness.Task06Fixture
		version int
	}{{first, 1}, {first, 2}, {second, 1}} {
		fixture, version := item.fixture, item.version
		facts, _ := json.Marshal(map[string]any{
			"checklist": map[string]any{
				"coverage": map[string]any{"expected": 1, "unknownScope": false},
				"rooms":    []any{map[string]any{"components": []any{map[string]any{"reviewStatus": "NOT_REVIEWED", "description": "não deve ser lida"}}}},
			},
			"processing": map[string]any{"status": "COMPLETED"},
		})
		row := database.ReportSnapshot{
			ID: identity.NewID(), TenantID: fixture.TenantID, InspectionID: fixture.InspectionID,
			VersionNumber: version, Classification: "NORMAL", CanonicalJSON: facts,
			HTML: json.RawMessage(`"<p>excluído da projeção</p>"`), CreatedAt: now.Add(time.Duration(version) * time.Second),
		}
		if err := h.AdminDB.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	operation, err := Setup(Dependencies{DB: h.AdminDB})
	if err != nil {
		t.Fatal(err)
	}
	result, err := operation(ctx, Input{TenantID: first.TenantID, InspectionID: first.InspectionID, TenantAdmin: true, First: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 || result.Items[0].Version != 2 || result.Items[1].Version != 1 {
		t.Fatalf("history versions = %#v, want [2,1]", result.Items)
	}
	for _, item := range result.Items {
		if item.InspectionID != first.InspectionID || len(item.HistoryFacts) == 0 || string(item.HistoryFacts) == "null" {
			t.Fatalf("history escaped requested inspection or omitted lightweight facts: %#v", item)
		}
		if !bytes.Contains(item.HistoryFacts, []byte("coverage")) || !bytes.Contains(item.HistoryFacts, []byte("NOT_REVIEWED")) || bytes.Contains(item.HistoryFacts, []byte("description")) || bytes.Contains(item.HistoryFacts, []byte("excluído da projeção")) {
			t.Fatalf("history facts are not the light JSON projection: %s", item.HistoryFacts)
		}
	}
	other, err := operation(ctx, Input{TenantID: second.TenantID, InspectionID: second.InspectionID, TenantAdmin: true, First: 10})
	if err != nil || len(other.Items) != 1 || other.Items[0].Version != 1 {
		t.Fatalf("other tenant history = %#v, err=%v", other, err)
	}
}
