package migrations

import (
	"strings"
	"testing"
)

func TestPlannerContractsUT056UT057(t *testing.T) {
	steps := []Step{{Version: 2, Name: "b", SQL: "SELECT 2", Compatible: true}, {Version: 1, Name: "a", SQL: "SELECT 1", Compatible: true}}
	plan, err := Plan(steps, nil)
	if err != nil || len(plan) != 2 || plan[0].Version != 1 {
		t.Fatalf("plan=%v err=%v", plan, err)
	}
	if _, err := Plan([]Step{steps[0], steps[0]}, nil); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := Plan([]Step{{Version: 3, Name: "drop", SQL: "DROP TABLE x", Destructive: true}}, nil); err == nil {
		t.Fatal("unsafe destructive accepted")
	}
	if _, err := Plan([]Step{steps[0]}, map[int]string{2: "changed"}); err == nil {
		t.Fatal("checksum drift accepted")
	}
}

func TestLatestVersionUsesFoundationCatalog(t *testing.T) {
	steps := Foundation()
	latest := 0
	for _, step := range steps {
		if step.Version > latest {
			latest = step.Version
		}
	}
	if got := LatestVersion(); got != latest {
		t.Fatalf("latest version=%d, want %d", got, latest)
	}
}

func TestLLMCallLedgerMigrationIsVersion40AndTenantScoped(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 40 {
			step = candidate
			break
		}
	}
	if step.Name != "llm_call_ledger" {
		t.Fatalf("ledger migration=%+v", step)
	}
	for _, required := range []string{
		"usage.llm_calls",
		"ENABLE ROW LEVEL SECURITY",
		"FORCE ROW LEVEL SECURITY",
		"idx_llm_calls_inspection_cursor",
		"idx_llm_calls_started",
		"guard_llm_call_mutation",
		"NEW.state <> 'FINISHED'",
		"GRANT SELECT, INSERT, UPDATE, DELETE",
	} {
		if !strings.Contains(step.SQL, required) {
			t.Fatalf("ledger migration does not contain %q", required)
		}
	}
	if got := LatestVersion(); got != 41 {
		t.Fatalf("latest version=%d, want 41", got)
	}
}

func TestGlobalLLMUsageMigrationAddsRestrictedReaders(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 41 {
			step = candidate
			break
		}
	}
	if step.Name != "global_llm_usage_readers" {
		t.Fatalf("global usage migration=%+v", step)
	}
	for _, required := range []string{
		"idx_llm_calls_global_cursor",
		"idx_llm_calls_tenant_cursor",
		"idx_llm_calls_mode_cursor",
		"usage.read_llm_usage_summary",
		"usage.read_llm_usage_calls",
		"usage.read_llm_usage_tenants",
		"SECURITY DEFINER",
		"SET search_path = usage, inspections, tenancy, platform, pg_catalog",
		"REVOKE ALL ON FUNCTION",
		"GRANT EXECUTE ON FUNCTION",
	} {
		if !strings.Contains(step.SQL, required) {
			t.Fatalf("global usage migration does not contain %q", required)
		}
	}
}

func TestOnboardingEmailCoordinationMigrationUsesRestrictedDatabaseBoundary(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 29 {
			step = candidate
			break
		}
	}
	if step.Version != 29 {
		t.Fatal("onboarding coordination migration is missing")
	}
	for _, required := range []string{
		"onboarding.email_states",
		"onboarding.prepare_email_attempt",
		"onboarding.promote_email_session",
		"onboarding.is_current_verified_session",
		"SECURITY DEFINER",
		"SET search_path = onboarding, pg_catalog",
		"REVOKE ALL ON FUNCTION",
	} {
		if !strings.Contains(step.SQL, required) {
			t.Fatalf("migration does not contain %q", required)
		}
	}
}

func TestOnboardingAgencyLookupRequiresLocatorProof(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 30 {
			step = candidate
			break
		}
	}
	if step.Version != 30 {
		t.Fatal("onboarding locator-proof migration is missing")
	}
	for _, required := range []string{"DROP FUNCTION IF EXISTS onboarding.load_session_agency(uuid)", "load_session_agency(uuid, bytea)", "session_locator_digest = p_locator_digest", "REVOKE ALL ON FUNCTION onboarding.load_session_agency(uuid, bytea)"} {
		if !strings.Contains(step.SQL, required) {
			t.Fatalf("migration does not contain %q", required)
		}
	}
}

func TestPendingMembershipStatusFitsPersistedColumn(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 31 {
			step = candidate
			break
		}
	}
	if step.Version != 31 {
		t.Fatal("membership status width migration is missing")
	}
	if !strings.Contains(step.SQL, "access.memberships ALTER COLUMN status TYPE varchar(32)") {
		t.Fatal("membership status width migration does not support pending activation")
	}
}

func TestOnboardingRequestTracksInspectionOncePerSession(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 32 {
			step = candidate
			break
		}
	}
	if step.Version != 32 {
		t.Fatal("onboarding request inspection migration is missing")
	}
	for _, required := range []string{"inspection_id uuid", "idx_onboarding_request_session", "session_id"} {
		if !strings.Contains(step.SQL, required) {
			t.Fatalf("migration does not contain %q", required)
		}
	}
}
