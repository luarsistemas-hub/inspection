package migrations

import (
	"strings"
	"testing"
)

func TestCatalogNoteManifestSealing109IsIdempotentAndKeepsRolesUntouched(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 109 {
			step = candidate
			break
		}
	}
	if step.Name != "catalog_note_manifest_sealing" || !step.Compatible || step.Destructive {
		t.Fatalf("unexpected migration 109: %+v", step)
	}
	for _, required := range []string{
		"ADD COLUMN IF NOT EXISTS sealed_at timestamptz",
		"GRANT UPDATE (note_revisions, missing_count, digest, sealed_at) ON analysis.catalog_note_manifests TO inspection_runtime",
		"CREATE OR REPLACE FUNCTION analysis.reject_sealed_catalog_manifest_change()",
		"DROP TRIGGER IF EXISTS sealed_catalog_manifest_immutable",
		"CREATE TRIGGER sealed_catalog_manifest_immutable BEFORE UPDATE",
		"IF OLD.sealed_at IS NOT NULL THEN RAISE EXCEPTION",
	} {
		if !strings.Contains(step.SQL, required) {
			t.Errorf("migration 109 missing %q", required)
		}
	}
	if strings.Contains(step.SQL, "ALTER ROLE") || strings.Contains(step.SQL, "PASSWORD") || strings.Contains(step.SQL, "DROP TABLE") {
		t.Fatal("migration 109 must not change roles or drop data")
	}
	versions := map[int]bool{}
	for _, candidate := range Foundation() {
		if versions[candidate.Version] {
			t.Fatalf("duplicate migration version %d", candidate.Version)
		}
		versions[candidate.Version] = true
	}
}
