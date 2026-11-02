package migrations

import (
	"strings"
	"testing"
)

func TestRetentionClockAndClassPurges113IsCompatibleAndKeepsRolesUntouched(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 113 {
			step = candidate
			break
		}
	}
	if step.Name != "retention_clock_project_closure_and_class_purges" || !step.Compatible || step.Destructive {
		t.Fatalf("unexpected migration 113: %+v", step)
	}
	for _, required := range []string{
		"ADD COLUMN IF NOT EXISTS closed_at timestamptz",
		"UPDATE projects.projects SET closed_at=updated_at WHERE status='CLOSED' AND closed_at IS NULL",
		"ADD COLUMN IF NOT EXISTS secrets_purged_at timestamptz",
		"idx_outbox_retention_purge_due",
		"WHERE type='retention.purge_due.v1'",
		"app.retention_location",
		"CREATE OR REPLACE FUNCTION platform.discover_scheduler_tenants(",
		"WITH eff AS (SELECT DISTINCT ON (rp.tenant_id)",
		"ORDER BY rp.tenant_id, rp.version DESC)",
		"LEFT JOIN eff e ON e.tenant_id=i.tenant_id",
		"COALESCE(e.evidence_days,1825)",
		"idx_retention_policies_effective ON retention.policies(tenant_id, version DESC)",
		"i.updated_at<=p_now-interval '24 hours'",
		"GREATEST(COALESCE(pr.closed_at, pr.updated_at), i.updated_at)",
		"pr.status='CLOSED'",
		"o.created_at>p_now-interval '24 hours'",
		"GRANT EXECUTE ON FUNCTION platform.discover_scheduler_tenants(timestamptz,timestamptz,timestamptz) TO inspection_runtime",
	} {
		if !strings.Contains(step.SQL, required) {
			t.Errorf("migration 113 missing %q", required)
		}
	}
	for _, forbidden := range []string{"ALTER ROLE", "CREATE ROLE", "PASSWORD", "DROP COLUMN", "DROP TABLE"} {
		if strings.Contains(step.SQL, forbidden) {
			t.Errorf("migration 113 must not contain %q", forbidden)
		}
	}
	// The discovery function keeps its signature but must no longer compare
	// terminal inspections with the fixed five-year cutoff.
	// The effective policy is computed once per tenant, never per inspection.
	if strings.Contains(step.SQL, "SELECT rp.evidence_days FROM retention.policies") {
		t.Error("migration 113 evaluates the effective policy with a per-row subquery")
	}
	if strings.Contains(step.SQL, "i.updated_at<=p_terminal_updated_before") {
		t.Error("migration 113 still compares terminal inspections with the fixed cutoff argument")
	}
}
