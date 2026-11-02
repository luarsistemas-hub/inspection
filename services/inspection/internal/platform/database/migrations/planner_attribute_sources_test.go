package migrations

import (
	"strings"
	"testing"
)

func TestInventoryAttributeSourceLineage112IsCompatibleAndKeepsRolesUntouched(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 112 {
			step = candidate
			break
		}
	}
	if step.Name != "inventory_attribute_source_lineage" || !step.Compatible || step.Destructive {
		t.Fatalf("unexpected migration 112: %+v", step)
	}
	for _, required := range []string{
		"fk_attribute_source_item",
		"fk_attribute_source_origin",
		"fk_attribute_source_visual",
		"ON DELETE CASCADE",
		"GRANT DELETE ON inventory.item_attribute_sources TO inspection_runtime",
		"pg_trigger_depth()=1",
		"'DISCARD','REGENERATE'",
		"DROP TRIGGER IF EXISTS human_review_source_append_only",
	} {
		if !strings.Contains(step.SQL, required) {
			t.Errorf("migration 112 missing %q", required)
		}
	}
	if strings.Contains(step.SQL, "ALTER ROLE") || strings.Contains(step.SQL, "PASSWORD") || strings.Contains(step.SQL, "DROP TABLE") {
		t.Fatal("migration 112 must not change roles or drop data")
	}
}
