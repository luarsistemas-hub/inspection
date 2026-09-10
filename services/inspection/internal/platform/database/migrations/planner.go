package migrations

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// Step is an ordered advanced migration applied after additive models.
type Step struct {
	Version     int
	Name        string
	SQL         string
	Destructive bool
	Compatible  bool
}

func (s Step) Checksum() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s", s.Version, s.Name, s.SQL)))
	return hex.EncodeToString(sum[:])
}

// Plan validates and orders advanced migrations.
func Plan(steps []Step, applied map[int]string) ([]Step, error) {
	ordered := append([]Step(nil), steps...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Version < ordered[j].Version })
	seen := make(map[int]struct{}, len(ordered))
	var pending []Step
	for _, step := range ordered {
		if step.Version <= 0 || step.Name == "" || step.SQL == "" {
			return nil, fmt.Errorf("migration: invalid step %d", step.Version)
		}
		if _, ok := seen[step.Version]; ok {
			return nil, fmt.Errorf("migration: duplicate version %d", step.Version)
		}
		seen[step.Version] = struct{}{}
		if checksum, ok := applied[step.Version]; ok {
			if checksum != step.Checksum() {
				return nil, fmt.Errorf("migration: checksum drift at version %d", step.Version)
			}
			continue
		}
		if step.Destructive && !step.Compatible {
			return nil, fmt.Errorf("migration: destructive step %d lacks compatibility gate", step.Version)
		}
		pending = append(pending, step)
	}
	return pending, nil
}

// Foundation returns the capability schemas, roles and forced-RLS contract.
func Foundation() []Step {
	return []Step{{Version: 1, Name: "foundation_rls", Compatible: true, SQL: `
CREATE SCHEMA IF NOT EXISTS platform;
CREATE SCHEMA IF NOT EXISTS tenancy;
CREATE SCHEMA IF NOT EXISTS access;
CREATE SCHEMA IF NOT EXISTS audit;
CREATE SCHEMA IF NOT EXISTS messaging;
CREATE TABLE IF NOT EXISTS platform.schema_migrations(version integer PRIMARY KEY, name text NOT NULL, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now());
DO $$ BEGIN CREATE ROLE inspection_runtime NOINHERIT NOBYPASSRLS NOLOGIN; EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ DECLARE t text; BEGIN
  FOREACH t IN ARRAY ARRAY['tenancy.bootstrap_requests','tenancy.tenants','tenancy.business_units','access.memberships','access.resource_scopes','audit.events','messaging.outbox'] LOOP
    EXECUTE 'ALTER TABLE ' || t || ' ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE ' || t || ' FORCE ROW LEVEL SECURITY';
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON ' || t;
    EXECUTE 'CREATE POLICY tenant_isolation ON ' || t || ' USING (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid) WITH CHECK (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid)';
  END LOOP;
END $$;
GRANT USAGE ON SCHEMA tenancy, access, audit, messaging TO inspection_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA tenancy, access, audit, messaging TO inspection_runtime;`},
		{Version: 2, Name: "declarative_catalog_rls", Compatible: true, SQL: `
CREATE SCHEMA IF NOT EXISTS participants;
CREATE SCHEMA IF NOT EXISTS segments;
CREATE SCHEMA IF NOT EXISTS templates;
CREATE SCHEMA IF NOT EXISTS assets;
DO $$ DECLARE t text; BEGIN
  FOREACH t IN ARRAY ARRAY[
    'participants.participants','participants.contacts','participants.contact_verifications','participants.channel_selections',
    'segments.definitions','segments.definition_versions','templates.templates','templates.template_versions','templates.analysis_profiles','templates.analysis_profile_versions',
    'assets.assets','assets.asset_attribute_versions','assets.asset_assignments'
  ] LOOP
    EXECUTE 'ALTER TABLE ' || t || ' ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE ' || t || ' FORCE ROW LEVEL SECURITY';
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON ' || t;
    EXECUTE 'CREATE POLICY tenant_isolation ON ' || t || ' USING (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid) WITH CHECK (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid)';
  END LOOP;
END $$;
CREATE UNIQUE INDEX IF NOT EXISTS idx_template_one_active ON templates.template_versions(tenant_id, template_id) WHERE status='ACTIVE';
ALTER TABLE assets.assets DROP CONSTRAINT IF EXISTS chk_asset_geofence;
ALTER TABLE assets.assets ADD CONSTRAINT chk_asset_geofence CHECK (geofence_meters BETWEEN 25 AND 10000);
ALTER TABLE assets.assets DROP CONSTRAINT IF EXISTS chk_asset_coordinates;
ALTER TABLE assets.assets ADD CONSTRAINT chk_asset_coordinates CHECK ((latitude_e6 IS NULL AND longitude_e6 IS NULL) OR (latitude_e6 BETWEEN -90000000 AND 90000000 AND longitude_e6 BETWEEN -180000000 AND 180000000));
CREATE OR REPLACE FUNCTION platform.reject_immutable_document_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN RAISE EXCEPTION 'published document is immutable'; END IF;
  IF (to_jsonb(NEW) - 'status') IS DISTINCT FROM (to_jsonb(OLD) - 'status') THEN RAISE EXCEPTION 'published document content is immutable'; END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS immutable_definition_version ON segments.definition_versions;
CREATE TRIGGER immutable_definition_version BEFORE UPDATE OR DELETE ON segments.definition_versions FOR EACH ROW EXECUTE FUNCTION platform.reject_immutable_document_mutation();
DROP TRIGGER IF EXISTS immutable_template_version ON templates.template_versions;
CREATE TRIGGER immutable_template_version BEFORE UPDATE OR DELETE ON templates.template_versions FOR EACH ROW EXECUTE FUNCTION platform.reject_immutable_document_mutation();
DROP TRIGGER IF EXISTS immutable_analysis_profile_version ON templates.analysis_profile_versions;
CREATE TRIGGER immutable_analysis_profile_version BEFORE UPDATE OR DELETE ON templates.analysis_profile_versions FOR EACH ROW EXECUTE FUNCTION platform.reject_immutable_document_mutation();
CREATE OR REPLACE FUNCTION platform.reject_append_only_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'append-only record is immutable'; END $$;
DROP TRIGGER IF EXISTS immutable_asset_attribute_version ON assets.asset_attribute_versions;
CREATE TRIGGER immutable_asset_attribute_version BEFORE UPDATE OR DELETE ON assets.asset_attribute_versions FOR EACH ROW EXECUTE FUNCTION platform.reject_append_only_mutation();
DROP TRIGGER IF EXISTS immutable_contact_verification ON participants.contact_verifications;
CREATE TRIGGER immutable_contact_verification BEFORE UPDATE OR DELETE ON participants.contact_verifications FOR EACH ROW EXECUTE FUNCTION platform.reject_append_only_mutation();
GRANT USAGE ON SCHEMA participants, segments, templates, assets TO inspection_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA participants, segments, templates, assets TO inspection_runtime;`},
		{Version: 3, Name: "async_sessions_storage_rls", Compatible: true, SQL: `
CREATE SCHEMA IF NOT EXISTS invitations;
CREATE SCHEMA IF NOT EXISTS media;
CREATE SCHEMA IF NOT EXISTS notifications;
DO $$ DECLARE t text; BEGIN
  FOREACH t IN ARRAY ARRAY[
    'messaging.inbox','invitations.invitations','invitations.otp_challenges','invitations.external_sessions',
    'media.media_objects','media.multipart_uploads','media.upload_parts',
    'notifications.deliveries','notifications.channel_attempts','notifications.provider_callbacks'
  ] LOOP
    EXECUTE 'ALTER TABLE ' || t || ' ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE ' || t || ' FORCE ROW LEVEL SECURITY';
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON ' || t;
    EXECUTE 'CREATE POLICY tenant_isolation ON ' || t || ' USING (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid) WITH CHECK (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid)';
  END LOOP;
END $$;
CREATE OR REPLACE FUNCTION platform.reject_original_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status IN ('VERIFIED','SCREENED','READY','REJECTED','PURGED') AND (to_jsonb(NEW) - 'status') IS DISTINCT FROM (to_jsonb(OLD) - 'status') THEN RAISE EXCEPTION 'original media is immutable'; END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS immutable_media_original ON media.media_objects;
CREATE TRIGGER immutable_media_original BEFORE UPDATE ON media.media_objects FOR EACH ROW EXECUTE FUNCTION platform.reject_original_mutation();
GRANT USAGE ON SCHEMA invitations, media, notifications TO inspection_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA invitations, media, notifications TO inspection_runtime;`},
		{Version: 4, Name: "inspection_lifecycle_rls", Compatible: true, SQL: `
CREATE SCHEMA IF NOT EXISTS schedules;
CREATE SCHEMA IF NOT EXISTS inspections;
CREATE SCHEMA IF NOT EXISTS projects;
DO $$ DECLARE t text; BEGIN
  FOREACH t IN ARRAY ARRAY[
    'schedules.schedules','schedules.occurrence_materializations','schedules.reminder_plans',
    'inspections.inspections','inspections.responsibilities','inspections.policy_snapshots','inspections.reference_snapshots',
    'projects.projects','projects.project_stages','projects.stage_transitions'
  ] LOOP
    EXECUTE 'ALTER TABLE ' || t || ' ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE ' || t || ' FORCE ROW LEVEL SECURITY';
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON ' || t;
    EXECUTE 'CREATE POLICY tenant_isolation ON ' || t || ' USING (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid) WITH CHECK (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid)';
  END LOOP;
END $$;
ALTER TABLE schedules.schedules DROP CONSTRAINT IF EXISTS chk_schedule_status;
ALTER TABLE schedules.schedules ADD CONSTRAINT chk_schedule_status CHECK (status IN ('ACTIVE','CANCELED'));
ALTER TABLE inspections.inspections DROP CONSTRAINT IF EXISTS chk_inspection_status;
ALTER TABLE inspections.inspections ADD CONSTRAINT chk_inspection_status CHECK (status IN ('PLANNED','INVITED','IN_PROGRESS','SUBMITTED','ANALYZING','RECAPTURE_PENDING','COMPLETED','CANCELED','INVALIDATED'));
ALTER TABLE inspections.inspections DROP CONSTRAINT IF EXISTS chk_inspection_source;
ALTER TABLE inspections.inspections ADD CONSTRAINT chk_inspection_source CHECK (source IN ('SCHEDULED','MILESTONE','MANUAL'));
ALTER TABLE inspections.responsibilities DROP CONSTRAINT IF EXISTS chk_responsibility_status;
ALTER TABLE inspections.responsibilities ADD CONSTRAINT chk_responsibility_status CHECK (status IN ('PENDING','ACCESSED','IN_PROGRESS','SUBMITTED','EXPIRED','REVOKED','CANCELED'));
ALTER TABLE projects.projects DROP CONSTRAINT IF EXISTS chk_project_status;
ALTER TABLE projects.projects ADD CONSTRAINT chk_project_status CHECK (status IN ('ACTIVE','CLOSED','INVALIDATED'));
ALTER TABLE projects.project_stages DROP CONSTRAINT IF EXISTS chk_stage_status;
ALTER TABLE projects.project_stages ADD CONSTRAINT chk_stage_status CHECK (status IN ('PLANNED','AVAILABLE','IN_PROGRESS','COMPLETED','SKIPPED','CANCELED','INVALIDATED'));
ALTER TABLE projects.project_stages DROP CONSTRAINT IF EXISTS chk_stage_kind;
ALTER TABLE projects.project_stages ADD CONSTRAINT chk_stage_kind CHECK (kind IN ('ORIGIN','INSPECTION','EXCEPTIONAL'));
CREATE OR REPLACE FUNCTION platform.reject_lifecycle_snapshot_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'lifecycle snapshot is immutable'; END $$;
DROP TRIGGER IF EXISTS immutable_policy_snapshot ON inspections.policy_snapshots;
CREATE TRIGGER immutable_policy_snapshot BEFORE UPDATE OR DELETE ON inspections.policy_snapshots FOR EACH ROW EXECUTE FUNCTION platform.reject_lifecycle_snapshot_mutation();
DROP TRIGGER IF EXISTS immutable_reference_snapshot ON inspections.reference_snapshots;
CREATE TRIGGER immutable_reference_snapshot BEFORE UPDATE OR DELETE ON inspections.reference_snapshots FOR EACH ROW EXECUTE FUNCTION platform.reject_lifecycle_snapshot_mutation();
DROP TRIGGER IF EXISTS immutable_stage_transition ON projects.stage_transitions;
CREATE TRIGGER immutable_stage_transition BEFORE UPDATE OR DELETE ON projects.stage_transitions FOR EACH ROW EXECUTE FUNCTION platform.reject_lifecycle_snapshot_mutation();
GRANT USAGE ON SCHEMA schedules, inspections, projects TO inspection_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA schedules, inspections, projects TO inspection_runtime;`},
		{Version: 5, Name: "capture_media_origin_recapture_rls", Compatible: true, SQL: `
CREATE SCHEMA IF NOT EXISTS origins;
CREATE SCHEMA IF NOT EXISTS capture;
CREATE SCHEMA IF NOT EXISTS recapture;
DO $$ DECLARE t text; BEGIN
  FOREACH t IN ARRAY ARRAY[
    'invitations.processing_acceptances',
    'origins.origins','origins.origin_versions','origins.origin_evidence',
    'capture.capture_drafts','capture.requirement_answers','capture.submission_versions',
    'media.derivatives','media.screening_runs',
    'recapture.requests','recapture.request_requirements'
  ] LOOP
    EXECUTE 'ALTER TABLE ' || t || ' ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE ' || t || ' FORCE ROW LEVEL SECURITY';
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON ' || t;
    EXECUTE 'CREATE POLICY tenant_isolation ON ' || t || ' USING (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid) WITH CHECK (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid)';
  END LOOP;
END $$;
CREATE UNIQUE INDEX IF NOT EXISTS idx_origin_one_active ON origins.origin_versions(tenant_id, origin_id) WHERE status='ACTIVE';
CREATE UNIQUE INDEX IF NOT EXISTS idx_answer_lineage ON capture.requirement_answers(tenant_id, draft_id, requirement_key);
CREATE UNIQUE INDEX IF NOT EXISTS idx_submission_version ON capture.submission_versions(tenant_id, draft_id, version_number);
CREATE UNIQUE INDEX IF NOT EXISTS idx_recapture_requirement ON recapture.request_requirements(tenant_id, request_id, requirement_key);
ALTER TABLE media.media_objects DROP CONSTRAINT IF EXISTS chk_media_size;
ALTER TABLE media.media_objects ADD CONSTRAINT chk_media_size CHECK (size_bytes > 0 AND size_bytes <= 20971520);
CREATE OR REPLACE FUNCTION platform.reject_capture_history_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'capture history is immutable'; END $$;
DROP TRIGGER IF EXISTS immutable_origin_evidence ON origins.origin_evidence;
CREATE TRIGGER immutable_origin_evidence BEFORE UPDATE OR DELETE ON origins.origin_evidence FOR EACH ROW EXECUTE FUNCTION platform.reject_capture_history_mutation();
DROP TRIGGER IF EXISTS immutable_submission_version ON capture.submission_versions;
CREATE TRIGGER immutable_submission_version BEFORE UPDATE OR DELETE ON capture.submission_versions FOR EACH ROW EXECUTE FUNCTION platform.reject_capture_history_mutation();
DROP TRIGGER IF EXISTS immutable_media_derivative ON media.derivatives;
CREATE TRIGGER immutable_media_derivative BEFORE UPDATE OR DELETE ON media.derivatives FOR EACH ROW EXECUTE FUNCTION platform.reject_capture_history_mutation();
GRANT USAGE ON SCHEMA origins, capture, recapture TO inspection_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA origins, capture, recapture TO inspection_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA invitations, media TO inspection_runtime;`},
		{Version: 6, Name: "capture_contract_constraints", Compatible: true, SQL: `
CREATE UNIQUE INDEX IF NOT EXISTS idx_origin_invite_idempotency ON origins.origin_versions(tenant_id, idempotency_key);
CREATE UNIQUE INDEX IF NOT EXISTS idx_media_create_idempotency ON media.media_objects(tenant_id, idempotency_key) WHERE idempotency_key <> '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_media_derivative_kind ON media.derivatives(tenant_id, media_id, kind);
ALTER TABLE origins.origin_versions DROP CONSTRAINT IF EXISTS chk_origin_version_status;
ALTER TABLE origins.origin_versions ADD CONSTRAINT chk_origin_version_status CHECK (status IN ('DRAFT','SUBMITTED','ACTIVE','SUPERSEDED','INVALIDATED'));
ALTER TABLE capture.capture_drafts DROP CONSTRAINT IF EXISTS chk_capture_draft_status;
ALTER TABLE capture.capture_drafts ADD CONSTRAINT chk_capture_draft_status CHECK (status IN ('OPEN','SUBMITTED'));
ALTER TABLE media.media_objects DROP CONSTRAINT IF EXISTS chk_media_status;
ALTER TABLE media.media_objects ADD CONSTRAINT chk_media_status CHECK (status IN ('INITIATED','UPLOADING','UPLOADED','VERIFIED','SCREENED','READY','REJECTED','ABORTED','PURGED'));
ALTER TABLE recapture.requests DROP CONSTRAINT IF EXISTS chk_recapture_status;
ALTER TABLE recapture.requests ADD CONSTRAINT chk_recapture_status CHECK (status IN ('REQUESTED','ACCESSED','SUBMITTED','EXPIRED','CANCELED'));
CREATE OR REPLACE FUNCTION platform.reject_original_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE submitted boolean;
BEGIN
  IF OLD.status IN ('VERIFIED','SCREENED','READY','REJECTED','PURGED') AND
     (NEW.object_key,NEW.content_type,NEW.sha256,NEW.size_bytes,NEW.tenant_id,NEW.responsibility_id,NEW.id,NEW.created_at)
       IS DISTINCT FROM
     (OLD.object_key,OLD.content_type,OLD.sha256,OLD.size_bytes,OLD.tenant_id,OLD.responsibility_id,OLD.id,OLD.created_at)
  THEN RAISE EXCEPTION 'original media is immutable'; END IF;
  SELECT EXISTS(SELECT 1 FROM capture.capture_drafts d WHERE d.tenant_id=OLD.tenant_id AND d.responsibility_id=OLD.responsibility_id AND d.status='SUBMITTED') INTO submitted;
  IF submitted AND NEW.status <> 'PURGED' AND to_jsonb(NEW) IS DISTINCT FROM to_jsonb(OLD) AND
     NOT (OLD.replaces_media_id IS NULL AND NEW.replaces_media_id IS NOT NULL AND (to_jsonb(NEW) - 'replaces_media_id') = (to_jsonb(OLD) - 'replaces_media_id'))
  THEN RAISE EXCEPTION 'submitted evidence is immutable'; END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS immutable_media_original ON media.media_objects;
CREATE TRIGGER immutable_media_original BEFORE UPDATE ON media.media_objects FOR EACH ROW EXECUTE FUNCTION platform.reject_original_mutation();`},
		{Version: 7, Name: "capture_lineage_constraints", Compatible: true, SQL: `
CREATE UNIQUE INDEX IF NOT EXISTS idx_capture_draft_responsibility ON capture.capture_drafts(tenant_id, responsibility_id);
CREATE INDEX IF NOT EXISTS idx_screening_media ON media.screening_runs(tenant_id, media_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_recapture_inspection ON recapture.requests(tenant_id, inspection_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_recapture_one_active ON recapture.requests(tenant_id, inspection_id) WHERE status IN ('REQUESTED','ACCESSED');
ALTER TABLE recapture.requests ADD COLUMN IF NOT EXISTS payload_digest varchar(64) NOT NULL DEFAULT '';
ALTER TABLE recapture.request_requirements DROP CONSTRAINT IF EXISTS chk_recapture_requirement_status;
ALTER TABLE recapture.request_requirements ADD CONSTRAINT chk_recapture_requirement_status CHECK (status IN ('OPEN','CORRECTED','EXPIRED','CANCELED'));
CREATE OR REPLACE FUNCTION platform.reject_submitted_answer_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE submitted boolean;
BEGIN
  SELECT status='SUBMITTED' FROM capture.capture_drafts WHERE tenant_id=OLD.tenant_id AND id=OLD.draft_id INTO submitted;
  IF submitted THEN RAISE EXCEPTION 'submitted requirement answer is immutable'; END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS immutable_submitted_answer ON capture.requirement_answers;
CREATE TRIGGER immutable_submitted_answer BEFORE UPDATE OR DELETE ON capture.requirement_answers FOR EACH ROW EXECUTE FUNCTION platform.reject_submitted_answer_mutation();`},
		{Version: 8, Name: "capture_expired_drafts", Compatible: true, SQL: `
ALTER TABLE capture.capture_drafts DROP CONSTRAINT IF EXISTS chk_capture_draft_status;
ALTER TABLE capture.capture_drafts ADD CONSTRAINT chk_capture_draft_status CHECK (status IN ('OPEN','SUBMITTED','EXPIRED'));`},
		{Version: 9, Name: "invitation_token_rotation", Compatible: true, SQL: `
ALTER TABLE invitations.invitations ADD COLUMN IF NOT EXISTS previous_token_hash bytea;
CREATE INDEX IF NOT EXISTS idx_invitation_previous_token_hash ON invitations.invitations(previous_token_hash);`},
		{Version: 10, Name: "capture_snapshot_immutability", Compatible: true, SQL: `
CREATE OR REPLACE FUNCTION platform.reject_capture_snapshot_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR
     NEW.template_version_id IS DISTINCT FROM OLD.template_version_id OR
     NEW.reference_payload IS DISTINCT FROM OLD.reference_payload OR
     NEW.policy_payload IS DISTINCT FROM OLD.policy_payload
  THEN RAISE EXCEPTION 'capture occurrence snapshot is immutable'; END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS immutable_capture_snapshot ON capture.capture_drafts;
CREATE TRIGGER immutable_capture_snapshot BEFORE UPDATE ON capture.capture_drafts FOR EACH ROW EXECUTE FUNCTION platform.reject_capture_snapshot_mutation();`},
		{Version: 11, Name: "analysis_reporting_retention_rls", Compatible: true, SQL: `
CREATE SCHEMA IF NOT EXISTS analysis; CREATE SCHEMA IF NOT EXISTS reports; CREATE SCHEMA IF NOT EXISTS retention;
DO $$ DECLARE t text; BEGIN FOREACH t IN ARRAY ARRAY[
  'analysis.comparison_jobs','analysis.analysis_runs','analysis.findings','analysis.classification_runs',
  'reports.report_snapshots','reports.report_artifacts','retention.policies'
] LOOP
  EXECUTE 'ALTER TABLE ' || t || ' ENABLE ROW LEVEL SECURITY'; EXECUTE 'ALTER TABLE ' || t || ' FORCE ROW LEVEL SECURITY';
  EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON ' || t;
  EXECUTE 'CREATE POLICY tenant_isolation ON ' || t || ' USING (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid) WITH CHECK (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid)';
END LOOP; END $$;
ALTER TABLE analysis.findings ADD CONSTRAINT chk_finding_confidence CHECK (confidence >= 0 AND confidence <= 1);
ALTER TABLE analysis.findings ADD CONSTRAINT chk_finding_severity CHECK (severity IN ('NONE','LOW','MEDIUM','HIGH','CRITICAL'));
ALTER TABLE analysis.classification_runs ADD CONSTRAINT chk_final_classification CHECK (classification IN ('NORMAL','ATTENTION','CRITICAL'));
CREATE UNIQUE INDEX idx_analysis_job_input ON analysis.comparison_jobs(tenant_id, inspection_id, requirement_key, model_alias, prompt_version, input_digest);
CREATE OR REPLACE FUNCTION platform.reject_terminal_analysis_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'analysis/report record is immutable'; END $$;
CREATE TRIGGER immutable_analysis_run BEFORE UPDATE OR DELETE ON analysis.analysis_runs FOR EACH ROW EXECUTE FUNCTION platform.reject_terminal_analysis_mutation();
CREATE TRIGGER immutable_finding BEFORE UPDATE OR DELETE ON analysis.findings FOR EACH ROW EXECUTE FUNCTION platform.reject_terminal_analysis_mutation();
CREATE TRIGGER immutable_classification BEFORE UPDATE OR DELETE ON analysis.classification_runs FOR EACH ROW EXECUTE FUNCTION platform.reject_terminal_analysis_mutation();
CREATE TRIGGER immutable_report_snapshot BEFORE UPDATE OR DELETE ON reports.report_snapshots FOR EACH ROW EXECUTE FUNCTION platform.reject_terminal_analysis_mutation();
GRANT USAGE ON SCHEMA analysis, reports, retention TO inspection_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA analysis, reports, retention TO inspection_runtime;`},
		{Version: 12, Name: "dashboard_usage_retention_projection_rls", Compatible: true, SQL: `
CREATE SCHEMA IF NOT EXISTS dashboard; CREATE SCHEMA IF NOT EXISTS usage;
DO $$ DECLARE t text; BEGIN FOREACH t IN ARRAY ARRAY[
  'dashboard.inspections','usage.records','usage.daily_summaries',
  'retention.deletion_requests','retention.legal_holds','retention.purge_runs'
] LOOP
  EXECUTE 'ALTER TABLE ' || t || ' ENABLE ROW LEVEL SECURITY'; EXECUTE 'ALTER TABLE ' || t || ' FORCE ROW LEVEL SECURITY';
  EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON ' || t;
  EXECUTE 'CREATE POLICY tenant_isolation ON ' || t || ' USING (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid) WITH CHECK (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid)';
END LOOP; END $$;
CREATE UNIQUE INDEX IF NOT EXISTS idx_dashboard_inspection ON dashboard.inspections(tenant_id, inspection_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_usage_daily ON usage.daily_summaries(tenant_id, day);
CREATE UNIQUE INDEX IF NOT EXISTS idx_retention_hold_active ON retention.legal_holds(tenant_id, inspection_id) WHERE active;
GRANT USAGE ON SCHEMA dashboard, usage TO inspection_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA dashboard, usage TO inspection_runtime;`},
		{Version: 13, Name: "internal_notification_recipients_and_purge_override", Compatible: true, SQL: `
CREATE TABLE IF NOT EXISTS notifications.recipients (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  identity_id uuid NOT NULL,
  channel varchar(20) NOT NULL,
  destination varchar(320) NOT NULL,
  verified boolean NOT NULL DEFAULT false,
  selected boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, identity_id, channel, destination)
);
ALTER TABLE notifications.recipients ENABLE ROW LEVEL SECURITY;
ALTER TABLE notifications.recipients FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON notifications.recipients;
CREATE POLICY tenant_isolation ON notifications.recipients USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON notifications.recipients TO inspection_runtime;
CREATE UNIQUE INDEX IF NOT EXISTS idx_retention_purge_inspection ON retention.purge_runs(tenant_id, inspection_id);
CREATE OR REPLACE FUNCTION platform.reject_terminal_analysis_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND current_setting('app.retention_purge', true) = 'on' THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'analysis/report record is immutable';
END $$;
CREATE OR REPLACE FUNCTION platform.reject_immutable_document_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND current_setting('app.retention_purge', true) = 'on' THEN RETURN OLD; END IF;
  IF TG_OP = 'DELETE' THEN RAISE EXCEPTION 'published document is immutable'; END IF;
  IF (to_jsonb(NEW) - 'status') IS DISTINCT FROM (to_jsonb(OLD) - 'status') THEN RAISE EXCEPTION 'published document content is immutable'; END IF;
  RETURN NEW;
END $$;`},
		{Version: 14, Name: "retention_purge_trigger_overrides_and_delivery_linkage", Compatible: true, SQL: `
ALTER TABLE notifications.deliveries ADD COLUMN IF NOT EXISTS inspection_id uuid;
CREATE INDEX IF NOT EXISTS idx_delivery_inspection ON notifications.deliveries(tenant_id, inspection_id);
CREATE OR REPLACE FUNCTION platform.reject_lifecycle_snapshot_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND current_setting('app.retention_purge', true) = 'on' THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'lifecycle snapshot is immutable';
END $$;
CREATE OR REPLACE FUNCTION platform.reject_capture_history_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND current_setting('app.retention_purge', true) = 'on' THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'capture history is immutable';
END $$;
CREATE OR REPLACE FUNCTION platform.reject_submitted_answer_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE submitted boolean;
BEGIN
  IF TG_OP = 'DELETE' AND current_setting('app.retention_purge', true) = 'on' THEN RETURN OLD; END IF;
  SELECT status='SUBMITTED' FROM capture.capture_drafts WHERE tenant_id=OLD.tenant_id AND id=OLD.draft_id INTO submitted;
  IF submitted THEN RAISE EXCEPTION 'submitted requirement answer is immutable'; END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END $$;`},
		{Version: 15, Name: "runtime_schema_compatibility_read", Compatible: true, SQL: `
GRANT USAGE ON SCHEMA platform TO inspection_runtime;
GRANT SELECT ON platform.schema_migrations TO inspection_runtime;`},
		{Version: 16, Name: "oidc_membership_lookup_rls", Compatible: true, SQL: `
DROP POLICY IF EXISTS membership_oidc_lookup ON access.memberships;
CREATE POLICY membership_oidc_lookup ON access.memberships FOR SELECT TO inspection_runtime
USING (
  issuer = current_setting('app.oidc_issuer', true)
  AND subject = current_setting('app.oidc_subject', true)
);`},
		{Version: 17, Name: "business_unit_idempotency", Compatible: true, SQL: `
ALTER TABLE tenancy.business_units ADD COLUMN IF NOT EXISTS idempotency_key varchar(200) NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_business_unit_idempotency ON tenancy.business_units(tenant_id, idempotency_key) WHERE idempotency_key <> '';`},
		{Version: 18, Name: "worker_outbox_dispatch_role", Compatible: true, SQL: `
DO $$ BEGIN CREATE ROLE inspection_worker INHERIT BYPASSRLS NOLOGIN; EXCEPTION WHEN duplicate_object THEN ALTER ROLE inspection_worker INHERIT BYPASSRLS; END $$;
GRANT inspection_runtime TO inspection_worker;`},
		{Version: 19, Name: "recapture_responsibility_lineage", Compatible: true, SQL: `
DROP INDEX IF EXISTS inspections.idx_inspection_responsibility;
CREATE INDEX IF NOT EXISTS idx_inspection_responsibility ON inspections.responsibilities(tenant_id, inspection_id);`},
		{Version: 20, Name: "product_entitlements_and_access_contract", Compatible: true, SQL: `
CREATE TABLE IF NOT EXISTS access.product_entitlements (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  membership_id uuid NOT NULL,
  product varchar(16) NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uq_product_entitlement UNIQUE (tenant_id, membership_id, product),
  CONSTRAINT chk_product_entitlement_product CHECK (product IN ('ADMIN','DASHBOARD'))
);
ALTER TABLE access.product_entitlements ENABLE ROW LEVEL SECURITY;
ALTER TABLE access.product_entitlements FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON access.product_entitlements;
CREATE POLICY tenant_isolation ON access.product_entitlements USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
CREATE INDEX IF NOT EXISTS idx_product_entitlements_membership ON access.product_entitlements(tenant_id, membership_id);
INSERT INTO access.product_entitlements (id, tenant_id, membership_id, product)
SELECT gen_random_uuid(), tenant_id, id, CASE WHEN role='TENANT_ADMIN' THEN 'ADMIN' ELSE 'DASHBOARD' END
FROM access.memberships WHERE status='ACTIVE'
ON CONFLICT (tenant_id, membership_id, product) DO NOTHING;
INSERT INTO access.product_entitlements (id, tenant_id, membership_id, product)
SELECT gen_random_uuid(), tenant_id, id, 'DASHBOARD'
FROM access.memberships WHERE status='ACTIVE' AND role='TENANT_ADMIN'
ON CONFLICT (tenant_id, membership_id, product) DO NOTHING;
ALTER TABLE access.resource_scopes DROP CONSTRAINT IF EXISTS chk_resource_scope_kind;
ALTER TABLE access.resource_scopes ADD CONSTRAINT chk_resource_scope_kind CHECK (kind IN ('BUSINESS_UNIT','ASSET','PROJECT','INSPECTION'));
ALTER TABLE access.memberships DROP CONSTRAINT IF EXISTS chk_membership_role;
ALTER TABLE access.memberships ADD CONSTRAINT chk_membership_role CHECK (role IN ('TENANT_ADMIN','MANAGER','EMPLOYEE','VIEWER','CUSTOMER_VIEWER'));
		GRANT SELECT, INSERT, UPDATE, DELETE ON access.product_entitlements TO inspection_runtime;`},
		{Version: 21, Name: "report_publication_and_recipient_notifications", Compatible: true, SQL: `
ALTER TABLE reports.report_snapshots ADD COLUMN IF NOT EXISTS publication_policy_version bigint NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS reports.publication_policies (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL UNIQUE, mode varchar(16) NOT NULL,
  version bigint NOT NULL DEFAULT 1, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT chk_publication_policy_mode CHECK (mode IN ('MANUAL','AUTOMATIC'))
);
CREATE TABLE IF NOT EXISTS reports.report_publications (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, snapshot_id uuid NOT NULL, inspection_id uuid NOT NULL,
  policy_version bigint NOT NULL, client_mutation_id varchar(200) NOT NULL, status varchar(20) NOT NULL,
  actor_id uuid NOT NULL, reason varchar(2000), version bigint NOT NULL DEFAULT 1,
  published_at timestamptz, invalidated_at timestamptz, superseded_by uuid, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT chk_report_publication_status CHECK (status IN ('PUBLISHED','INVALIDATED','SUPERSEDED')),
  CONSTRAINT uq_report_publication_snapshot UNIQUE (tenant_id, snapshot_id),
  CONSTRAINT uq_report_publication_mutation UNIQUE (tenant_id, client_mutation_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_report_publication_active ON reports.report_publications(tenant_id, inspection_id) WHERE status='PUBLISHED';
CREATE TABLE IF NOT EXISTS notifications.recipient_channels (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, recipient_membership_id uuid NOT NULL, channel varchar(20) NOT NULL,
  destination varchar(320) NOT NULL, verified_at timestamptz, selected boolean NOT NULL DEFAULT false,
  version bigint NOT NULL DEFAULT 1, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uq_recipient_channel UNIQUE (tenant_id, recipient_membership_id, channel, destination)
);
CREATE TABLE IF NOT EXISTS notifications.recipient_notifications (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, recipient_membership_id uuid NOT NULL, event_id uuid NOT NULL,
  kind varchar(100) NOT NULL, title varchar(500) NOT NULL, body varchar(2000) NOT NULL,
  resource_kind varchar(100) NOT NULL, resource_id uuid, created_at timestamptz NOT NULL DEFAULT now(), read_at timestamptz,
  CONSTRAINT uq_recipient_notification UNIQUE (tenant_id, recipient_membership_id, event_id, kind)
);
CREATE INDEX IF NOT EXISTS idx_recipient_notifications_cursor ON notifications.recipient_notifications(tenant_id, recipient_membership_id, created_at DESC, id DESC);
DO $$ DECLARE t text; BEGIN FOREACH t IN ARRAY ARRAY['reports.publication_policies','reports.report_publications','notifications.recipient_channels','notifications.recipient_notifications'] LOOP
  EXECUTE 'ALTER TABLE ' || t || ' ENABLE ROW LEVEL SECURITY'; EXECUTE 'ALTER TABLE ' || t || ' FORCE ROW LEVEL SECURITY';
  EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON ' || t;
  EXECUTE 'CREATE POLICY tenant_isolation ON ' || t || ' USING (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid) WITH CHECK (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid)';
END LOOP; END $$;
GRANT USAGE ON SCHEMA reports, notifications TO inspection_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA reports, notifications TO inspection_runtime;`},
		{Version: 22, Name: "membership_context_and_delegated_roles", Compatible: true, SQL: `
DROP INDEX IF EXISTS access.idx_memberships_oidc;
CREATE INDEX IF NOT EXISTS idx_memberships_oidc ON access.memberships(issuer, subject, id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_memberships_tenant_identity ON access.memberships(tenant_id, identity_id);
ALTER TABLE access.memberships DROP CONSTRAINT IF EXISTS chk_membership_role;
ALTER TABLE access.memberships ADD CONSTRAINT chk_membership_role CHECK (role IN ('TENANT_ADMIN','MANAGER','EMPLOYEE','VIEWER','CUSTOMER_VIEWER','ORGANIZATION_ADMIN','ACCESS_ADMIN','PARTICIPATION_ADMIN','INSPECTION_CONFIG_ADMIN','GOVERNANCE_ADMIN','AUDITOR'));
DROP POLICY IF EXISTS membership_oidc_lookup ON access.memberships;
CREATE POLICY membership_oidc_lookup ON access.memberships FOR SELECT TO inspection_runtime
USING (issuer = current_setting('app.oidc_issuer', true) AND subject = current_setting('app.oidc_subject', true));`},
		{Version: 23, Name: "administrative_collection_cursors", Compatible: true, SQL: `
CREATE INDEX IF NOT EXISTS idx_participants_cursor ON participants.participants(tenant_id, created_at, id);
CREATE INDEX IF NOT EXISTS idx_assets_cursor ON assets.assets(tenant_id, created_at, id);
CREATE INDEX IF NOT EXISTS idx_segments_cursor ON segments.definitions(tenant_id, created_at, id);
CREATE INDEX IF NOT EXISTS idx_templates_cursor ON templates.templates(tenant_id, created_at, id);`}}
}
