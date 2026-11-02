package migrations

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestLegacyMigrationChecksumsThrough95AreImmutable(t *testing.T) {
	const want = "a8a433a04f55dd8dc3656ef9dec1d8f21b381d0c83a317a992f011f91762b6dd"
	h := sha256.New()
	for _, step := range Foundation() {
		if step.Version <= 95 {
			_, _ = fmt.Fprintf(h, "%d:%s\n", step.Version, step.Checksum())
		}
	}
	if got := fmt.Sprintf("%x", h.Sum(nil)); got != want {
		t.Fatalf("legacy migration checksum aggregate changed: got %s, want %s", got, want)
	}
}

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

func TestInternalUserRecipientChannelBackfill107IsIdempotent(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 107 {
			step = candidate
			break
		}
	}
	if step.Name != "internal_user_verified_email_recipient_channel" || !step.Compatible {
		t.Fatalf("unexpected internal recipient migration: %+v", step)
	}
	for _, required := range []string{
		"DELETE FROM notifications.recipient_channels AS duplicate",
		"PARTITION BY tenant_id, recipient_membership_id, channel, destination",
		"ORDER BY verified_at IS NULL, NOT selected, id",
		"ranked.rn > 1",
		"access.user_invitations",
		"invitation.accepted_at",
		"membership.status='ACTIVE'",
		"membership.role <> 'CUSTOMER_VIEWER'",
		"lower(btrim(invitation.email, E' \\t\\r\\n'))",
		"existing_channel.channel='EMAIL'",
		"CREATE UNIQUE INDEX IF NOT EXISTS uq_recipient_channel",
		"ON CONFLICT (tenant_id, recipient_membership_id, channel, destination) DO NOTHING",
	} {
		if !strings.Contains(step.SQL, required) {
			t.Errorf("migration 107 missing %q", required)
		}
	}
	if strings.Index(step.SQL, "DELETE FROM notifications.recipient_channels") > strings.Index(step.SQL, "CREATE UNIQUE INDEX") {
		t.Fatal("migration must deduplicate recipient channels before creating the unique index")
	}
	if LatestVersion() != 116 {
		t.Fatalf("LatestVersion()=%d, want 116", LatestVersion())
	}
}

func TestTranscriptionAudioDeletionAttempts108IsIdempotentAndDiscoversDueTenants(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 108 {
			step = candidate
			break
		}
	}
	if step.Name != "transcription_audio_deletion_attempts" || !step.Compatible {
		t.Fatalf("unexpected audio deletion migration: %+v", step)
	}
	for _, required := range []string{
		"ADD COLUMN IF NOT EXISTS attempts integer NOT NULL DEFAULT 0",
		"EXCEPTION WHEN duplicate_object THEN NULL",
		"CREATE INDEX IF NOT EXISTS idx_audio_notes_retention_due",
		"CREATE OR REPLACE FUNCTION transcription.discover_due_tenants(p_retention interval)",
		"SECURITY DEFINER SET search_path=pg_catalog",
		"j.generation=1",
		"j.status='PROCESSING' AND j.lease_deadline<=now()",
		"d.status IN ('PENDING','FAILED')",
		"REVOKE ALL ON FUNCTION transcription.discover_due_tenants(interval) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION transcription.discover_due_tenants(interval) TO inspection_runtime",
	} {
		if !strings.Contains(step.SQL, required) {
			t.Errorf("migration 108 missing %q", required)
		}
	}
	if strings.Contains(step.SQL, "ALTER ROLE") || strings.Contains(step.SQL, "PASSWORD") {
		t.Fatal("migration 108 must not change database roles")
	}
}

func TestReportPublicationInvalidatedBy110IsIdempotent(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 110 {
			step = candidate
			break
		}
	}
	if step.Name != "report_publication_invalidated_by" || !step.Compatible {
		t.Fatalf("unexpected publication audit migration: %+v", step)
	}
	if !strings.Contains(step.SQL, "ADD COLUMN IF NOT EXISTS invalidated_by uuid") {
		t.Fatalf("migration 110 must add invalidated_by idempotently: %s", step.SQL)
	}
	for _, required := range []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS uq_report_publication_snapshot ON reports.report_publications (tenant_id, snapshot_id)",
		"CREATE UNIQUE INDEX IF NOT EXISTS uq_report_publication_mutation ON reports.report_publications (tenant_id, client_mutation_id) WHERE client_mutation_id <> ''",
	} {
		if !strings.Contains(step.SQL, required) {
			t.Errorf("migration 110 missing %q", required)
		}
	}
	if strings.Contains(step.SQL, "ALTER ROLE") || strings.Contains(step.SQL, "PASSWORD") {
		t.Fatal("migration 110 must not change database roles")
	}
}

func TestTFE_IT_198CustomerAdminEntitlementGuard111IsIdempotentAndCompatible(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 111 {
			step = candidate
			break
		}
	}
	if step.Name != "customer_membership_forbids_admin_entitlement" || !step.Compatible {
		t.Fatalf("unexpected customer guard migration: %+v", step)
	}
	for _, required := range []string{
		"DELETE FROM access.product_entitlements",
		"CREATE OR REPLACE FUNCTION access.forbid_customer_admin_entitlement()",
		"DROP TRIGGER IF EXISTS customer_admin_entitlement_guard ON access.product_entitlements",
		"DROP TRIGGER IF EXISTS customer_role_admin_entitlement_guard ON access.memberships",
		"DEFERRABLE INITIALLY DEFERRED",
	} {
		if !strings.Contains(step.SQL, required) {
			t.Errorf("migration 111 missing %q", required)
		}
	}
	if strings.Contains(step.SQL, "ALTER ROLE") || strings.Contains(step.SQL, "PASSWORD") {
		t.Fatal("migration 111 must not change database roles")
	}
}

func TestTFE_AccessInvitationReceipts114IsTenantIsolatedAndIdempotent(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 114 {
			step = candidate
		}
	}
	if step.Name != "access_invitation_receipts" || !step.Compatible {
		t.Fatalf("unexpected receipts migration: %+v", step)
	}
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS access.invitation_receipts",
		"PRIMARY KEY (tenant_id, client_mutation_id)",
		"ALTER TABLE access.invitation_receipts FORCE ROW LEVEL SECURITY",
		"DROP POLICY IF EXISTS tenant_isolation ON access.invitation_receipts",
		"GRANT SELECT, INSERT, UPDATE ON access.invitation_receipts TO inspection_runtime",
	} {
		if !strings.Contains(step.SQL, required) {
			t.Errorf("migration 114 missing %q", required)
		}
	}
	if strings.Contains(step.SQL, "ALTER ROLE") || strings.Contains(step.SQL, "PASSWORD") {
		t.Fatal("migration 114 must not change database roles")
	}
	var guard Step
	for _, candidate := range Foundation() {
		if candidate.Version == 111 {
			guard = candidate
		}
	}
	if !strings.Contains(guard.SQL, "SET LOCAL row_security = off") || strings.Index(guard.SQL, "SET LOCAL row_security = off") > strings.Index(guard.SQL, "DELETE FROM access.product_entitlements") {
		t.Fatal("migration 111 must disable row security before its normalizing DELETE")
	}
}

func TestTranscriptionContractsUT025(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Name == "transcription_contracts" {
			step = candidate
			break
		}
	}
	if step.Version != 99 || !step.Compatible {
		t.Fatalf("transcription migration=%+v", step)
	}
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS transcription.tenant_policies",
		"CREATE TABLE IF NOT EXISTS transcription.audio_notes",
		"CREATE TABLE IF NOT EXISTS transcription.jobs",
		"CREATE TABLE IF NOT EXISTS transcription.transcript_revisions",
		"CREATE TABLE IF NOT EXISTS transcription.audio_deletions",
		"CREATE TABLE IF NOT EXISTS analysis.catalog_note_manifests",
		"CREATE TABLE IF NOT EXISTS inventory.item_attribute_sources",
		"UNIQUE NULLS NOT DISTINCT (tenant_id,item_id,attribute_name,source_kind,note_id,revision_id,visual_evidence_id)",
		"FOREIGN KEY(tenant_id,id,current_revision_id) REFERENCES transcription.transcript_revisions(tenant_id,note_id,id)",
		"FOREIGN KEY(tenant_id,note_id,revision_id) REFERENCES transcription.transcript_revisions(tenant_id,note_id,id)",
		"FORCE ROW LEVEL SECURITY",
		"CREATE POLICY tenant_isolation",
		"'TRANSCRIPTION'",
		"audio_duration_ms bigint",
	} {
		if !strings.Contains(step.SQL, required) {
			t.Errorf("transcription migration missing %q", required)
		}
	}
	if LatestVersion() != 116 {
		t.Fatalf("LatestVersion()=%d, want 116", LatestVersion())
	}
}

func TestVoiceMigrationsAddColumnsIdempotentlyUT027(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version < 99 {
			continue
		}
		for _, statement := range strings.Split(step.SQL, ";") {
			upper := strings.ToUpper(strings.Join(strings.Fields(statement), " "))
			if strings.Contains(upper, "ADD COLUMN") && !strings.Contains(upper, "ADD COLUMN IF NOT EXISTS") {
				t.Errorf("migration %d (%s) has non-idempotent ADD COLUMN: %s", step.Version, step.Name, strings.TrimSpace(statement))
			}
		}
	}
}

func TestPlannerRejectsDuplicateTranscriptionVersionBeforeReturningStepsUT026(t *testing.T) {
	steps := []Step{
		{Version: 99, Name: "transcription_contracts", SQL: "SELECT 1", Compatible: true},
		{Version: 99, Name: "duplicate_transcription_contracts", SQL: "SELECT 2", Compatible: true},
	}
	plan, err := Plan(steps, nil)
	if err == nil || !strings.Contains(err.Error(), "duplicate version 99") {
		t.Fatalf("plan=%v err=%v, want duplicate-version error", plan, err)
	}
	if plan != nil {
		t.Fatalf("duplicate plan must not expose applicable steps: %v", plan)
	}
}

func TestInspectionOriginPromotionMigrationIsTenantScopedAndNonMutating(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 94 {
			step = candidate
			break
		}
	}
	if step.Name != "inspection_origin_promotions" || !step.Compatible {
		t.Fatalf("unexpected promotion migration: %+v", step)
	}
	for _, required := range []string{
		"CREATE TABLE origins.inspection_origin_promotions",
		"uq_inspection_origin_promotion_request ON origins.inspection_origin_promotions(tenant_id,inspection_id,client_mutation_id)",
		"WHERE status IN ('PREPARING','ACTIVATING')",
		"idx_inspection_origin_promotion_reconciliation ON origins.inspection_origin_promotions(status,updated_at)",
		"FOREIGN KEY(tenant_id,asset_id) REFERENCES assets.assets(tenant_id,id)",
		"FOREIGN KEY(tenant_id,inspection_id) REFERENCES inspections.inspections(tenant_id,id)",
		"FOREIGN KEY(tenant_id,draft_version_id) REFERENCES origins.origin_versions(tenant_id,id)",
		"FOREIGN KEY(catalog_prompt_snapshot_id) REFERENCES analysis.prompt_snapshots(id)",
		"chk_origin_version_source CHECK(source IN ('CAPTURE','LEGACY_ORIGIN','COMPARATIVE_INSPECTION','CONTESTATION','INSPECTION_PROMOTION'))",
		"auto_accepted_by_promotion_id uuid",
		"blocked_reason text",
		"blocked_at timestamptz",
		"ENABLE ROW LEVEL SECURITY",
		"FORCE ROW LEVEL SECURITY",
		"GRANT SELECT,INSERT,UPDATE ON origins.inspection_origin_promotions TO inspection_runtime",
	} {
		if !strings.Contains(step.SQL, required) {
			t.Errorf("promotion migration is missing %q", required)
		}
	}
	if strings.Contains(step.SQL, "\nUPDATE ") || strings.Contains(step.SQL, "\nDELETE FROM ") {
		t.Fatal("promotion migration must not mutate existing rows")
	}
	if LatestVersion() != 116 {
		t.Fatalf("LatestVersion()=%d, want 116", LatestVersion())
	}
}

func TestInspectionOriginPromotionReconciliationDiscoveryIsRuntimeScoped(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 95 {
			step = candidate
			break
		}
	}
	if step.Name != "inspection_origin_promotion_reconciliation" || !step.Compatible {
		t.Fatalf("unexpected reconciliation migration: %+v", step)
	}
	for _, required := range []string{"SECURITY DEFINER", "p.status='PREPARING'", "p.status='ACTIVATING'", "GRANT EXECUTE ON FUNCTION platform.discover_inspection_origin_promotion_tenants(timestamptz) TO inspection_runtime"} {
		if !strings.Contains(step.SQL, required) {
			t.Errorf("reconciliation migration missing %q", required)
		}
	}
}

func TestBaselineRetentionRepairMigrationUsesSuccessorActivationAndDiscoversWork(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 91 {
			step = candidate
			break
		}
	}
	if step.Name != "baseline_retention_scheduler_repairs" {
		t.Fatalf("migration=%+v", step)
	}
	for _, required := range []string{"successor.supersedes_id=v.id", "successor.activated_at", "idx_counterparty_pending_deadline", "media.video_sources", "retention.deletion_requests", "has_inventory", "responsibility_id IS NULL"} {
		if !strings.Contains(step.SQL, required) {
			t.Errorf("migration is missing retention/scheduler repair %q", required)
		}
	}
	if strings.Contains(step.SQL, "COALESCE(activated_at,created_at,CURRENT_TIMESTAMP)") {
		t.Fatal("superseded_at backfill must not use a version's own activation timestamp")
	}
}

func TestRetentionSchedulerMigrationUsesDueWindowsAndHoldFilters(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 92 {
			step = candidate
			break
		}
	}
	if step.Name != "retention_scheduler_due_windows" {
		t.Fatalf("migration=%+v", step)
	}
	for _, required := range []string{"superseded_at", "superseded_baseline_retention_days", "retention.legal_holds", "counterparty_invitations", "ov.status='ACTIVE'", "source_expiry_at<=p_now"} {
		if !strings.Contains(step.SQL, required) {
			t.Errorf("migration is missing scheduler predicate %q", required)
		}
	}
}

func TestSensitiveReviewMigrationReconcilesTerminalPreparationGenerically(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 78 {
			step = candidate
			break
		}
	}
	if step.Name != "sensitive_review_and_recoverable_video_plans" {
		t.Fatalf("migration=%+v", step)
	}
	for _, required := range []string{"chk_inspection_status", "'REVIEW_REQUIRED'", "'PROCESSING_FAILED'", "'REPLACEMENT_PENDING'", "chk_video_plan_status", "'BLOCKED'", "current_p.status IN ('FAILED','CANCELED')", "origin_p.status IN ('FAILED','CANCELED')", "current_p.error_code", "origin_p.error_code", "i.status='ANALYZING'", "version=i.version + 1", "platform.worker_runtime_settings", "GRANT USAGE ON SCHEMA platform TO inspection_runtime", "GRANT SELECT, INSERT, UPDATE ON platform.worker_runtime_settings TO inspection_runtime"} {
		if !strings.Contains(step.SQL, required) {
			t.Errorf("migration is missing reconciliation rule %q", required)
		}
	}
	if strings.Contains(step.SQL, "TO inspection_runtime, inspection_worker") {
		t.Fatal("migration must not assume the inspection_worker role exists")
	}
	if strings.Contains(step.SQL, "'REPLACEMENT_PENDING') AND i.deadline_at") {
		t.Fatal("replacement deadline processing must not reuse inspections.deadline_at")
	}
	if strings.Contains(step.SQL, "bcc7699a-c1ed-4210-b26d-45b271d6ee80") || strings.Contains(step.SQL, "5ab5c36c-7ddc-4ba5-800a-b3e9e75aaa59") {
		t.Fatal("migration must not encode local inspection identifiers")
	}
}

func TestSealedVideoPlanMigrationFencesFrameMembership(t *testing.T) {
	var step Step
	for _, candidate := range Foundation() {
		if candidate.Version == 57 {
			step = candidate
			break
		}
	}
	if step.Name != "sealed_video_plan_membership" {
		t.Fatalf("video plan migration=%+v", step)
	}
	for _, required := range []string{"analysis.video_plan_items", "analysis.video_plan_item_frames", "FOREIGN KEY(tenant_id,frame_id)", "idx_video_plan_tenant_id ON analysis.video_plans(tenant_id,id)", "idx_video_plan_item_tenant_id ON analysis.video_plan_items(tenant_id,id)", "ENABLE ROW LEVEL SECURITY", "CURRENT_ONLY", "TECHNICAL_FAILURE"} {
		if !strings.Contains(step.SQL, required) {
			t.Fatalf("video plan migration missing %q", required)
		}
	}
}

func TestVideoOriginPromotionPinsTheSealedAnalysisPlan(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version != 58 {
			continue
		}
		if step.Name != "pin_video_origin_to_analysis_plan" || !strings.Contains(step.SQL, "video_plan_id") || !strings.Contains(step.SQL, "REFERENCES analysis.video_plans(tenant_id,id)") {
			t.Fatalf("video origin plan-lineage migration=%+v", step)
		}
		return
	}
	t.Fatal("video origin plan-lineage migration missing")
}

func TestVideoRecaptureReceiptHasASeparateActiveState(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version != 67 {
			continue
		}
		if step.Name != "persist_received_video_recapture_state" {
			t.Fatalf("unexpected recapture receipt migration: %+v", step)
		}
		for _, required := range []string{"'RECEIVED'", "idx_recapture_one_active", "'REQUESTED','ACCESSED','RECEIVED'"} {
			if !strings.Contains(step.SQL, required) {
				t.Fatalf("recapture receipt migration missing %q", required)
			}
		}
		return
	}
	t.Fatal("video recapture receipt migration missing")
}

func TestVideoPreparationProfileGenerationMigrationReplacesPerMediaUniqueness(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version != 70 {
			continue
		}
		if step.Name != "version_video_preparations_by_profile" {
			t.Fatalf("unexpected preparation migration: %+v", step)
		}
		for _, required := range []string{"DROP CONSTRAINT IF EXISTS video_preparation_jobs_tenant_id_media_id_key", "uq_video_preparation_profile_generation", "profile_digest,generation", "uq_video_preparation_mutation", "client_mutation_id"} {
			if !strings.Contains(step.SQL, required) {
				t.Fatalf("preparation migration missing %q", required)
			}
		}
		if LatestVersion() != 116 {
			t.Fatalf("LatestVersion()=%d, want 116", LatestVersion())
		}
		return
	}
	t.Fatal("video preparation profile-generation migration missing")
}

func TestVideoPlanPreparationMigrationStoresTenantScopedLineage(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version != 72 {
			continue
		}
		if step.Name != "video_plan_preparations_and_carried_results" {
			t.Fatalf("unexpected video plan preparation migration: %+v", step)
		}
		for _, required := range []string{
			"parent_plan_id uuid", "FOREIGN KEY(tenant_id,parent_plan_id) REFERENCES analysis.video_plans(tenant_id,id)",
			"carried_result_job_id uuid", "uq_comparison_job_tenant_id ON analysis.comparison_jobs(tenant_id,id)", "REFERENCES analysis.comparison_jobs(tenant_id,id)",
			"analysis.video_plan_preparations", "PRIMARY KEY(tenant_id,plan_id,preparation_id)",
			"FOREIGN KEY(tenant_id,preparation_id) REFERENCES media.video_preparation_jobs(tenant_id,id)",
			"CHECK(role IN ('MAIN','SUPPLEMENT','ORIGIN'))", "ENABLE ROW LEVEL SECURITY", "GRANT SELECT, INSERT ON analysis.video_plan_preparations",
		} {
			if !strings.Contains(step.SQL, required) {
				t.Fatalf("video plan preparation migration missing %q", required)
			}
		}
		if LatestVersion() != 116 {
			t.Fatalf("LatestVersion()=%d, want 116", LatestVersion())
		}
		return
	}
	t.Fatal("video plan preparation migration missing")
}

func TestVideoCoverageMigrationCreatesTenantFencedDurableJobs(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version != 73 {
			continue
		}
		if step.Name != "video_recapture_coverage_jobs" {
			t.Fatalf("unexpected migration: %+v", step)
		}
		for _, required := range []string{"recapture.video_coverage_jobs", "UNIQUE(tenant_id, preparation_id)", "FOREIGN KEY(tenant_id, preparation_id)", "FOREIGN KEY(tenant_id, request_id)", "ENABLE ROW LEVEL SECURITY", "GRANT SELECT, INSERT, UPDATE"} {
			if !strings.Contains(step.SQL, required) {
				t.Fatalf("coverage migration missing %q", required)
			}
		}
		if LatestVersion() != 116 {
			t.Fatalf("LatestVersion()=%d, want 116", LatestVersion())
		}
		return
	}
	t.Fatal("video coverage migration missing")
}

func TestVideoPlanIdentityIndexesIncludeTenantAndID(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version != 74 {
			continue
		}
		if step.Name != "scope_video_plan_frame_identity_index" {
			t.Fatalf("unexpected migration: %+v", step)
		}
		for _, required := range []string{
			"DROP INDEX IF EXISTS analysis.idx_video_plan_frame_tenant_id",
			"ON analysis.video_plan_item_frames(tenant_id,id)",
			"DROP INDEX IF EXISTS media.idx_video_evidence_reference_id",
			"ON media.video_evidence_references(tenant_id,id)",
		} {
			if !strings.Contains(step.SQL, required) {
				t.Fatalf("identity index migration missing %q", required)
			}
		}
		if LatestVersion() != 116 {
			t.Fatalf("LatestVersion()=%d, want 116", LatestVersion())
		}
		return
	}
	t.Fatal("video plan identity index migration missing")
}

func TestRecaptureUnresolvedMigrationAllowsTerminalGapState(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version != 75 {
			continue
		}
		if step.Name != "recapture_unresolved_requirements" || !strings.Contains(step.SQL, "'UNRESOLVED'") {
			t.Fatalf("unexpected unresolved requirement migration: %+v", step)
		}
		return
	}
	t.Fatal("unresolved requirement migration missing")
}

func TestRetentionVideoPurgeMigrationGrantsOnlyRequiredDeletes(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version != 76 {
			continue
		}
		if step.Name != "retention_video_purge_delete_grants" {
			t.Fatalf("unexpected retention purge grant migration: %+v", step)
		}
		for _, required := range []string{
			"GRANT DELETE ON analysis.video_plan_preparations, analysis.video_plan_items, analysis.video_plan_item_frames, analysis.video_plans TO inspection_runtime",
			"GRANT DELETE ON media.video_preparation_jobs, media.video_scope_items, media.video_frames TO inspection_runtime",
		} {
			if !strings.Contains(step.SQL, required) {
				t.Fatalf("retention purge migration missing %q", required)
			}
		}
		if strings.Contains(step.SQL, "GRANT ALL") || strings.Contains(step.SQL, "UPDATE") || strings.Contains(step.SQL, "INSERT") {
			t.Fatalf("retention purge migration grants privileges beyond DELETE: %s", step.SQL)
		}
		if LatestVersion() != 116 {
			t.Fatalf("LatestVersion()=%d, want 116", LatestVersion())
		}
		return
	}
	t.Fatal("retention video purge grant migration missing")
}

func TestVideoScopeDisplayNamesMigrationAddsNullablePortugueseLabels(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version != 93 {
			continue
		}
		if step.Name != "video_scope_display_names_pt_br" || !step.Compatible {
			t.Fatalf("unexpected display-name migration: %+v", step)
		}
		for _, required := range []string{
			"environment_display_name varchar(120)",
			"component_display_name varchar(120)",
			"view_display_name varchar(120)",
		} {
			if !strings.Contains(step.SQL, required) {
				t.Fatalf("display-name migration missing %q", required)
			}
		}
		if LatestVersion() != 116 {
			t.Fatalf("LatestVersion()=%d, want 116", LatestVersion())
		}
		return
	}
	t.Fatal("video scope display-name migration missing")
}

func TestVideoPlanReanalysisMigrationAddsIdempotencyAndPinnedParent(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version != 71 {
			continue
		}
		if step.Name != "idempotent_video_plan_reanalysis" {
			t.Fatalf("unexpected reanalysis migration: %+v", step)
		}
		for _, required := range []string{"client_mutation_id", "expected_plan_id", "fk_video_plan_expected_plan", "uq_video_plan_reanalysis_mutation", "inspection_runtime"} {
			if !strings.Contains(step.SQL, required) {
				t.Fatalf("reanalysis migration missing %q", required)
			}
		}
		return
	}
	t.Fatal("video reanalysis migration missing")
}

func TestScopedRetentionMigrationRekeysRetentionTablesBeforeNullingInspectionScope(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version != 60 {
			continue
		}
		if step.Name != "scoped_video_retention_holds" {
			t.Fatalf("unexpected scoped retention migration: %+v", step)
		}
		for _, table := range []string{"legal_holds", "deletion_requests"} {
			dropKey := strings.Index(step.SQL, "ALTER TABLE retention."+table+" DROP CONSTRAINT IF EXISTS "+table+"_pkey")
			allowScoped := strings.Index(step.SQL, "ALTER TABLE retention."+table+" ALTER COLUMN inspection_id DROP NOT NULL")
			addKey := strings.Index(step.SQL, "ADD CONSTRAINT "+table+"_pkey PRIMARY KEY (id,tenant_id)")
			if dropKey < 0 || allowScoped < dropKey || addKey < allowScoped {
				t.Fatalf("%s key must be rekeyed before making inspection scope nullable: %s", table, step.SQL)
			}
		}
		return
	}
	t.Fatal("scoped video retention migration missing")
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
	if LatestVersion() < 40 {
		t.Fatalf("latest version did not include ledger migration: %d", LatestVersion())
	}
}

func TestReportPDFJobsMigrationAddsTenantScopedDurableJobs(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version == 53 {
			if step.Name != "durable_report_pdf_jobs" {
				t.Fatalf("unexpected report PDF migration: %+v", step)
			}
			for _, required := range []string{"reports.report_pdf_jobs", "uq_report_pdf_job", "ENABLE ROW LEVEL SECURITY", "FORCE ROW LEVEL SECURITY", "inspection_runtime"} {
				if !strings.Contains(step.SQL, required) {
					t.Fatalf("report PDF migration does not contain %q", required)
				}
			}
			return
		}
	}
	t.Fatal("durable report PDF migration missing")
}

func TestTriageCaseMigrationEnablesTenantIsolationAndBackfillsCurrentRisks(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version == 46 {
			for _, required := range []string{"dashboard.triage_cases", "dashboard.triage_case_events", "FORCE ROW LEVEL SECURITY", "idx_triage_case_tenant_inspection", "analysis.classification_runs", "c.classification IN ('CRITICAL','ATTENTION')"} {
				if !strings.Contains(step.SQL, required) {
					t.Fatalf("triage migration does not contain %q", required)
				}
			}
			return
		}
	}
	t.Fatal("triage review migration missing")
}

func TestTriageCaseRecoveryMigrationBackfillsMissingCasesIdempotently(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version == 52 {
			if step.Name != "backfill_triage_cases_from_classifications" {
				t.Fatalf("unexpected triage recovery migration: %+v", step)
			}
			for _, required := range []string{"analysis.classification_runs", "reports.report_snapshots", "c.classification IN ('CRITICAL','ATTENTION')", "ON CONFLICT (tenant_id,inspection_id) DO NOTHING"} {
				if !strings.Contains(step.SQL, required) {
					t.Fatalf("triage recovery migration does not contain %q", required)
				}
			}
			return
		}
	}
	t.Fatal("triage recovery migration missing")
}

func TestOriginAttentionItemsMigrationUsesJSONArrays(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version == 43 {
			if step.Name != "origin_attention_items" || !strings.Contains(step.SQL, "media.media_objects") || !strings.Contains(step.SQL, "origins.origin_evidence") || !strings.Contains(step.SQL, "SET DEFAULT '[]'::jsonb") || !strings.Contains(step.SQL, "SET NOT NULL") || !strings.Contains(step.SQL, "jsonb_typeof(attention_items) = 'array'") {
				t.Fatalf("unexpected attention items migration: %+v", step)
			}
			return
		}
	}
	t.Fatal("origin attention items migration missing")
}

func TestAnalysisFindingChangeTypeIsRemoved(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version == 42 {
			if step.Name != "remove_analysis_finding_change_type" || !strings.Contains(step.SQL, "DROP COLUMN IF EXISTS change_type") {
				t.Fatalf("unexpected analysis finding migration: %+v", step)
			}
			return
		}
	}
	t.Fatal("analysis finding removal migration missing")
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

func TestUsageDailySummaryIndexMigrationScopesUniquenessByTenantAndDay(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version != 47 {
			continue
		}
		if step.Name != "usage_daily_summary_tenant_day_index" || !strings.Contains(step.SQL, "DROP INDEX IF EXISTS usage.idx_usage_daily") || !strings.Contains(step.SQL, "CREATE UNIQUE INDEX idx_usage_daily ON usage.daily_summaries(tenant_id, day)") {
			t.Fatalf("unexpected usage summary migration: %+v", step)
		}
		if LatestVersion() < 47 {
			t.Fatalf("latest version did not include usage summary migration: %d", LatestVersion())
		}
		return
	}
	t.Fatal("usage daily summary migration missing")
}

func TestCacheAndDerivativeMetadataMigrationsAreAdditive(t *testing.T) {
	steps := Foundation()
	for version, name := range map[int]string{44: "llm_cache_usage_and_request_sizes", 45: "media_derivative_normalization_metadata"} {
		found := false
		for _, step := range steps {
			if step.Version == version && step.Name == name {
				found = true
				if step.Destructive {
					t.Fatalf("migration %d must be additive", version)
				}
			}
		}
		if !found {
			t.Fatalf("migration %d (%s) missing", version, name)
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

func TestVideoMembershipMigrationAddsTenantForeignKeys(t *testing.T) {
	for _, step := range Foundation() {
		if step.Version != 77 {
			continue
		}
		for _, required := range []string{
			"FOREIGN KEY(tenant_id,plan_id) REFERENCES analysis.video_plans(tenant_id,id) NOT VALID",
			"FOREIGN KEY(tenant_id,plan_item_id) REFERENCES analysis.video_plan_items(tenant_id,id) NOT VALID",
			"ALTER TABLE analysis.video_plan_item_frames ADD CONSTRAINT fk_video_plan_item_frame_frame FOREIGN KEY(tenant_id,frame_id) REFERENCES media.video_frames(tenant_id,id) NOT VALID",
			"FOREIGN KEY(tenant_id,preparation_id) REFERENCES media.video_preparation_jobs(tenant_id,id) NOT VALID",
			"ALTER TABLE media.video_scope_items ADD CONSTRAINT fk_video_scope_frame FOREIGN KEY(tenant_id,frame_id) REFERENCES media.video_frames(tenant_id,id) NOT VALID",
		} {
			if !strings.Contains(step.SQL, required) {
				t.Fatalf("video membership migration missing %q", required)
			}
		}
		return
	}
	t.Fatal("video membership foreign key migration is not registered")
}
