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
CREATE INDEX IF NOT EXISTS idx_templates_cursor ON templates.templates(tenant_id, created_at, id);`},
		{Version: 24, Name: "onboarding_contract_rls", Compatible: true, SQL: `
CREATE SCHEMA IF NOT EXISTS onboarding;
DO $$ DECLARE t text; BEGIN
  FOREACH t IN ARRAY ARRAY['onboarding.sessions','onboarding.otp_challenges','onboarding.step_records','onboarding.requests','onboarding.activation'] LOOP
    EXECUTE 'ALTER TABLE ' || t || ' ENABLE ROW LEVEL SECURITY';
    EXECUTE 'ALTER TABLE ' || t || ' FORCE ROW LEVEL SECURITY';
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON ' || t;
  END LOOP;
END $$;
CREATE POLICY tenant_isolation ON onboarding.sessions USING (
  (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  OR (session_locator_digest = decode(nullif(current_setting('app.onboarding_session_digest', true), ''), 'hex'))
) WITH CHECK (
  (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  OR (session_locator_digest = decode(nullif(current_setting('app.onboarding_session_digest', true), ''), 'hex'))
);
CREATE POLICY tenant_isolation ON onboarding.otp_challenges USING (
  (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  OR EXISTS (SELECT 1 FROM onboarding.sessions s WHERE s.id = session_id AND s.session_locator_digest = decode(nullif(current_setting('app.onboarding_session_digest', true), ''), 'hex'))
) WITH CHECK (
  (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  OR EXISTS (SELECT 1 FROM onboarding.sessions s WHERE s.id = session_id AND s.session_locator_digest = decode(nullif(current_setting('app.onboarding_session_digest', true), ''), 'hex'))
);
CREATE POLICY tenant_isolation ON onboarding.step_records USING (
  (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  OR EXISTS (SELECT 1 FROM onboarding.sessions s WHERE s.id = session_id AND s.session_locator_digest = decode(nullif(current_setting('app.onboarding_session_digest', true), ''), 'hex'))
) WITH CHECK (
  (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  OR EXISTS (SELECT 1 FROM onboarding.sessions s WHERE s.id = session_id AND s.session_locator_digest = decode(nullif(current_setting('app.onboarding_session_digest', true), ''), 'hex'))
);
CREATE POLICY tenant_isolation ON onboarding.requests USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY tenant_isolation ON onboarding.activation USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
CREATE INDEX IF NOT EXISTS idx_onboarding_sessions_locator ON onboarding.sessions(session_locator_digest) WHERE tenant_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_onboarding_requests_status ON onboarding.requests(tenant_id, status, updated_at);
GRANT USAGE ON SCHEMA onboarding TO inspection_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA onboarding TO inspection_runtime;`},
		{Version: 25, Name: "operational_notification_foundation", Compatible: true, SQL: `
ALTER TABLE notifications.deliveries ADD COLUMN IF NOT EXISTS logical_template varchar(100) NOT NULL DEFAULT '';
ALTER TABLE notifications.deliveries ADD COLUMN IF NOT EXISTS template_version varchar(32) NOT NULL DEFAULT '';
ALTER TABLE notifications.deliveries ADD COLUMN IF NOT EXISTS correlation_id varchar(200) NOT NULL DEFAULT '';
ALTER TABLE notifications.deliveries ADD COLUMN IF NOT EXISTS idempotency_key varchar(200) NOT NULL DEFAULT '';
ALTER TABLE notifications.deliveries ADD COLUMN IF NOT EXISTS request_digest varchar(64) NOT NULL DEFAULT '';
ALTER TABLE notifications.deliveries ADD COLUMN IF NOT EXISTS recipient_id varchar(200) NOT NULL DEFAULT '';
ALTER TABLE notifications.deliveries ADD COLUMN IF NOT EXISTS selected_provider varchar(50) NOT NULL DEFAULT '';
ALTER TABLE notifications.deliveries ADD COLUMN IF NOT EXISTS scheduled_at timestamptz;
ALTER TABLE notifications.deliveries ADD COLUMN IF NOT EXISTS lease_expires_at timestamptz;
ALTER TABLE notifications.channel_attempts ADD COLUMN IF NOT EXISTS template_variables jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE notifications.channel_attempts ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE notifications.channel_attempts ADD COLUMN IF NOT EXISTS lease_expires_at timestamptz;
ALTER TABLE notifications.channel_attempts ADD COLUMN IF NOT EXISTS last_attempt_at timestamptz;
ALTER TABLE notifications.provider_callbacks ADD COLUMN IF NOT EXISTS channel_attempt_id uuid;
CREATE TABLE IF NOT EXISTS notifications.attempt_history (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, channel_attempt_id uuid NOT NULL, sequence integer NOT NULL,
  provider varchar(50) NOT NULL, status varchar(20) NOT NULL, receipt_id varchar(500), error_code varchar(100),
  started_at timestamptz NOT NULL, finished_at timestamptz,
  CONSTRAINT uq_notification_attempt_sequence UNIQUE (tenant_id, channel_attempt_id, sequence)
);
CREATE TABLE IF NOT EXISTS notifications.execution_payloads (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, delivery_id uuid NOT NULL, key_id varchar(100) NOT NULL,
  nonce bytea NOT NULL, ciphertext bytea NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz,
  CONSTRAINT uq_notification_payload_delivery UNIQUE (tenant_id, delivery_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_delivery_idempotency ON notifications.deliveries(tenant_id, idempotency_key) WHERE idempotency_key <> '';
CREATE INDEX IF NOT EXISTS idx_delivery_correlation ON notifications.deliveries(tenant_id, correlation_id);
CREATE INDEX IF NOT EXISTS idx_channel_attempt_due ON notifications.channel_attempts(tenant_id, status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_notification_attempt_history ON notifications.attempt_history(tenant_id, channel_attempt_id, sequence);
CREATE INDEX IF NOT EXISTS idx_notification_payload_expiry ON notifications.execution_payloads(tenant_id, expires_at);
ALTER TABLE notifications.deliveries DROP CONSTRAINT IF EXISTS chk_notification_delivery_status;
ALTER TABLE notifications.deliveries ADD CONSTRAINT chk_notification_delivery_status CHECK (status IN ('PENDING','QUEUED','PROCESSING','ACCEPTED','SENT','DELIVERED','FAILED','UNKNOWN','CANCELED'));
ALTER TABLE notifications.channel_attempts DROP CONSTRAINT IF EXISTS chk_notification_channel_status;
ALTER TABLE notifications.channel_attempts ADD CONSTRAINT chk_notification_channel_status CHECK (status IN ('PENDING','QUEUED','PROCESSING','ACCEPTED','SENT','DELIVERED','FAILED','UNKNOWN','CANCELED'));
DO $$ DECLARE t text; BEGIN FOREACH t IN ARRAY ARRAY['notifications.attempt_history','notifications.execution_payloads'] LOOP
  EXECUTE 'ALTER TABLE ' || t || ' ENABLE ROW LEVEL SECURITY';
  EXECUTE 'ALTER TABLE ' || t || ' FORCE ROW LEVEL SECURITY';
  EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON ' || t;
  EXECUTE 'CREATE POLICY tenant_isolation ON ' || t || ' USING (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid) WITH CHECK (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid)';
END LOOP; END $$;
GRANT SELECT, INSERT, UPDATE, DELETE ON notifications.attempt_history, notifications.execution_payloads TO inspection_runtime;`},
		{Version: 26, Name: "notification_callback_scope", Compatible: true, SQL: `
ALTER TABLE notifications.channel_attempts ADD COLUMN IF NOT EXISTS provider_account varchar(200) NOT NULL DEFAULT '';
ALTER TABLE notifications.provider_callbacks ADD COLUMN IF NOT EXISTS provider_account varchar(200) NOT NULL DEFAULT '';
DROP INDEX IF EXISTS notifications.idx_provider_callback;
CREATE UNIQUE INDEX IF NOT EXISTS idx_provider_callback_scope ON notifications.provider_callbacks(tenant_id, provider, provider_account, callback_id);
CREATE INDEX IF NOT EXISTS idx_channel_attempt_provider_receipt ON notifications.channel_attempts(tenant_id, provider, provider_account, receipt_id) WHERE receipt_id <> '';
`},
		{Version: 27, Name: "meta_callback_tenant_resolution", Compatible: true, SQL: `
CREATE OR REPLACE FUNCTION notifications.resolve_meta_callback_tenant(p_account text, p_receipt text)
RETURNS uuid
LANGUAGE sql
SECURITY DEFINER
SET search_path = notifications, pg_catalog
AS $function$
  SELECT tenant_id
  FROM notifications.channel_attempts
  WHERE provider = 'meta' AND provider_account = p_account AND receipt_id = p_receipt
  GROUP BY tenant_id
  LIMIT 1
$function$;
REVOKE ALL ON FUNCTION notifications.resolve_meta_callback_tenant(text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION notifications.resolve_meta_callback_tenant(text, text) TO inspection_runtime;

`},
		{Version: 28, Name: "meta_callback_early_tenant_resolution", Compatible: true, SQL: `
CREATE OR REPLACE FUNCTION notifications.resolve_meta_callback_tenant(p_account text, p_receipt text)
RETURNS uuid
LANGUAGE sql
SECURITY DEFINER
SET search_path = notifications, pg_catalog
AS $function$
  WITH receipt_candidates AS (
    SELECT tenant_id
    FROM notifications.channel_attempts
    WHERE provider = 'meta' AND provider_account = p_account AND receipt_id = p_receipt
    GROUP BY tenant_id
  ), candidates AS (
    SELECT tenant_id FROM receipt_candidates
    UNION ALL
    SELECT tenant_id
    FROM notifications.channel_attempts
    WHERE provider = 'meta' AND provider_account = p_account
      AND NOT EXISTS (SELECT 1 FROM receipt_candidates)
    GROUP BY tenant_id
  )
  SELECT CASE WHEN count(*) = 1 THEN min(tenant_id::text)::uuid END
  FROM candidates
$function$;
REVOKE ALL ON FUNCTION notifications.resolve_meta_callback_tenant(text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION notifications.resolve_meta_callback_tenant(text, text) TO inspection_runtime;
`},
		{Version: 29, Name: "onboarding_email_attempt_coordination", Compatible: true, SQL: `
ALTER TABLE onboarding.sessions ADD COLUMN IF NOT EXISTS replaced_at timestamptz;
ALTER TABLE onboarding.sessions ADD COLUMN IF NOT EXISTS replacement_reason varchar(64) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_onboarding_sessions_replaced_at ON onboarding.sessions(replaced_at);
CREATE TABLE IF NOT EXISTS onboarding.email_states (
  email varchar(320) PRIMARY KEY,
  pending_session_id uuid,
  verified_session_id uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
REVOKE ALL ON TABLE onboarding.email_states FROM PUBLIC;
REVOKE ALL ON TABLE onboarding.email_states FROM inspection_runtime;

CREATE OR REPLACE FUNCTION onboarding.prepare_email_attempt(p_email text, p_session_id uuid, p_now timestamptz)
RETURNS TABLE(previous_pending_session_id uuid, verified_session_id uuid)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = onboarding, pg_catalog
AS $function$
DECLARE
  v_pending uuid;
  v_verified uuid;
BEGIN
  INSERT INTO onboarding.email_states(email, created_at, updated_at)
  VALUES (p_email, p_now, p_now)
  ON CONFLICT (email) DO NOTHING;

  SELECT e.pending_session_id, e.verified_session_id
    INTO v_pending, v_verified
  FROM onboarding.email_states e
  WHERE e.email = p_email
  FOR UPDATE;

  IF v_verified IS NULL THEN
    SELECT s.id INTO v_verified
    FROM onboarding.sessions s
    WHERE s.email = p_email AND s.state NOT IN ('IDENTITY_PENDING', 'IDENTITY_REPLACED')
    ORDER BY s.created_at DESC, s.id DESC
    LIMIT 1;
    IF v_verified IS NOT NULL THEN
      UPDATE onboarding.email_states SET verified_session_id = v_verified, updated_at = p_now WHERE email = p_email;
    END IF;
  END IF;

  IF v_pending IS NOT NULL THEN
    UPDATE onboarding.sessions
       SET state = 'IDENTITY_REPLACED', replaced_at = p_now,
           replacement_reason = 'NEW_ATTEMPT', expires_at = LEAST(expires_at, p_now), updated_at = p_now
     WHERE id = v_pending AND state = 'IDENTITY_PENDING';
    UPDATE onboarding.otp_challenges
       SET expires_at = LEAST(expires_at, p_now)
     WHERE session_id = v_pending AND purpose = 'ONBOARDING' AND verified_at IS NULL;
  END IF;

  UPDATE onboarding.email_states
     SET pending_session_id = p_session_id, updated_at = p_now
   WHERE email = p_email;

  RETURN QUERY SELECT v_pending, v_verified;
END
$function$;
REVOKE ALL ON FUNCTION onboarding.prepare_email_attempt(text, uuid, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION onboarding.prepare_email_attempt(text, uuid, timestamptz) TO inspection_runtime;

CREATE OR REPLACE FUNCTION onboarding.abort_email_attempt(p_email text, p_session_id uuid, p_reason text, p_now timestamptz)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = onboarding, pg_catalog
AS $function$
BEGIN
  UPDATE onboarding.sessions
     SET state = 'IDENTITY_REPLACED', replaced_at = p_now,
         replacement_reason = left(coalesce(p_reason, 'ABORTED'), 64), expires_at = LEAST(expires_at, p_now), updated_at = p_now
   WHERE id = p_session_id AND state = 'IDENTITY_PENDING';
  UPDATE onboarding.otp_challenges
     SET expires_at = LEAST(expires_at, p_now)
   WHERE session_id = p_session_id AND purpose = 'ONBOARDING' AND verified_at IS NULL;
  UPDATE onboarding.email_states
     SET pending_session_id = NULL, updated_at = p_now
   WHERE email = p_email AND pending_session_id = p_session_id;
END
$function$;
REVOKE ALL ON FUNCTION onboarding.abort_email_attempt(text, uuid, text, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION onboarding.abort_email_attempt(text, uuid, text, timestamptz) TO inspection_runtime;

CREATE OR REPLACE FUNCTION onboarding.promote_email_session(p_email text, p_session_id uuid, p_locator_digest bytea, p_csrf_digest bytea, p_now timestamptz)
RETURNS TABLE(accepted boolean, owner_subject text, agency_tenant_id uuid, agency_business_unit_id uuid, agency_name text, agency_code text, agency_status text)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = onboarding, pg_catalog
AS $function$
DECLARE
  v_pending uuid;
  v_verified uuid;
  v_owner_subject text;
  v_tenant_id uuid;
  v_unit_id uuid;
  v_unit_name text;
  v_unit_code text;
  v_unit_status text;
BEGIN
  SELECT e.pending_session_id, e.verified_session_id
    INTO v_pending, v_verified
  FROM onboarding.email_states e
  WHERE e.email = p_email
  FOR UPDATE;

  IF v_pending IS DISTINCT FROM p_session_id THEN
    RETURN QUERY SELECT false, NULL::text, NULL::uuid, NULL::uuid, NULL::text, NULL::text, NULL::text;
    RETURN;
  END IF;

  IF v_verified IS NOT NULL THEN
    SELECT s.owner_subject, s.tenant_id
      INTO v_owner_subject, v_tenant_id
    FROM onboarding.sessions s
    WHERE s.id = v_verified;

    SELECT b.id, b.name, b.code, b.status
      INTO v_unit_id, v_unit_name, v_unit_code, v_unit_status
    FROM tenancy.business_units b
    WHERE b.tenant_id = v_tenant_id
    ORDER BY b.created_at, b.id
    LIMIT 1;

    UPDATE onboarding.sessions
       SET state = 'IDENTITY_REPLACED', replaced_at = p_now,
           replacement_reason = 'NEW_VERIFIED_ATTEMPT', updated_at = p_now
     WHERE id = v_verified AND id <> p_session_id;
  END IF;

  UPDATE onboarding.otp_challenges
     SET verified_at = p_now
   WHERE session_id = p_session_id AND purpose = 'ONBOARDING' AND verified_at IS NULL;

  UPDATE onboarding.sessions
     SET session_locator_digest = p_locator_digest,
         csrf_digest = p_csrf_digest,
         owner_subject = v_owner_subject,
         tenant_id = v_tenant_id,
         state = 'IDENTITY_VERIFIED', current_step = 'AGENCY',
         version = version + 1, updated_at = p_now
   WHERE id = p_session_id;

  UPDATE onboarding.email_states
     SET pending_session_id = NULL, verified_session_id = p_session_id, updated_at = p_now
   WHERE email = p_email;

  RETURN QUERY SELECT true, v_owner_subject, v_tenant_id, v_unit_id, v_unit_name, v_unit_code, v_unit_status;
END
$function$;
REVOKE ALL ON FUNCTION onboarding.promote_email_session(text, uuid, bytea, bytea, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION onboarding.promote_email_session(text, uuid, bytea, bytea, timestamptz) TO inspection_runtime;

CREATE OR REPLACE FUNCTION onboarding.is_current_verified_session(p_email text, p_session_id uuid)
RETURNS boolean
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = onboarding, pg_catalog
AS $function$
  SELECT CASE
    WHEN NOT EXISTS (SELECT 1 FROM onboarding.email_states WHERE email = p_email)
      THEN EXISTS (SELECT 1 FROM onboarding.sessions WHERE id = p_session_id AND email = p_email AND state NOT IN ('IDENTITY_PENDING', 'IDENTITY_REPLACED') AND replaced_at IS NULL)
    ELSE EXISTS (
    SELECT 1
    FROM onboarding.email_states e
    JOIN onboarding.sessions s ON s.id = e.verified_session_id
    WHERE e.email = p_email AND e.verified_session_id = p_session_id
      AND s.state <> 'IDENTITY_REPLACED' AND s.replaced_at IS NULL
    )
  END
$function$;
REVOKE ALL ON FUNCTION onboarding.is_current_verified_session(text, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION onboarding.is_current_verified_session(text, uuid) TO inspection_runtime;

CREATE OR REPLACE FUNCTION onboarding.load_session_agency(p_session_id uuid)
RETURNS TABLE(tenant_id uuid, business_unit_id uuid, name text, code text, status text)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = onboarding, pg_catalog
AS $function$
  SELECT s.tenant_id, b.id, b.name, b.code, b.status
  FROM onboarding.sessions s
  LEFT JOIN LATERAL (
    SELECT bu.id, bu.name, bu.code, bu.status
    FROM tenancy.business_units bu
    WHERE bu.tenant_id = s.tenant_id
    ORDER BY bu.created_at, bu.id
    LIMIT 1
  ) b ON true
  WHERE s.id = p_session_id AND s.tenant_id IS NOT NULL
$function$;
REVOKE ALL ON FUNCTION onboarding.load_session_agency(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION onboarding.load_session_agency(uuid) TO inspection_runtime;
`},
		{Version: 30, Name: "onboarding_agency_locator_proof", Compatible: true, SQL: `
DROP FUNCTION IF EXISTS onboarding.load_session_agency(uuid);
CREATE OR REPLACE FUNCTION onboarding.load_session_agency(p_session_id uuid, p_locator_digest bytea)
RETURNS TABLE(tenant_id uuid, business_unit_id uuid, name text, code text, status text)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = onboarding, pg_catalog
AS $function$
  SELECT s.tenant_id, b.id, b.name, b.code, b.status
  FROM onboarding.sessions s
  LEFT JOIN LATERAL (
    SELECT bu.id, bu.name, bu.code, bu.status
    FROM tenancy.business_units bu
    WHERE bu.tenant_id = s.tenant_id
    ORDER BY bu.created_at, bu.id
    LIMIT 1
  ) b ON true
  WHERE s.id = p_session_id
    AND s.session_locator_digest = p_locator_digest
    AND s.state NOT IN ('IDENTITY_PENDING', 'IDENTITY_REPLACED')
    AND s.replaced_at IS NULL
$function$;
REVOKE ALL ON FUNCTION onboarding.load_session_agency(uuid, bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION onboarding.load_session_agency(uuid, bytea) TO inspection_runtime;
`},
		{Version: 31, Name: "pending_membership_status_width", Compatible: true, SQL: `
ALTER TABLE access.memberships ALTER COLUMN status TYPE varchar(32);
`},
		{Version: 32, Name: "onboarding_request_inspection", Compatible: true, SQL: `
ALTER TABLE onboarding.requests ADD COLUMN IF NOT EXISTS inspection_id uuid;
CREATE INDEX IF NOT EXISTS idx_onboarding_requests_inspection ON onboarding.requests (inspection_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_onboarding_request_session ON onboarding.requests (session_id);
`},
		{Version: 33, Name: "localized_real_estate_template_names", Compatible: true, SQL: `
UPDATE templates.templates
SET name = CASE key
  WHEN 'real-estate-checklist' THEN 'Primeira vistoria do imóvel'
  WHEN 'real-estate-fixed-origin' THEN 'Vistoria comparativa do imóvel'
  ELSE name
END,
version = version + 1,
updated_at = now()
WHERE key IN ('real-estate-checklist', 'real-estate-fixed-origin')
  AND name = 'Primeira inspeção imobiliária';
`},
		{Version: 34, Name: "admin_activation_invitation_token", Compatible: true, SQL: `
ALTER TABLE onboarding.activation ADD COLUMN IF NOT EXISTS session_id uuid;
ALTER TABLE onboarding.activation ADD COLUMN IF NOT EXISTS invitation_token_digest bytea;
ALTER TABLE onboarding.activation ADD COLUMN IF NOT EXISTS invitation_expires_at timestamptz;
ALTER TABLE onboarding.activation ADD COLUMN IF NOT EXISTS invitation_claimed_at timestamptz;
UPDATE onboarding.activation
SET session_id = idempotency_key::uuid
WHERE session_id IS NULL
  AND idempotency_key ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$';
ALTER TABLE onboarding.activation DROP CONSTRAINT IF EXISTS chk_onboarding_activation_session_id;
ALTER TABLE onboarding.activation ADD CONSTRAINT chk_onboarding_activation_session_id CHECK (session_id IS NOT NULL);
CREATE INDEX IF NOT EXISTS idx_onboarding_activation_session ON onboarding.activation(session_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_onboarding_activation_invitation_token
  ON onboarding.activation(invitation_token_digest)
  WHERE invitation_token_digest IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_onboarding_activation_invitation_expiry
  ON onboarding.activation(invitation_expires_at)
  WHERE invitation_claimed_at IS NULL;
DROP POLICY IF EXISTS tenant_isolation ON onboarding.activation;
CREATE POLICY tenant_isolation ON onboarding.activation USING (
  tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid
  OR invitation_token_digest = decode(nullif(current_setting('app.admin_activation_digest', true), ''), 'hex')
) WITH CHECK (
  tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid
  OR invitation_token_digest = decode(nullif(current_setting('app.admin_activation_digest', true), ''), 'hex')
);
`},
		{Version: 35, Name: "resumable_onboarding_origin_promotions", Compatible: true, SQL: `
CREATE TABLE IF NOT EXISTS origins.promotions (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  inspection_id uuid NOT NULL,
  asset_id uuid NOT NULL,
  template_id uuid NOT NULL,
  expected_asset_version bigint NOT NULL,
  selected_media_ids jsonb NOT NULL,
  manifest jsonb NOT NULL,
  status varchar(20) NOT NULL,
  failure_reason varchar(2000) NOT NULL DEFAULT '',
  origin_version_id uuid,
  requested_by uuid NOT NULL,
  requested_at timestamptz NOT NULL,
  started_at timestamptz,
  completed_at timestamptz,
  updated_at timestamptz NOT NULL,
  CONSTRAINT chk_origin_promotion_status CHECK (status IN ('PENDING','PROCESSING','ACTIVE','FAILED')),
  CONSTRAINT uq_origin_promotion_inspection UNIQUE (tenant_id, inspection_id),
  CONSTRAINT uq_origin_promotion_version UNIQUE (origin_version_id)
);
CREATE INDEX IF NOT EXISTS idx_origin_promotion_status ON origins.promotions(tenant_id, asset_id, status);
ALTER TABLE origins.promotions ENABLE ROW LEVEL SECURITY;
ALTER TABLE origins.promotions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON origins.promotions;
CREATE POLICY tenant_isolation ON origins.promotions
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON origins.promotions TO inspection_runtime;
`},
		{Version: 36, Name: "responsible_email_correction_lineage", Compatible: true, SQL: `
ALTER TABLE invitations.invitations ADD COLUMN IF NOT EXISTS idempotency_key varchar(200) NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_invitation_idempotency
  ON invitations.invitations(tenant_id, idempotency_key)
  WHERE idempotency_key <> '';
ALTER TABLE notifications.deliveries ADD COLUMN IF NOT EXISTS invitation_id uuid;
CREATE INDEX IF NOT EXISTS idx_delivery_invitation ON notifications.deliveries(tenant_id, invitation_id, created_at DESC);
UPDATE notifications.deliveries d
SET invitation_id = CASE
  WHEN split_part(d.idempotency_key, ':', 1) ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
    THEN split_part(d.idempotency_key, ':', 1)::uuid
  ELSE NULL
END
WHERE d.logical_template = 'capture-link'
  AND d.invitation_id IS NULL
  AND split_part(d.idempotency_key, ':', 1) ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
  AND EXISTS (
    SELECT 1 FROM invitations.invitations i
    WHERE i.tenant_id = d.tenant_id
      AND i.id = split_part(d.idempotency_key, ':', 1)::uuid
  );
UPDATE notifications.deliveries d
SET inspection_id = r.inspection_id
FROM invitations.invitations i
JOIN inspections.responsibilities r ON r.tenant_id = i.tenant_id AND r.id = i.responsibility_id
WHERE d.tenant_id = i.tenant_id
  AND d.invitation_id = i.id
  AND d.inspection_id IS NULL;
`},
		{Version: 37, Name: "global_analysis_prompts", Destructive: true, Compatible: true, SQL: `
CREATE SCHEMA IF NOT EXISTS analysis;
CREATE TABLE IF NOT EXISTS analysis.prompts (
  id uuid PRIMARY KEY,
  analysis_type varchar(64) NOT NULL UNIQUE,
  definition_json jsonb NOT NULL,
  canonical_digest varchar(64) NOT NULL,
  revision bigint NOT NULL DEFAULT 1,
  updated_by uuid NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS analysis.prompt_snapshots (
  id uuid PRIMARY KEY,
  analysis_type varchar(64) NOT NULL,
  definition_json jsonb NOT NULL,
  canonical_digest varchar(64) NOT NULL,
  created_at timestamptz NOT NULL,
  CONSTRAINT uq_analysis_prompt_snapshot_digest UNIQUE (analysis_type, canonical_digest)
);
DROP TRIGGER IF EXISTS immutable_analysis_prompt_snapshot ON analysis.prompt_snapshots;
CREATE TRIGGER immutable_analysis_prompt_snapshot BEFORE UPDATE OR DELETE ON analysis.prompt_snapshots FOR EACH ROW EXECUTE FUNCTION platform.reject_append_only_mutation();
GRANT USAGE ON SCHEMA analysis TO inspection_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON analysis.prompts, analysis.prompt_snapshots TO inspection_runtime;
ALTER TABLE inspections.inspections ADD COLUMN IF NOT EXISTS analysis_prompt_snapshot_id uuid;
ALTER TABLE analysis.comparison_jobs ADD COLUMN IF NOT EXISTS prompt_snapshot_id uuid;
ALTER TABLE analysis.comparison_jobs ADD COLUMN IF NOT EXISTS prompt_digest varchar(200) NOT NULL DEFAULT '';
ALTER TABLE analysis.analysis_runs ADD COLUMN IF NOT EXISTS prompt_snapshot_id uuid;
ALTER TABLE analysis.analysis_runs ADD COLUMN IF NOT EXISTS prompt_digest varchar(200) NOT NULL DEFAULT '';
ALTER TABLE analysis.classification_runs ADD COLUMN IF NOT EXISTS prompt_snapshot_id uuid;
ALTER TABLE usage.records ADD COLUMN IF NOT EXISTS prompt_snapshot_id uuid;
ALTER TABLE usage.records ADD COLUMN IF NOT EXISTS prompt_digest varchar(200) NOT NULL DEFAULT '';
DO $$
DECLARE
  snapshot_id uuid;
  snapshot_digest varchar(64);
BEGIN
  SELECT id, canonical_digest INTO snapshot_id, snapshot_digest
  FROM analysis.prompt_snapshots
  WHERE analysis_type = 'REAL_ESTATE'
  ORDER BY created_at ASC
  LIMIT 1;
  IF snapshot_id IS NULL THEN
    RAISE EXCEPTION 'REAL_ESTATE analysis prompt snapshot was not seeded';
  END IF;
  UPDATE inspections.inspections SET analysis_prompt_snapshot_id = snapshot_id WHERE analysis_prompt_snapshot_id IS NULL;
  UPDATE analysis.comparison_jobs SET prompt_snapshot_id = snapshot_id, prompt_digest = snapshot_digest WHERE prompt_snapshot_id IS NULL OR prompt_digest = '';
  UPDATE analysis.analysis_runs SET prompt_snapshot_id = snapshot_id, prompt_digest = snapshot_digest WHERE prompt_snapshot_id IS NULL OR prompt_digest = '';
  UPDATE analysis.classification_runs SET prompt_snapshot_id = snapshot_id WHERE prompt_snapshot_id IS NULL;
  UPDATE usage.records SET prompt_snapshot_id = snapshot_id, prompt_digest = snapshot_digest WHERE prompt_snapshot_id IS NULL OR prompt_digest = '';
END $$;
ALTER TABLE inspections.inspections ALTER COLUMN analysis_prompt_snapshot_id SET NOT NULL;
ALTER TABLE analysis.comparison_jobs ALTER COLUMN prompt_snapshot_id SET NOT NULL;
ALTER TABLE analysis.comparison_jobs ALTER COLUMN prompt_digest SET NOT NULL;
ALTER TABLE analysis.analysis_runs ALTER COLUMN prompt_snapshot_id SET NOT NULL;
ALTER TABLE analysis.analysis_runs ALTER COLUMN prompt_digest SET NOT NULL;
ALTER TABLE analysis.classification_runs ALTER COLUMN prompt_snapshot_id SET NOT NULL;
ALTER TABLE usage.records ALTER COLUMN prompt_snapshot_id SET NOT NULL;
ALTER TABLE usage.records ALTER COLUMN prompt_digest SET NOT NULL;
ALTER TABLE inspections.inspections DROP COLUMN IF EXISTS analysis_profile_version_id;
DROP INDEX IF EXISTS analysis.idx_analysis_job_input;
ALTER TABLE analysis.comparison_jobs DROP COLUMN IF EXISTS prompt_version;
ALTER TABLE analysis.analysis_runs DROP COLUMN IF EXISTS prompt_version;
ALTER TABLE analysis.classification_runs DROP COLUMN IF EXISTS profile_version_id;
ALTER TABLE usage.records DROP COLUMN IF EXISTS prompt_version;
DROP TABLE IF EXISTS templates.analysis_profile_versions;
DROP TABLE IF EXISTS templates.analysis_profiles;
CREATE UNIQUE INDEX IF NOT EXISTS idx_analysis_job_input ON analysis.comparison_jobs(tenant_id, inspection_id, requirement_key, model_alias, prompt_digest, input_digest);
`},
		{Version: 38, Name: "analysis_job_prompt_digest_index", Compatible: true, SQL: `
DROP INDEX IF EXISTS analysis.idx_analysis_job_input;
CREATE UNIQUE INDEX IF NOT EXISTS idx_analysis_job_input ON analysis.comparison_jobs(tenant_id, inspection_id, requirement_key, model_alias, prompt_digest, input_digest);
`},
		{Version: 39, Name: "analysis_finding_change_type", Compatible: true, SQL: `
ALTER TABLE analysis.findings ADD COLUMN IF NOT EXISTS change_type varchar(32);
`},
		{Version: 40, Name: "llm_call_ledger", Compatible: true, SQL: `
ALTER TABLE usage.llm_calls ENABLE ROW LEVEL SECURITY;
ALTER TABLE usage.llm_calls FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON usage.llm_calls;
CREATE POLICY tenant_isolation ON usage.llm_calls
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
CREATE INDEX IF NOT EXISTS idx_llm_calls_inspection_cursor
  ON usage.llm_calls(tenant_id, inspection_id, started_at DESC, call_id DESC);
CREATE INDEX IF NOT EXISTS idx_llm_calls_job
  ON usage.llm_calls(tenant_id, job_id);
CREATE INDEX IF NOT EXISTS idx_llm_calls_started
  ON usage.llm_calls(tenant_id, started_at)
  WHERE state = 'STARTED';
ALTER TABLE usage.llm_calls DROP CONSTRAINT IF EXISTS chk_llm_call_mode;
ALTER TABLE usage.llm_calls ADD CONSTRAINT chk_llm_call_mode CHECK (mode IN ('mock','live'));
ALTER TABLE usage.llm_calls DROP CONSTRAINT IF EXISTS chk_llm_call_state;
ALTER TABLE usage.llm_calls ADD CONSTRAINT chk_llm_call_state CHECK (state IN ('STARTED','FINISHED'));
ALTER TABLE usage.llm_calls DROP CONSTRAINT IF EXISTS chk_llm_call_attempt;
ALTER TABLE usage.llm_calls ADD CONSTRAINT chk_llm_call_attempt CHECK (attempt >= 0 AND replay_generation >= 0);
ALTER TABLE usage.llm_calls DROP CONSTRAINT IF EXISTS chk_llm_call_tokens;
ALTER TABLE usage.llm_calls ADD CONSTRAINT chk_llm_call_tokens CHECK ((input_tokens IS NULL OR input_tokens >= 0) AND (output_tokens IS NULL OR output_tokens >= 0));
ALTER TABLE usage.llm_calls DROP CONSTRAINT IF EXISTS chk_llm_call_cost;
ALTER TABLE usage.llm_calls ADD CONSTRAINT chk_llm_call_cost CHECK (reported_cost IS NULL OR reported_cost >= 0);
CREATE OR REPLACE FUNCTION platform.guard_llm_call_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF current_setting('app.retention_purge', true) <> 'on' THEN
      RAISE EXCEPTION 'LLM call deletion requires retention purge';
    END IF;
    RETURN OLD;
  END IF;
  IF OLD.state <> 'STARTED' THEN
    RAISE EXCEPTION 'finished LLM call is immutable';
  END IF;
  IF NEW.call_id IS DISTINCT FROM OLD.call_id OR
     NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR
     NEW.inspection_id IS DISTINCT FROM OLD.inspection_id OR
     NEW.job_id IS DISTINCT FROM OLD.job_id OR
     NEW.event_id IS DISTINCT FROM OLD.event_id OR
     NEW.execution_id IS DISTINCT FROM OLD.execution_id OR
     NEW.correlation_id IS DISTINCT FROM OLD.correlation_id OR
     NEW.attempt IS DISTINCT FROM OLD.attempt OR
     NEW.replay_generation IS DISTINCT FROM OLD.replay_generation OR
     NEW.mode IS DISTINCT FROM OLD.mode OR
     NEW.comparison_mode IS DISTINCT FROM OLD.comparison_mode OR
     NEW.model_alias IS DISTINCT FROM OLD.model_alias OR
     NEW.prompt_digest IS DISTINCT FROM OLD.prompt_digest OR
     NEW.started_at IS DISTINCT FROM OLD.started_at OR
     NEW.state <> 'FINISHED' OR
     NEW.finished_at IS NULL OR
     NEW.technical_outcome = ''
  THEN
    RAISE EXCEPTION 'LLM call identity is immutable';
  END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS immutable_llm_call ON usage.llm_calls;
CREATE TRIGGER immutable_llm_call BEFORE UPDATE OR DELETE ON usage.llm_calls FOR EACH ROW EXECUTE FUNCTION platform.guard_llm_call_mutation();
GRANT USAGE ON SCHEMA usage TO inspection_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON usage.llm_calls TO inspection_runtime;
`},
		{Version: 41, Name: "global_llm_usage_readers", Compatible: true, SQL: `
CREATE INDEX IF NOT EXISTS idx_llm_calls_global_cursor
  ON usage.llm_calls(started_at DESC, call_id DESC);
CREATE INDEX IF NOT EXISTS idx_llm_calls_tenant_cursor
  ON usage.llm_calls(tenant_id, started_at DESC, call_id DESC);
CREATE INDEX IF NOT EXISTS idx_llm_calls_mode_cursor
  ON usage.llm_calls(mode, started_at DESC, call_id DESC);

CREATE OR REPLACE FUNCTION usage.read_llm_usage_summary(
  p_from timestamptz,
  p_to timestamptz,
  p_tenant_id uuid,
  p_inspection_id uuid,
  p_provider text,
  p_model text,
  p_model_alias text,
  p_mode text,
  p_outcome text,
  p_state text,
  p_cost_state text
)
RETURNS TABLE(
  attempted_calls bigint,
  delivered_calls bigint,
  incomplete_calls bigint,
  input_tokens bigint,
  output_tokens bigint,
  known_reported_cost numeric,
  unknown_cost_calls bigint,
  coverage_started_at timestamptz,
  legacy_coverage_calls bigint
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = usage, inspections, tenancy, platform, pg_catalog
AS $function$
  SELECT
    count(*)::bigint,
    count(*) FILTER (WHERE l.transport_delivered IS TRUE)::bigint,
    count(*) FILTER (WHERE l.state = 'STARTED')::bigint,
    coalesce(sum(CASE WHEN l.transport_delivered IS TRUE THEN coalesce(l.input_tokens, 0) ELSE 0 END), 0)::bigint,
    coalesce(sum(CASE WHEN l.transport_delivered IS TRUE THEN coalesce(l.output_tokens, 0) ELSE 0 END), 0)::bigint,
    coalesce(sum(CASE WHEN l.transport_delivered IS TRUE THEN coalesce(l.reported_cost, 0) ELSE 0 END), 0)::numeric,
    count(*) FILTER (WHERE l.transport_delivered IS TRUE AND l.reported_cost IS NULL)::bigint,
    max(sm.applied_at),
    count(*) FILTER (WHERE sm.applied_at IS NOT NULL AND i.created_at < sm.applied_at)::bigint
  FROM usage.llm_calls l
  LEFT JOIN inspections.inspections i ON i.tenant_id = l.tenant_id AND i.id = l.inspection_id
  LEFT JOIN platform.schema_migrations sm ON sm.version = 40
  WHERE l.started_at >= p_from AND l.started_at < p_to
    AND (p_tenant_id IS NULL OR l.tenant_id = p_tenant_id)
    AND (p_inspection_id IS NULL OR l.inspection_id = p_inspection_id)
    AND (p_provider IS NULL OR l.provider ILIKE '%' || p_provider || '%')
    AND (p_model IS NULL OR l.model ILIKE '%' || p_model || '%')
    AND (p_model_alias IS NULL OR l.model_alias ILIKE '%' || p_model_alias || '%')
    AND (p_mode IS NULL OR l.mode = p_mode)
    AND (p_outcome IS NULL OR l.technical_outcome ILIKE '%' || p_outcome || '%')
    AND (p_state IS NULL OR l.state = p_state)
    AND (
      p_cost_state IS NULL
      OR (p_cost_state = 'informed' AND l.transport_delivered IS TRUE AND l.reported_cost IS NOT NULL)
      OR (p_cost_state = 'missing' AND l.transport_delivered IS TRUE AND l.reported_cost IS NULL)
    )
$function$;
REVOKE ALL ON FUNCTION usage.read_llm_usage_summary(timestamptz, timestamptz, uuid, uuid, text, text, text, text, text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION usage.read_llm_usage_summary(timestamptz, timestamptz, uuid, uuid, text, text, text, text, text, text, text) TO inspection_runtime;

CREATE OR REPLACE FUNCTION usage.read_llm_usage_calls(
  p_from timestamptz,
  p_to timestamptz,
  p_tenant_id uuid,
  p_inspection_id uuid,
  p_provider text,
  p_model text,
  p_model_alias text,
  p_mode text,
  p_outcome text,
  p_state text,
  p_cost_state text,
  p_after_started_at timestamptz,
  p_after_call_id uuid,
  p_limit integer
)
RETURNS TABLE(
  call_id uuid,
  tenant_id uuid,
  tenant_name text,
  inspection_id uuid,
  job_id uuid,
  event_id uuid,
  execution_id uuid,
  correlation_id varchar,
  attempt integer,
  replay_generation integer,
  mode varchar,
  comparison_mode varchar,
  model_alias varchar,
  provider varchar,
  model varchar,
  gateway_request_id varchar,
  state varchar,
  technical_outcome varchar,
  transport_delivered boolean,
  http_status integer,
  input_tokens bigint,
  output_tokens bigint,
  reported_cost numeric,
  duration_ms bigint,
  started_at timestamptz,
  finished_at timestamptz
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = usage, inspections, tenancy, platform, pg_catalog
AS $function$
  SELECT l.call_id, l.tenant_id, t.name, l.inspection_id, l.job_id, l.event_id, l.execution_id,
         l.correlation_id, l.attempt, l.replay_generation, l.mode, l.comparison_mode, l.model_alias,
         l.provider, l.model, l.gateway_request_id, l.state, l.technical_outcome,
         l.transport_delivered, l.http_status, l.input_tokens, l.output_tokens, l.reported_cost,
         l.duration_ms, l.started_at, l.finished_at
  FROM usage.llm_calls l
  JOIN tenancy.tenants t ON t.id = l.tenant_id
  WHERE l.started_at >= p_from AND l.started_at < p_to
    AND (p_tenant_id IS NULL OR l.tenant_id = p_tenant_id)
    AND (p_inspection_id IS NULL OR l.inspection_id = p_inspection_id)
    AND (p_provider IS NULL OR l.provider ILIKE '%' || p_provider || '%')
    AND (p_model IS NULL OR l.model ILIKE '%' || p_model || '%')
    AND (p_model_alias IS NULL OR l.model_alias ILIKE '%' || p_model_alias || '%')
    AND (p_mode IS NULL OR l.mode = p_mode)
    AND (p_outcome IS NULL OR l.technical_outcome ILIKE '%' || p_outcome || '%')
    AND (p_state IS NULL OR l.state = p_state)
    AND (
      p_cost_state IS NULL
      OR (p_cost_state = 'informed' AND l.transport_delivered IS TRUE AND l.reported_cost IS NOT NULL)
      OR (p_cost_state = 'missing' AND l.transport_delivered IS TRUE AND l.reported_cost IS NULL)
    )
    AND (p_after_started_at IS NULL OR l.started_at < p_after_started_at OR (l.started_at = p_after_started_at AND l.call_id < p_after_call_id))
  ORDER BY l.started_at DESC, l.call_id DESC
  LIMIT p_limit
$function$;
REVOKE ALL ON FUNCTION usage.read_llm_usage_calls(timestamptz, timestamptz, uuid, uuid, text, text, text, text, text, text, text, timestamptz, uuid, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION usage.read_llm_usage_calls(timestamptz, timestamptz, uuid, uuid, text, text, text, text, text, text, text, timestamptz, uuid, integer) TO inspection_runtime;

CREATE OR REPLACE FUNCTION usage.read_llm_usage_tenants(
  p_search text,
  p_after_name text,
  p_after_id uuid,
  p_limit integer
)
RETURNS TABLE(
  id uuid,
  name varchar,
  language varchar,
  default_timezone varchar,
  status varchar,
  version bigint
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = tenancy, usage, platform, pg_catalog
AS $function$
  SELECT t.id, t.name, t.language, t.default_timezone, t.status, t.version
  FROM tenancy.tenants t
  WHERE (p_search IS NULL OR t.name ILIKE '%' || p_search || '%' OR t.id::text ILIKE '%' || p_search || '%')
    AND (p_after_name IS NULL OR t.name > p_after_name OR (t.name = p_after_name AND t.id > p_after_id))
  ORDER BY t.name ASC, t.id ASC
  LIMIT p_limit
$function$;
REVOKE ALL ON FUNCTION usage.read_llm_usage_tenants(text, text, uuid, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION usage.read_llm_usage_tenants(text, text, uuid, integer) TO inspection_runtime;
`},
		{Version: 42, Name: "remove_analysis_finding_change_type", Destructive: true, Compatible: true, SQL: `
ALTER TABLE analysis.findings DROP COLUMN IF EXISTS change_type;
`},
		{Version: 43, Name: "origin_attention_items", Compatible: true, SQL: `
ALTER TABLE media.media_objects ADD COLUMN IF NOT EXISTS attention_items jsonb;
UPDATE media.media_objects SET attention_items = '[]'::jsonb WHERE attention_items IS NULL;
ALTER TABLE media.media_objects ALTER COLUMN attention_items SET DEFAULT '[]'::jsonb;
ALTER TABLE media.media_objects ALTER COLUMN attention_items SET NOT NULL;
ALTER TABLE origins.origin_evidence ADD COLUMN IF NOT EXISTS attention_items jsonb;
UPDATE origins.origin_evidence SET attention_items = '[]'::jsonb WHERE attention_items IS NULL;
ALTER TABLE origins.origin_evidence ALTER COLUMN attention_items SET DEFAULT '[]'::jsonb;
ALTER TABLE origins.origin_evidence ALTER COLUMN attention_items SET NOT NULL;
ALTER TABLE media.media_objects DROP CONSTRAINT IF EXISTS chk_media_attention_items;
ALTER TABLE media.media_objects ADD CONSTRAINT chk_media_attention_items CHECK (jsonb_typeof(attention_items) = 'array');
ALTER TABLE origins.origin_evidence DROP CONSTRAINT IF EXISTS chk_origin_evidence_attention_items;
ALTER TABLE origins.origin_evidence ADD CONSTRAINT chk_origin_evidence_attention_items CHECK (jsonb_typeof(attention_items) = 'array');
`},
		{Version: 44, Name: "llm_cache_usage_and_request_sizes", Compatible: true, SQL: `
ALTER TABLE usage.llm_calls ADD COLUMN IF NOT EXISTS cached_input_tokens bigint;
ALTER TABLE usage.llm_calls ADD COLUMN IF NOT EXISTS image_count integer;
ALTER TABLE usage.llm_calls ADD COLUMN IF NOT EXISTS request_body_bytes bigint;
ALTER TABLE usage.llm_calls DROP CONSTRAINT IF EXISTS chk_llm_call_cache_usage;
ALTER TABLE usage.llm_calls ADD CONSTRAINT chk_llm_call_cache_usage CHECK (
  (cached_input_tokens IS NULL OR (cached_input_tokens >= 0 AND (input_tokens IS NULL OR cached_input_tokens <= input_tokens)))
  AND (image_count IS NULL OR image_count >= 0)
  AND (request_body_bytes IS NULL OR request_body_bytes >= 0)
);

CREATE OR REPLACE FUNCTION usage.read_llm_cache_summary_v1(
  p_from timestamptz, p_to timestamptz, p_tenant_id uuid, p_inspection_id uuid,
  p_provider text, p_model text, p_model_alias text, p_mode text, p_outcome text,
  p_state text, p_cost_state text
)
RETURNS TABLE(cached_input_tokens bigint, cache_hit_calls bigint, known_cache_calls bigint, unknown_cache_calls bigint)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = usage, pg_catalog
AS $function$
  SELECT
    coalesce(sum(l.cached_input_tokens) FILTER (WHERE l.transport_delivered IS TRUE), 0)::bigint,
    count(*) FILTER (WHERE l.transport_delivered IS TRUE AND l.cached_input_tokens > 0)::bigint,
    count(*) FILTER (WHERE l.transport_delivered IS TRUE AND l.cached_input_tokens IS NOT NULL)::bigint,
    count(*) FILTER (WHERE l.transport_delivered IS TRUE AND l.cached_input_tokens IS NULL)::bigint
  FROM usage.llm_calls l
  WHERE l.started_at >= p_from AND l.started_at < p_to
    AND (p_tenant_id IS NULL OR l.tenant_id = p_tenant_id)
    AND (p_inspection_id IS NULL OR l.inspection_id = p_inspection_id)
    AND (p_provider IS NULL OR l.provider ILIKE '%' || p_provider || '%')
    AND (p_model IS NULL OR l.model ILIKE '%' || p_model || '%')
    AND (p_model_alias IS NULL OR l.model_alias ILIKE '%' || p_model_alias || '%')
    AND (p_mode IS NULL OR l.mode = p_mode)
    AND (p_outcome IS NULL OR l.technical_outcome ILIKE '%' || p_outcome || '%')
    AND (p_state IS NULL OR l.state = p_state)
    AND (p_cost_state IS NULL
      OR (p_cost_state = 'informed' AND l.transport_delivered IS TRUE AND l.reported_cost IS NOT NULL)
      OR (p_cost_state = 'missing' AND l.transport_delivered IS TRUE AND l.reported_cost IS NULL))
$function$;
REVOKE ALL ON FUNCTION usage.read_llm_cache_summary_v1(timestamptz, timestamptz, uuid, uuid, text, text, text, text, text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION usage.read_llm_cache_summary_v1(timestamptz, timestamptz, uuid, uuid, text, text, text, text, text, text, text) TO inspection_runtime;

CREATE OR REPLACE FUNCTION usage.read_llm_usage_calls_v2(
  p_from timestamptz, p_to timestamptz, p_tenant_id uuid, p_inspection_id uuid,
  p_provider text, p_model text, p_model_alias text, p_mode text, p_outcome text,
  p_state text, p_cost_state text, p_after_started_at timestamptz, p_after_call_id uuid, p_limit integer
)
RETURNS TABLE(
  call_id uuid, tenant_id uuid, tenant_name text, inspection_id uuid, job_id uuid,
  event_id uuid, execution_id uuid, correlation_id varchar, attempt integer,
  replay_generation integer, mode varchar, comparison_mode varchar, model_alias varchar,
  provider varchar, model varchar, gateway_request_id varchar, state varchar,
  technical_outcome varchar, transport_delivered boolean, http_status integer,
  input_tokens bigint, output_tokens bigint, reported_cost numeric, duration_ms bigint,
  started_at timestamptz, finished_at timestamptz, cached_input_tokens bigint,
  image_count integer, request_body_bytes bigint
)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = usage, tenancy, pg_catalog
AS $function$
  SELECT l.call_id, l.tenant_id, t.name::text, l.inspection_id, l.job_id, l.event_id,
         l.execution_id, l.correlation_id, l.attempt, l.replay_generation, l.mode,
         l.comparison_mode, l.model_alias, l.provider, l.model, l.gateway_request_id,
         l.state, l.technical_outcome, l.transport_delivered, l.http_status,
         l.input_tokens, l.output_tokens, l.reported_cost, l.duration_ms,
         l.started_at, l.finished_at, l.cached_input_tokens, l.image_count, l.request_body_bytes
  FROM usage.llm_calls l JOIN tenancy.tenants t ON t.id = l.tenant_id
  WHERE l.started_at >= p_from AND l.started_at < p_to
    AND (p_tenant_id IS NULL OR l.tenant_id = p_tenant_id)
    AND (p_inspection_id IS NULL OR l.inspection_id = p_inspection_id)
    AND (p_provider IS NULL OR l.provider ILIKE '%' || p_provider || '%')
    AND (p_model IS NULL OR l.model ILIKE '%' || p_model || '%')
    AND (p_model_alias IS NULL OR l.model_alias ILIKE '%' || p_model_alias || '%')
    AND (p_mode IS NULL OR l.mode = p_mode)
    AND (p_outcome IS NULL OR l.technical_outcome ILIKE '%' || p_outcome || '%')
    AND (p_state IS NULL OR l.state = p_state)
    AND (p_cost_state IS NULL
      OR (p_cost_state = 'informed' AND l.transport_delivered IS TRUE AND l.reported_cost IS NOT NULL)
      OR (p_cost_state = 'missing' AND l.transport_delivered IS TRUE AND l.reported_cost IS NULL))
    AND (p_after_started_at IS NULL OR l.started_at < p_after_started_at OR (l.started_at = p_after_started_at AND l.call_id < p_after_call_id))
  ORDER BY l.started_at DESC, l.call_id DESC LIMIT p_limit
$function$;
REVOKE ALL ON FUNCTION usage.read_llm_usage_calls_v2(timestamptz, timestamptz, uuid, uuid, text, text, text, text, text, text, text, timestamptz, uuid, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION usage.read_llm_usage_calls_v2(timestamptz, timestamptz, uuid, uuid, text, text, text, text, text, text, text, timestamptz, uuid, integer) TO inspection_runtime;
`},
		{Version: 45, Name: "media_derivative_normalization_metadata", Compatible: true, SQL: `
ALTER TABLE media.derivatives ADD COLUMN IF NOT EXISTS content_type varchar(100) NOT NULL DEFAULT 'image/jpeg';
ALTER TABLE media.derivatives ADD COLUMN IF NOT EXISTS width integer;
ALTER TABLE media.derivatives ADD COLUMN IF NOT EXISTS height integer;
ALTER TABLE media.derivatives ADD COLUMN IF NOT EXISTS size_bytes bigint;
ALTER TABLE media.derivatives ADD COLUMN IF NOT EXISTS profile varchar(100);
ALTER TABLE media.derivatives DROP CONSTRAINT IF EXISTS chk_media_derivative_dimensions;
ALTER TABLE media.derivatives ADD CONSTRAINT chk_media_derivative_dimensions CHECK (
  (width IS NULL OR width > 0) AND (height IS NULL OR height > 0) AND (size_bytes IS NULL OR size_bytes >= 0)
);
`},
		{Version: 46, Name: "dashboard_triage_review_cases", Compatible: true, SQL: `
CREATE UNIQUE INDEX IF NOT EXISTS idx_triage_case_tenant_inspection ON dashboard.triage_cases(tenant_id, inspection_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_triage_case_event_mutation ON dashboard.triage_case_events(tenant_id, client_mutation_id) WHERE client_mutation_id <> '';
INSERT INTO dashboard.triage_cases(id,tenant_id,inspection_id,status,classification,reason_codes,report_version,version,created_at,updated_at)
SELECT gen_random_uuid(), d.tenant_id, d.inspection_id, 'NEW', c.classification, c.reason_codes,
       coalesce(r.version_number, 0), 1, now(), now()
FROM dashboard.inspections d
JOIN LATERAL (SELECT classification,reason_codes FROM analysis.classification_runs c WHERE c.tenant_id=d.tenant_id AND c.inspection_id=d.inspection_id ORDER BY created_at DESC LIMIT 1) c ON true
LEFT JOIN LATERAL (SELECT version_number FROM reports.report_snapshots r WHERE r.tenant_id=d.tenant_id AND r.inspection_id=d.inspection_id ORDER BY version_number DESC LIMIT 1) r ON true
WHERE NOT d.invalidated AND c.classification IN ('CRITICAL','ATTENTION')
ON CONFLICT (tenant_id,inspection_id) DO NOTHING;
ALTER TABLE dashboard.triage_cases ENABLE ROW LEVEL SECURITY;
ALTER TABLE dashboard.triage_cases FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON dashboard.triage_cases;
CREATE POLICY tenant_isolation ON dashboard.triage_cases USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE dashboard.triage_case_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE dashboard.triage_case_events FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON dashboard.triage_case_events;
CREATE POLICY tenant_isolation ON dashboard.triage_case_events USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
CREATE INDEX IF NOT EXISTS idx_triage_case_queue ON dashboard.triage_cases(tenant_id, status, classification, updated_at);
CREATE INDEX IF NOT EXISTS idx_triage_case_events_history ON dashboard.triage_case_events(tenant_id, case_id, created_at);
GRANT USAGE ON SCHEMA dashboard TO inspection_runtime;
GRANT SELECT, INSERT, UPDATE ON dashboard.triage_cases TO inspection_runtime;
GRANT SELECT, INSERT ON dashboard.triage_case_events TO inspection_runtime;
`},
		{Version: 47, Name: "usage_daily_summary_tenant_day_index", Compatible: true, SQL: `
DROP INDEX IF EXISTS usage.idx_usage_daily;
CREATE UNIQUE INDEX idx_usage_daily ON usage.daily_summaries(tenant_id, day);
`},
		{Version: 48, Name: "structured_brazilian_asset_addresses", Compatible: true, SQL: `
UPDATE assets.assets
SET legacy_address = address, address_status = 'LEGACY'
WHERE legacy_address = '' AND address <> '';
CREATE INDEX IF NOT EXISTS idx_asset_tenant_postal ON assets.assets(tenant_id, address_postal_code);
CREATE INDEX IF NOT EXISTS idx_asset_tenant_city_state ON assets.assets(tenant_id, address_city, address_state);
ALTER TABLE assets.assets DROP CONSTRAINT IF EXISTS chk_asset_address_status;
ALTER TABLE assets.assets ADD CONSTRAINT chk_asset_address_status CHECK (address_status IN ('LEGACY','INCOMPLETE','COMPLETE'));
ALTER TABLE assets.assets DROP CONSTRAINT IF EXISTS chk_asset_address_components;
ALTER TABLE assets.assets ADD CONSTRAINT chk_asset_address_components CHECK (
  address_status <> 'COMPLETE' OR (
    address_country_code = 'BR' AND address_postal_code ~ '^[0-9]{8}$' AND
    length(btrim(address_street)) > 0 AND
    ((address_without_number AND address_number = '') OR (NOT address_without_number AND length(btrim(address_number)) > 0)) AND
    length(btrim(address_city)) > 0 AND address_state IN ('AC','AL','AP','AM','BA','CE','DF','ES','GO','MA','MT','MS','MG','PA','PB','PR','PE','PI','RJ','RN','RS','RO','RR','SC','SP','SE','TO')
  )
);
`},
		{Version: 49, Name: "email_based_access_invitations", Compatible: true, SQL: `
ALTER TABLE access.memberships ADD COLUMN IF NOT EXISTS name varchar(200) NOT NULL DEFAULT '';
ALTER TABLE access.memberships ADD COLUMN IF NOT EXISTS email varchar(320) NOT NULL DEFAULT '';
ALTER TABLE access.memberships ADD COLUMN IF NOT EXISTS invitation_status varchar(24) NOT NULL DEFAULT 'NONE';
CREATE INDEX IF NOT EXISTS idx_memberships_tenant_email ON access.memberships(tenant_id, email);
ALTER TABLE access.user_invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE access.user_invitations FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON access.user_invitations;
CREATE POLICY tenant_isolation ON access.user_invitations USING (
  tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid
  OR token_digest = decode(nullif(current_setting('app.user_invitation_digest', true), ''), 'hex')
  OR session_digest = decode(nullif(current_setting('app.user_invitation_session', true), ''), 'hex')
) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
DROP POLICY IF EXISTS invitation_token_lookup ON access.user_invitations;
DROP POLICY IF EXISTS invitation_session_lookup ON access.user_invitations;
ALTER TABLE access.user_invitation_otp_challenges ENABLE ROW LEVEL SECURITY;
ALTER TABLE access.user_invitation_otp_challenges FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON access.user_invitation_otp_challenges;
CREATE POLICY tenant_isolation ON access.user_invitation_otp_challenges USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON access.user_invitations, access.user_invitation_otp_challenges TO inspection_runtime;
`},
		{Version: 50, Name: "unified_inspection_planning", Compatible: true, SQL: `
ALTER TABLE projects.projects ADD COLUMN IF NOT EXISTS name varchar(200) NOT NULL DEFAULT '';
ALTER TABLE projects.projects ADD COLUMN IF NOT EXISTS ordered_stages boolean NOT NULL DEFAULT false;
ALTER TABLE projects.projects DROP COLUMN IF EXISTS participant_id;
ALTER TABLE projects.projects DROP COLUMN IF EXISTS template_id;
ALTER TABLE projects.projects DROP COLUMN IF EXISTS template_version_id;
ALTER TABLE projects.projects DROP COLUMN IF EXISTS report_mode;
ALTER TABLE projects.project_stages DROP COLUMN IF EXISTS inspection_id;
ALTER TABLE schedules.schedules ADD COLUMN IF NOT EXISTS project_id uuid;
ALTER TABLE schedules.schedules ADD COLUMN IF NOT EXISTS stage_id uuid;
ALTER TABLE schedules.schedules ADD COLUMN IF NOT EXISTS comparison_mode varchar(32) NOT NULL DEFAULT 'CHECKLIST_ONLY';
ALTER TABLE schedules.schedules ADD COLUMN IF NOT EXISTS template_version_id uuid;
UPDATE schedules.schedules s SET template_version_id=t.active_version_id FROM templates.templates t WHERE s.tenant_id=t.tenant_id AND s.template_id=t.id AND s.template_version_id IS NULL AND t.active_version_id IS NOT NULL;
UPDATE schedules.schedules s SET template_version_id=(
  SELECT v.id FROM templates.template_versions v WHERE v.tenant_id=s.tenant_id AND v.template_id=s.template_id ORDER BY v.version_number DESC LIMIT 1
) WHERE s.template_version_id IS NULL;
UPDATE schedules.schedules s SET comparison_mode=v.definition_json->>'defaultComparisonMode'
FROM templates.template_versions v
WHERE v.tenant_id=s.tenant_id AND v.id=s.template_version_id
  AND v.definition_json->>'defaultComparisonMode' IN ('CHECKLIST_ONLY','FIXED_ORIGIN');
-- Schedules whose template never had a version cannot run and cannot satisfy NOT NULL.
DELETE FROM schedules.schedules WHERE template_version_id IS NULL;
ALTER TABLE schedules.schedules ALTER COLUMN template_version_id SET NOT NULL;
CREATE INDEX IF NOT EXISTS idx_schedules_project_id ON schedules.schedules(project_id);
CREATE INDEX IF NOT EXISTS idx_schedules_stage_id ON schedules.schedules(stage_id);
DROP INDEX IF EXISTS origins.idx_origin_context;
DROP TABLE IF EXISTS pg_temp.origin_merge;
CREATE TEMP TABLE origin_merge AS
SELECT o.id AS dup_id, first_value(o.id) OVER (
  PARTITION BY o.tenant_id, o.asset_id
  ORDER BY EXISTS(SELECT 1 FROM origins.origin_versions v WHERE v.tenant_id=o.tenant_id AND v.origin_id=o.id AND v.status='ACTIVE') DESC, o.created_at, o.id
) AS keep_id
FROM origins.origins o;
DELETE FROM origin_merge WHERE dup_id=keep_id;
UPDATE origins.origin_versions v SET origin_id=r.keep_id, version_number=r.base+r.rn,
  status=CASE WHEN v.status='ACTIVE' THEN 'SUPERSEDED' ELSE v.status END
FROM (
  SELECT ov.id, m.keep_id,
    row_number() OVER (PARTITION BY m.keep_id ORDER BY ov.created_at, ov.id) AS rn,
    (SELECT coalesce(max(k.version_number),0) FROM origins.origin_versions k WHERE k.tenant_id=ov.tenant_id AND k.origin_id=m.keep_id) AS base
  FROM origins.origin_versions ov JOIN origin_merge m ON m.dup_id=ov.origin_id
) r WHERE v.id=r.id;
DELETE FROM origins.origins o USING origin_merge m WHERE o.id=m.dup_id;
DROP TABLE origin_merge;
ALTER TABLE origins.origins DROP COLUMN IF EXISTS template_id;
CREATE UNIQUE INDEX IF NOT EXISTS idx_origin_asset ON origins.origins(tenant_id, asset_id);
`},
		{Version: 51, Name: "scheduler_tenant_discovery", Compatible: true, SQL: `
CREATE OR REPLACE FUNCTION platform.discover_scheduler_tenants(
  p_now timestamptz,
  p_terminal_updated_before timestamptz,
  p_deadline_before timestamptz
) RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog
AS $$
  SELECT s.tenant_id FROM schedules.schedules s
  WHERE s.status='ACTIVE' AND s.next_due_at<=p_now
  UNION
  SELECT r.tenant_id FROM schedules.reminder_plans r
  WHERE r.status='PLANNED' AND r.remind_at<=p_now
  UNION
  SELECT i.tenant_id FROM inspections.inspections i
  WHERE i.status IN ('COMPLETED','CANCELED','INVALIDATED') AND i.updated_at<=p_terminal_updated_before
  UNION
  SELECT i.tenant_id FROM inspections.inspections i
  WHERE i.status IN ('PLANNED','INVITED','IN_PROGRESS','SUBMITTED','ANALYZING') AND i.deadline_at<=p_deadline_before
$$;
REVOKE ALL ON FUNCTION platform.discover_scheduler_tenants(timestamptz, timestamptz, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.discover_scheduler_tenants(timestamptz, timestamptz, timestamptz) TO inspection_runtime;
`},
		{Version: 52, Name: "backfill_triage_cases_from_classifications", Compatible: true, SQL: `
INSERT INTO dashboard.triage_cases(id,tenant_id,inspection_id,status,classification,reason_codes,report_version,version,created_at,updated_at)
SELECT gen_random_uuid(), i.tenant_id, i.id, 'NEW', c.classification, c.reason_codes,
       coalesce(r.version_number, 0), 1, now(), now()
FROM inspections.inspections i
JOIN LATERAL (
  SELECT classification, reason_codes
  FROM analysis.classification_runs c
  WHERE c.tenant_id=i.tenant_id AND c.inspection_id=i.id
  ORDER BY c.created_at DESC
  LIMIT 1
) c ON true
LEFT JOIN LATERAL (
  SELECT version_number
  FROM reports.report_snapshots r
  WHERE r.tenant_id=i.tenant_id AND r.inspection_id=i.id
  ORDER BY r.version_number DESC
  LIMIT 1
) r ON true
WHERE i.status NOT IN ('INVALIDATED','CANCELED')
  AND c.classification IN ('CRITICAL','ATTENTION')
ON CONFLICT (tenant_id,inspection_id) DO NOTHING;
`},
		{Version: 53, Name: "durable_report_pdf_jobs", Compatible: true, SQL: `
ALTER TABLE reports.report_snapshots ADD COLUMN IF NOT EXISTS pdf_render_version integer NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS reports.report_pdf_jobs (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  snapshot_id uuid NOT NULL,
  audience varchar(32) NOT NULL,
  renderer_version integer NOT NULL,
  status varchar(16) NOT NULL,
  attempts integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  lease_token uuid,
  lease_expires_at timestamptz,
  artifact_id uuid,
  pending_object_key varchar(1000) NOT NULL DEFAULT '',
  last_error varchar(200) NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uq_report_pdf_job UNIQUE (tenant_id,snapshot_id,audience),
  CONSTRAINT chk_report_pdf_job_audience CHECK (audience IN ('PDF','PDF_CUSTOMER')),
  CONSTRAINT chk_report_pdf_job_status CHECK (status IN ('QUEUED','PROCESSING','READY','FAILED','CANCELED')),
  CONSTRAINT chk_report_pdf_job_attempts CHECK (attempts >= 0 AND attempts <= 20)
);
CREATE INDEX IF NOT EXISTS idx_report_pdf_ready ON reports.report_pdf_jobs(status,next_attempt_at,created_at);
ALTER TABLE reports.report_pdf_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE reports.report_pdf_jobs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON reports.report_pdf_jobs;
CREATE POLICY tenant_isolation ON reports.report_pdf_jobs
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
GRANT USAGE ON SCHEMA reports TO inspection_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON reports.report_pdf_jobs TO inspection_runtime;
`},
		{Version: 54, Name: "video_capture_contracts", Compatible: true, SQL: `
ALTER TABLE inspections.inspections ADD COLUMN IF NOT EXISTS media_kind varchar(8) NOT NULL DEFAULT 'PHOTO';
ALTER TABLE capture.capture_drafts ADD COLUMN IF NOT EXISTS media_kind varchar(8) NOT NULL DEFAULT 'PHOTO';
ALTER TABLE media.media_objects ADD COLUMN IF NOT EXISTS media_kind varchar(8) NOT NULL DEFAULT 'PHOTO';
ALTER TABLE media.media_objects ADD COLUMN IF NOT EXISTS video_attempt_id uuid;
ALTER TABLE invitations.processing_acceptances ADD COLUMN IF NOT EXISTS video_processing boolean NOT NULL DEFAULT false;
ALTER TABLE inspections.inspections ADD CONSTRAINT chk_inspection_media_kind CHECK (media_kind IN ('PHOTO','VIDEO'));
ALTER TABLE capture.capture_drafts ADD CONSTRAINT chk_capture_draft_media_kind CHECK (media_kind IN ('PHOTO','VIDEO'));
ALTER TABLE media.media_objects ADD CONSTRAINT chk_media_object_media_kind CHECK (media_kind IN ('PHOTO','VIDEO'));
ALTER TABLE media.media_objects DROP CONSTRAINT IF EXISTS chk_media_size;
ALTER TABLE media.media_objects ADD CONSTRAINT chk_media_size CHECK ((media_kind='PHOTO' AND size_bytes BETWEEN 1 AND 20971520) OR (media_kind='VIDEO' AND size_bytes BETWEEN 1 AND 1073741824));
ALTER TABLE media.media_objects DROP CONSTRAINT IF EXISTS chk_media_status;
ALTER TABLE media.media_objects ADD CONSTRAINT chk_media_status CHECK (status IN ('INITIATED','UPLOADING','COMPLETING','UPLOADED','RECEIVED','VERIFIED','SCREENED','READY','REJECTED','FAILED','ABORTED','PURGED'));
CREATE UNIQUE INDEX IF NOT EXISTS uq_media_tenant_id ON media.media_objects(tenant_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_responsibility_tenant_id ON inspections.responsibilities(tenant_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_submission_tenant_id ON capture.submission_versions(tenant_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_inspection_tenant_id ON inspections.inspections(tenant_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_origin_version_tenant_id ON origins.origin_versions(tenant_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_video_attempt_tenant_id ON capture.video_attempts(tenant_id,id);
ALTER TABLE capture.video_attempts DROP CONSTRAINT IF EXISTS fk_video_attempt_responsibility;
ALTER TABLE capture.video_attempts ADD CONSTRAINT fk_video_attempt_responsibility FOREIGN KEY(tenant_id,responsibility_id) REFERENCES inspections.responsibilities(tenant_id,id);
ALTER TABLE media.media_objects DROP CONSTRAINT IF EXISTS fk_video_attempt;
ALTER TABLE media.media_objects ADD CONSTRAINT fk_video_attempt FOREIGN KEY(tenant_id,video_attempt_id) REFERENCES capture.video_attempts(tenant_id,id);
CREATE TABLE IF NOT EXISTS media.video_policies (
  tenant_id uuid PRIMARY KEY,
  max_duration_minutes integer NOT NULL DEFAULT 15 CHECK (max_duration_minutes BETWEEN 1 AND 60),
  origin_retention_days integer NOT NULL DEFAULT 30 CHECK (origin_retention_days BETWEEN 1 AND 36500),
  current_retention_days integer NOT NULL DEFAULT 5 CHECK (current_retention_days BETWEEN 1 AND 36500),
  version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  last_mutation_id varchar(200) NOT NULL DEFAULT '',
  last_payload_digest varchar(64) NOT NULL DEFAULT '',
  updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE media.video_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE media.video_policies FORCE ROW LEVEL SECURITY;
ALTER TABLE media.video_policies DROP CONSTRAINT IF EXISTS chk_video_policy_duration;
ALTER TABLE media.video_policies ADD CONSTRAINT chk_video_policy_duration CHECK (max_duration_minutes BETWEEN 1 AND 60);
ALTER TABLE media.video_policies DROP CONSTRAINT IF EXISTS chk_video_policy_origin_retention;
ALTER TABLE media.video_policies ADD CONSTRAINT chk_video_policy_origin_retention CHECK (origin_retention_days BETWEEN 1 AND 36500);
ALTER TABLE media.video_policies DROP CONSTRAINT IF EXISTS chk_video_policy_current_retention;
ALTER TABLE media.video_policies ADD CONSTRAINT chk_video_policy_current_retention CHECK (current_retention_days BETWEEN 1 AND 36500);
CREATE POLICY tenant_isolation ON media.video_policies USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE ON media.video_policies TO inspection_runtime;
CREATE TABLE IF NOT EXISTS platform.video_admission (
  id integer PRIMARY KEY CHECK (id=1), enabled boolean NOT NULL DEFAULT false, version bigint NOT NULL DEFAULT 1 CHECK (version>0),
  changed_by uuid, changed_at timestamptz NOT NULL DEFAULT now(), reason varchar(2000) NOT NULL DEFAULT '',
  mutation_id varchar(200) NOT NULL DEFAULT '', payload_hash varchar(64) NOT NULL DEFAULT ''
);
INSERT INTO platform.video_admission(id,enabled,version,changed_at) VALUES (1,false,1,now()) ON CONFLICT (id) DO NOTHING;
ALTER TABLE platform.video_admission DROP CONSTRAINT IF EXISTS chk_video_admission_singleton;
ALTER TABLE platform.video_admission ADD CONSTRAINT chk_video_admission_singleton CHECK (id=1);
GRANT SELECT, UPDATE ON platform.video_admission TO inspection_runtime;
CREATE TABLE IF NOT EXISTS capture.video_attempts (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, responsibility_id uuid NOT NULL, kind varchar(16) NOT NULL,
  policy_version bigint NOT NULL, max_duration_seconds integer NOT NULL CHECK (max_duration_seconds BETWEEN 60 AND 3600),
  max_bytes bigint NOT NULL CHECK (max_bytes BETWEEN 1 AND 1073741824), acquisition_source varchar(16) NOT NULL,
  status varchar(16) NOT NULL, idempotency_key varchar(200) NOT NULL, payload_digest varchar(64) NOT NULL,
  admitted_at timestamptz NOT NULL, expires_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id,id), UNIQUE (tenant_id,responsibility_id,idempotency_key),
  CHECK (kind IN ('ORIGIN','CURRENT','SUPPLEMENT')), CHECK (acquisition_source IN ('CAMERA','FILE')),
  CHECK (status IN ('ACTIVE','RECEIVED','CANCELED','EXPIRED','REJECTED'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_video_attempt_one_active ON capture.video_attempts(tenant_id,responsibility_id) WHERE status='ACTIVE';
CREATE INDEX IF NOT EXISTS idx_video_attempt_expiry ON capture.video_attempts(status,expires_at);
ALTER TABLE capture.video_attempts DROP CONSTRAINT IF EXISTS chk_video_attempt_duration;
ALTER TABLE capture.video_attempts ADD CONSTRAINT chk_video_attempt_duration CHECK (max_duration_seconds BETWEEN 60 AND 3600);
ALTER TABLE capture.video_attempts DROP CONSTRAINT IF EXISTS chk_video_attempt_bytes;
ALTER TABLE capture.video_attempts ADD CONSTRAINT chk_video_attempt_bytes CHECK (max_bytes BETWEEN 1 AND 1073741824);
ALTER TABLE capture.video_attempts DROP CONSTRAINT IF EXISTS chk_video_attempt_kind;
ALTER TABLE capture.video_attempts ADD CONSTRAINT chk_video_attempt_kind CHECK (kind IN ('ORIGIN','CURRENT','SUPPLEMENT'));
ALTER TABLE capture.video_attempts DROP CONSTRAINT IF EXISTS chk_video_attempt_source;
ALTER TABLE capture.video_attempts ADD CONSTRAINT chk_video_attempt_source CHECK (acquisition_source IN ('CAMERA','FILE'));
ALTER TABLE capture.video_attempts DROP CONSTRAINT IF EXISTS chk_video_attempt_status;
ALTER TABLE capture.video_attempts ADD CONSTRAINT chk_video_attempt_status CHECK (status IN ('ACTIVE','RECEIVED','CANCELED','EXPIRED','REJECTED'));
ALTER TABLE capture.video_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE capture.video_attempts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON capture.video_attempts USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE ON capture.video_attempts TO inspection_runtime;
CREATE TABLE IF NOT EXISTS media.video_preparation_jobs (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, media_id uuid NOT NULL, submission_id uuid NOT NULL,
  status varchar(16) NOT NULL DEFAULT 'QUEUED', attempts integer NOT NULL DEFAULT 0, next_attempt_at timestamptz NOT NULL DEFAULT now(),
  lease_token uuid, lease_expires_at timestamptz, created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(tenant_id,media_id), CHECK (status IN ('QUEUED','PROCESSING','READY','FAILED','CANCELED'))
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_video_job_tenant_id ON media.video_preparation_jobs(tenant_id,id);
ALTER TABLE media.video_preparation_jobs DROP CONSTRAINT IF EXISTS fk_video_job_media;
ALTER TABLE media.video_preparation_jobs ADD CONSTRAINT fk_video_job_media FOREIGN KEY(tenant_id,media_id) REFERENCES media.media_objects(tenant_id,id);
ALTER TABLE media.video_preparation_jobs DROP CONSTRAINT IF EXISTS fk_video_job_submission;
ALTER TABLE media.video_preparation_jobs ADD CONSTRAINT fk_video_job_submission FOREIGN KEY(tenant_id,submission_id) REFERENCES capture.submission_versions(tenant_id,id);
CREATE INDEX IF NOT EXISTS idx_video_preparation_due ON media.video_preparation_jobs(status,next_attempt_at,created_at);
ALTER TABLE media.video_preparation_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE media.video_preparation_jobs FORCE ROW LEVEL SECURITY;
ALTER TABLE media.video_preparation_jobs DROP CONSTRAINT IF EXISTS chk_video_preparation_job_status;
ALTER TABLE media.video_preparation_jobs ADD CONSTRAINT chk_video_preparation_job_status CHECK (status IN ('QUEUED','PROCESSING','READY','FAILED','CANCELED'));
CREATE POLICY tenant_isolation ON media.video_preparation_jobs USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE ON media.video_preparation_jobs TO inspection_runtime;
CREATE TABLE IF NOT EXISTS media.video_frames (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, source_media_id uuid NOT NULL, preparation_id uuid NOT NULL,
  pts_us bigint NOT NULL CHECK(pts_us>=0), sha256 varchar(64) NOT NULL, object_key varchar(1000) NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(tenant_id,id), UNIQUE(tenant_id,preparation_id,pts_us,sha256)
);
ALTER TABLE media.video_frames DROP CONSTRAINT IF EXISTS fk_video_frame_source;
ALTER TABLE media.video_frames ADD CONSTRAINT fk_video_frame_source FOREIGN KEY(tenant_id,source_media_id) REFERENCES media.media_objects(tenant_id,id);
ALTER TABLE media.video_frames DROP CONSTRAINT IF EXISTS fk_video_frame_preparation;
ALTER TABLE media.video_frames ADD CONSTRAINT fk_video_frame_preparation FOREIGN KEY(tenant_id,preparation_id) REFERENCES media.video_preparation_jobs(tenant_id,id);
ALTER TABLE media.video_frames ENABLE ROW LEVEL SECURITY;
ALTER TABLE media.video_frames FORCE ROW LEVEL SECURITY;
ALTER TABLE media.video_frames DROP CONSTRAINT IF EXISTS chk_video_frame_pts;
ALTER TABLE media.video_frames ADD CONSTRAINT chk_video_frame_pts CHECK (pts_us >= 0);
CREATE POLICY tenant_isolation ON media.video_frames USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON media.video_frames TO inspection_runtime;
CREATE TABLE IF NOT EXISTS analysis.video_plans (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, inspection_id uuid NOT NULL, submission_id uuid NOT NULL,
  reference_version_id uuid, generation integer NOT NULL, plan_digest varchar(64) NOT NULL, sealed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(tenant_id,id), UNIQUE(tenant_id,submission_id,generation)
);
ALTER TABLE analysis.video_plans DROP CONSTRAINT IF EXISTS fk_video_plan_inspection;
ALTER TABLE analysis.video_plans ADD CONSTRAINT fk_video_plan_inspection FOREIGN KEY(tenant_id,inspection_id) REFERENCES inspections.inspections(tenant_id,id);
ALTER TABLE analysis.video_plans DROP CONSTRAINT IF EXISTS fk_video_plan_submission;
ALTER TABLE analysis.video_plans ADD CONSTRAINT fk_video_plan_submission FOREIGN KEY(tenant_id,submission_id) REFERENCES capture.submission_versions(tenant_id,id);
ALTER TABLE analysis.video_plans DROP CONSTRAINT IF EXISTS fk_video_plan_reference;
ALTER TABLE analysis.video_plans ADD CONSTRAINT fk_video_plan_reference FOREIGN KEY(tenant_id,reference_version_id) REFERENCES origins.origin_versions(tenant_id,id);
CREATE TABLE IF NOT EXISTS media.video_evidence_references (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, preparation_id uuid NOT NULL, owner_type varchar(32) NOT NULL,
  owner_id uuid NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(tenant_id,id),
  CHECK(owner_type IN ('ORIGIN_VERSION','VIDEO_PLAN','REPORT_SNAPSHOT','ACTIVE_WORK'))
);
ALTER TABLE media.video_evidence_references DROP CONSTRAINT IF EXISTS fk_video_evidence_preparation;
ALTER TABLE media.video_evidence_references ADD CONSTRAINT fk_video_evidence_preparation FOREIGN KEY(tenant_id,preparation_id) REFERENCES media.video_preparation_jobs(tenant_id,id);
ALTER TABLE analysis.video_plans ENABLE ROW LEVEL SECURITY;
ALTER TABLE analysis.video_plans FORCE ROW LEVEL SECURITY;
ALTER TABLE analysis.video_plans DROP CONSTRAINT IF EXISTS uq_video_plan_submission_generation;
DROP INDEX IF EXISTS analysis.uq_video_plan_submission_generation;
ALTER TABLE analysis.video_plans ADD CONSTRAINT uq_video_plan_submission_generation UNIQUE(tenant_id,submission_id,generation);
CREATE POLICY tenant_isolation ON analysis.video_plans USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE media.video_evidence_references ENABLE ROW LEVEL SECURITY;
ALTER TABLE media.video_evidence_references FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON media.video_evidence_references USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
GRANT USAGE ON SCHEMA analysis TO inspection_runtime;
GRANT SELECT, INSERT, UPDATE ON analysis.video_plans TO inspection_runtime;
GRANT SELECT, INSERT, DELETE ON media.video_evidence_references TO inspection_runtime;
`},
		{Version: 55, Name: "durable_video_preparation_state", Compatible: true, SQL: `
ALTER TABLE media.video_preparation_jobs ADD COLUMN IF NOT EXISTS phase varchar(32) NOT NULL DEFAULT 'RECEIVED';
ALTER TABLE media.video_preparation_jobs ADD COLUMN IF NOT EXISTS heartbeat_at timestamptz;
ALTER TABLE media.video_preparation_jobs ADD COLUMN IF NOT EXISTS processed_frames integer NOT NULL DEFAULT 0;
ALTER TABLE media.video_preparation_jobs ADD COLUMN IF NOT EXISTS source_sha256 varchar(64) NOT NULL DEFAULT '';
ALTER TABLE media.video_preparation_jobs ADD COLUMN IF NOT EXISTS profile_digest varchar(64) NOT NULL DEFAULT '';
ALTER TABLE media.video_preparation_jobs ADD COLUMN IF NOT EXISTS manifest_digest varchar(64) NOT NULL DEFAULT '';
ALTER TABLE media.video_preparation_jobs ADD COLUMN IF NOT EXISTS pending_object_keys jsonb NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE media.video_preparation_jobs ADD COLUMN IF NOT EXISTS error_code varchar(64) NOT NULL DEFAULT '';
ALTER TABLE media.video_preparation_jobs ADD COLUMN IF NOT EXISTS first_ready_at timestamptz;
CREATE TABLE IF NOT EXISTS media.video_sources (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, media_id uuid NOT NULL,
  container varchar(32) NOT NULL, codec varchar(32) NOT NULL, pixel_format varchar(32) NOT NULL,
  color_transfer varchar(32) NOT NULL, duration_micros bigint NOT NULL CHECK(duration_micros>0),
  width integer NOT NULL CHECK(width>0), height integer NOT NULL CHECK(height>0), rotation integer NOT NULL DEFAULT 0,
  frame_rate double precision NOT NULL CHECK(frame_rate>0), audio_present boolean NOT NULL DEFAULT false,
  screening_status varchar(16) NOT NULL DEFAULT 'PENDING' CHECK(screening_status IN ('PENDING','APPROVED','BLOCKED','FAILED','DISABLED')),
  screening_model_digest varchar(64) NOT NULL DEFAULT '', flagged_region_count integer NOT NULL DEFAULT 0,
  flagged_regions_truncated boolean NOT NULL DEFAULT false,
  first_frame_ready_at timestamptz, retention_days integer, source_expiry_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(tenant_id,media_id), UNIQUE(tenant_id,id),
  FOREIGN KEY(tenant_id,media_id) REFERENCES media.media_objects(tenant_id,id)
);
ALTER TABLE media.video_sources ENABLE ROW LEVEL SECURITY;
ALTER TABLE media.video_sources FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON media.video_sources USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE ON media.video_sources TO inspection_runtime;
ALTER TABLE media.video_frames ADD COLUMN IF NOT EXISTS width integer NOT NULL DEFAULT 1;
ALTER TABLE media.video_frames ADD COLUMN IF NOT EXISTS height integer NOT NULL DEFAULT 1;
ALTER TABLE media.video_frames ADD COLUMN IF NOT EXISTS role varchar(16) NOT NULL DEFAULT 'BASELINE';
ALTER TABLE media.video_frames ADD COLUMN IF NOT EXISTS quality varchar(32) NOT NULL DEFAULT '';
ALTER TABLE media.video_frames ADD COLUMN IF NOT EXISTS size_bytes bigint NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS media.video_scope_items (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, preparation_id uuid NOT NULL, frame_id uuid NOT NULL,
  environment_candidate varchar(120) NOT NULL, component_label varchar(120) NOT NULL,
  view_label varchar(120) NOT NULL, visibility varchar(120) NOT NULL, landmarks jsonb NOT NULL DEFAULT '[]'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(tenant_id,id),
  FOREIGN KEY(tenant_id,preparation_id) REFERENCES media.video_preparation_jobs(tenant_id,id),
  FOREIGN KEY(tenant_id,frame_id) REFERENCES media.video_frames(tenant_id,id)
);
CREATE INDEX IF NOT EXISTS idx_video_scope_preparation ON media.video_scope_items(tenant_id,preparation_id,frame_id);
ALTER TABLE media.video_scope_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE media.video_scope_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON media.video_scope_items USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON media.video_scope_items TO inspection_runtime;
`},
		{Version: 56, Name: "video_source_expiry_leases", Compatible: true, SQL: `
ALTER TABLE media.video_sources ADD COLUMN IF NOT EXISTS status varchar(16) NOT NULL DEFAULT 'RETAINED';
ALTER TABLE media.video_sources ADD COLUMN IF NOT EXISTS delete_lease_token uuid;
ALTER TABLE media.video_sources ADD COLUMN IF NOT EXISTS delete_lease_until timestamptz;
ALTER TABLE media.video_sources ADD COLUMN IF NOT EXISTS original_deleted_at timestamptz;
ALTER TABLE media.video_sources ADD COLUMN IF NOT EXISTS delete_error_code varchar(64) NOT NULL DEFAULT '';
ALTER TABLE origins.origin_versions ADD COLUMN IF NOT EXISTS media_kind varchar(8) NOT NULL DEFAULT 'PHOTO';
ALTER TABLE origins.origin_versions ADD COLUMN IF NOT EXISTS video_preparation_id uuid;
ALTER TABLE origins.origin_versions ADD COLUMN IF NOT EXISTS scope_manifest_digest varchar(64) NOT NULL DEFAULT '';
ALTER TABLE origins.origin_versions DROP CONSTRAINT IF EXISTS fk_origin_video_preparation;
ALTER TABLE origins.origin_versions ADD CONSTRAINT fk_origin_video_preparation FOREIGN KEY(tenant_id,video_preparation_id) REFERENCES media.video_preparation_jobs(tenant_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_video_evidence_owner ON media.video_evidence_references(tenant_id,preparation_id,owner_type,owner_id);
ALTER TABLE media.video_sources DROP CONSTRAINT IF EXISTS chk_video_source_status;
ALTER TABLE media.video_sources ADD CONSTRAINT chk_video_source_status CHECK (status IN ('RETAINED','DELETING','EXPIRED'));
CREATE INDEX IF NOT EXISTS idx_video_source_expiry ON media.video_sources(tenant_id,source_expiry_at,status);
CREATE TABLE IF NOT EXISTS media.video_source_leases (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, source_media_id uuid NOT NULL,
  purpose varchar(24) NOT NULL, lease_until timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(tenant_id,id),
  FOREIGN KEY(tenant_id,source_media_id) REFERENCES media.video_sources(tenant_id,media_id),
  CHECK(purpose IN ('PLAYBACK','EXTRACTION'))
);
CREATE INDEX IF NOT EXISTS idx_video_source_lease ON media.video_source_leases(tenant_id,source_media_id,lease_until);
ALTER TABLE media.video_source_leases ENABLE ROW LEVEL SECURITY;
ALTER TABLE media.video_source_leases FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON media.video_source_leases USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, DELETE ON media.video_source_leases TO inspection_runtime;
`},
		{Version: 57, Name: "sealed_video_plan_membership", Compatible: true, SQL: `
ALTER TABLE analysis.video_plans ADD COLUMN IF NOT EXISTS origin_preparation_id uuid;
ALTER TABLE analysis.video_plans ADD COLUMN IF NOT EXISTS current_preparation_id uuid;
ALTER TABLE analysis.video_plans ADD COLUMN IF NOT EXISTS status varchar(24) NOT NULL DEFAULT 'BUILDING';
ALTER TABLE analysis.video_plans DROP CONSTRAINT IF EXISTS fk_video_plan_origin_preparation;
ALTER TABLE analysis.video_plans ADD CONSTRAINT fk_video_plan_origin_preparation FOREIGN KEY(tenant_id,origin_preparation_id) REFERENCES media.video_preparation_jobs(tenant_id,id);
ALTER TABLE analysis.video_plans DROP CONSTRAINT IF EXISTS fk_video_plan_current_preparation;
ALTER TABLE analysis.video_plans ADD CONSTRAINT fk_video_plan_current_preparation FOREIGN KEY(tenant_id,current_preparation_id) REFERENCES media.video_preparation_jobs(tenant_id,id);
ALTER TABLE analysis.video_plans DROP CONSTRAINT IF EXISTS chk_video_plan_status;
ALTER TABLE analysis.video_plans ADD CONSTRAINT chk_video_plan_status CHECK(status IN ('BUILDING','SEALED','FAILED'));
DROP INDEX IF EXISTS analysis.idx_video_plan_tenant_id;
CREATE UNIQUE INDEX IF NOT EXISTS idx_video_plan_tenant_id ON analysis.video_plans(tenant_id,id);
CREATE TABLE IF NOT EXISTS analysis.video_plan_items (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, plan_id uuid NOT NULL, component_id uuid NOT NULL,
  requirement_key varchar(240) NOT NULL, outcome varchar(24) NOT NULL, reason varchar(120) NOT NULL DEFAULT '',
  input_digest varchar(64) NOT NULL, origin_frame_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
  current_frame_ids jsonb NOT NULL DEFAULT '[]'::jsonb, core_groups jsonb NOT NULL DEFAULT '[]'::jsonb,
  current_groups jsonb NOT NULL DEFAULT '[]'::jsonb, created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(tenant_id,id), UNIQUE(tenant_id,plan_id,component_id),
  FOREIGN KEY(tenant_id,plan_id) REFERENCES analysis.video_plans(tenant_id,id),
  CHECK(outcome IN ('MATCHED','CURRENT_ONLY','INCONCLUSIVE','TECHNICAL_FAILURE'))
);
DROP INDEX IF EXISTS analysis.idx_video_plan_item_tenant_id;
CREATE UNIQUE INDEX IF NOT EXISTS idx_video_plan_item_tenant_id ON analysis.video_plan_items(tenant_id,id);
CREATE TABLE IF NOT EXISTS analysis.video_plan_item_frames (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, plan_item_id uuid NOT NULL, frame_id uuid NOT NULL,
  role varchar(16) NOT NULL, core_group integer NOT NULL DEFAULT 0, pair_order integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(tenant_id,id),
  FOREIGN KEY(tenant_id,plan_item_id) REFERENCES analysis.video_plan_items(tenant_id,id),
  FOREIGN KEY(tenant_id,frame_id) REFERENCES media.video_frames(tenant_id,id),
  CHECK(role IN ('ORIGIN','CURRENT'))
);
CREATE INDEX IF NOT EXISTS idx_video_plan_items ON analysis.video_plan_items(tenant_id,plan_id,requirement_key);
CREATE INDEX IF NOT EXISTS idx_video_plan_item_frames ON analysis.video_plan_item_frames(tenant_id,plan_item_id,core_group,pair_order);
ALTER TABLE analysis.video_plan_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE analysis.video_plan_items FORCE ROW LEVEL SECURITY;
ALTER TABLE analysis.video_plan_item_frames ENABLE ROW LEVEL SECURITY;
ALTER TABLE analysis.video_plan_item_frames FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON analysis.video_plan_items USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY tenant_isolation ON analysis.video_plan_item_frames USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE analysis.comparison_jobs ADD COLUMN IF NOT EXISTS video_plan_id uuid;
ALTER TABLE analysis.comparison_jobs ADD COLUMN IF NOT EXISTS video_plan_item_id uuid;
ALTER TABLE analysis.comparison_jobs ADD COLUMN IF NOT EXISTS video_core_group integer NOT NULL DEFAULT 0;
ALTER TABLE analysis.comparison_jobs DROP CONSTRAINT IF EXISTS fk_comparison_job_video_plan;
ALTER TABLE analysis.comparison_jobs ADD CONSTRAINT fk_comparison_job_video_plan FOREIGN KEY(tenant_id,video_plan_id) REFERENCES analysis.video_plans(tenant_id,id);
ALTER TABLE analysis.comparison_jobs DROP CONSTRAINT IF EXISTS fk_comparison_job_video_plan_item;
ALTER TABLE analysis.comparison_jobs ADD CONSTRAINT fk_comparison_job_video_plan_item FOREIGN KEY(tenant_id,video_plan_item_id) REFERENCES analysis.video_plan_items(tenant_id,id);
ALTER TABLE reports.report_snapshots ADD COLUMN IF NOT EXISTS video_plan_id uuid;
ALTER TABLE reports.report_snapshots DROP CONSTRAINT IF EXISTS fk_report_snapshot_video_plan;
ALTER TABLE reports.report_snapshots ADD CONSTRAINT fk_report_snapshot_video_plan FOREIGN KEY(tenant_id,video_plan_id) REFERENCES analysis.video_plans(tenant_id,id);
GRANT SELECT, INSERT, UPDATE ON analysis.video_plans, analysis.video_plan_items, analysis.video_plan_item_frames TO inspection_runtime;
`},
		{Version: 58, Name: "pin_video_origin_to_analysis_plan", Compatible: true, SQL: `
ALTER TABLE origins.origin_versions ADD COLUMN IF NOT EXISTS video_plan_id uuid;
ALTER TABLE origins.origin_versions DROP CONSTRAINT IF EXISTS fk_origin_video_plan;
ALTER TABLE origins.origin_versions ADD CONSTRAINT fk_origin_video_plan FOREIGN KEY(tenant_id,video_plan_id) REFERENCES analysis.video_plans(tenant_id,id);
GRANT SELECT, INSERT, UPDATE ON origins.origin_versions TO inspection_runtime;
`},
		{Version: 59, Name: "version_video_preparation_retries", Compatible: true, SQL: `
ALTER TABLE media.video_preparation_jobs ADD COLUMN IF NOT EXISTS preparation_version integer NOT NULL DEFAULT 1;
`},
		{Version: 60, Name: "scoped_video_retention_holds", Compatible: true, SQL: `
ALTER TABLE retention.legal_holds DROP CONSTRAINT IF EXISTS legal_holds_pkey;
ALTER TABLE retention.legal_holds ALTER COLUMN inspection_id DROP NOT NULL;
ALTER TABLE retention.legal_holds ADD CONSTRAINT legal_holds_pkey PRIMARY KEY (id,tenant_id);
ALTER TABLE retention.legal_holds ADD COLUMN IF NOT EXISTS source_media_id uuid;
ALTER TABLE retention.legal_holds ADD COLUMN IF NOT EXISTS origin_version_id uuid;
ALTER TABLE retention.deletion_requests DROP CONSTRAINT IF EXISTS deletion_requests_pkey;
ALTER TABLE retention.deletion_requests ALTER COLUMN inspection_id DROP NOT NULL;
ALTER TABLE retention.deletion_requests ADD CONSTRAINT deletion_requests_pkey PRIMARY KEY (id,tenant_id);
ALTER TABLE retention.deletion_requests ADD COLUMN IF NOT EXISTS source_media_id uuid;
ALTER TABLE retention.deletion_requests ADD COLUMN IF NOT EXISTS origin_version_id uuid;
ALTER TABLE retention.legal_holds DROP CONSTRAINT IF EXISTS chk_legal_hold_one_scope;
ALTER TABLE retention.legal_holds ADD CONSTRAINT chk_legal_hold_one_scope CHECK (
  num_nonnulls(inspection_id,source_media_id,origin_version_id) = 1
);
ALTER TABLE retention.deletion_requests DROP CONSTRAINT IF EXISTS chk_deletion_request_one_scope;
ALTER TABLE retention.deletion_requests ADD CONSTRAINT chk_deletion_request_one_scope CHECK (
  num_nonnulls(inspection_id,source_media_id,origin_version_id) = 1
);
ALTER TABLE retention.legal_holds DROP CONSTRAINT IF EXISTS fk_legal_hold_source;
ALTER TABLE retention.legal_holds ADD CONSTRAINT fk_legal_hold_source FOREIGN KEY(tenant_id,source_media_id) REFERENCES media.video_sources(tenant_id,media_id);
ALTER TABLE retention.legal_holds DROP CONSTRAINT IF EXISTS fk_legal_hold_origin;
ALTER TABLE retention.legal_holds ADD CONSTRAINT fk_legal_hold_origin FOREIGN KEY(tenant_id,origin_version_id) REFERENCES origins.origin_versions(tenant_id,id);
ALTER TABLE retention.deletion_requests DROP CONSTRAINT IF EXISTS fk_deletion_request_source;
ALTER TABLE retention.deletion_requests ADD CONSTRAINT fk_deletion_request_source FOREIGN KEY(tenant_id,source_media_id) REFERENCES media.video_sources(tenant_id,media_id);
ALTER TABLE retention.deletion_requests DROP CONSTRAINT IF EXISTS fk_deletion_request_origin;
ALTER TABLE retention.deletion_requests ADD CONSTRAINT fk_deletion_request_origin FOREIGN KEY(tenant_id,origin_version_id) REFERENCES origins.origin_versions(tenant_id,id);
DROP INDEX IF EXISTS retention.idx_retention_hold_active;
CREATE UNIQUE INDEX IF NOT EXISTS idx_retention_hold_inspection_active ON retention.legal_holds(tenant_id,inspection_id) WHERE active AND inspection_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_retention_hold_source_active ON retention.legal_holds(tenant_id,source_media_id) WHERE active AND source_media_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_retention_hold_origin_active ON retention.legal_holds(tenant_id,origin_version_id) WHERE active AND origin_version_id IS NOT NULL;
GRANT SELECT, INSERT, UPDATE, DELETE ON retention.legal_holds, retention.deletion_requests TO inspection_runtime;
`},
		{Version: 61, Name: "persist_video_preparation_generation", Compatible: true, SQL: `
ALTER TABLE media.video_preparation_jobs ADD COLUMN IF NOT EXISTS generation integer NOT NULL DEFAULT 1;
`},
		{Version: 62, Name: "permit_submitted_video_processing_status", Compatible: true, SQL: `
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
     NOT (OLD.replaces_media_id IS NULL AND NEW.replaces_media_id IS NOT NULL AND (to_jsonb(NEW) - 'replaces_media_id') = (to_jsonb(OLD) - 'replaces_media_id')) AND
     NOT (OLD.media_kind = 'VIDEO' AND NEW.media_kind = 'VIDEO' AND
          OLD.status IN ('RECEIVED','VERIFIED','SCREENED') AND NEW.status IN ('VERIFIED','SCREENED','READY','REJECTED') AND
          (to_jsonb(NEW) - 'status') = (to_jsonb(OLD) - 'status') AND
          (NEW.status <> 'READY' OR EXISTS (
            SELECT 1 FROM media.video_preparation_jobs p
            WHERE p.tenant_id = OLD.tenant_id AND p.media_id = OLD.id AND p.status = 'READY'
          )))
  THEN RAISE EXCEPTION 'submitted evidence is immutable'; END IF;
  RETURN NEW;
END $$;
`},
		{Version: 63, Name: "retain_video_scope_identity_ambiguity", Compatible: true, SQL: `
ALTER TABLE media.video_scope_items ADD COLUMN IF NOT EXISTS identity_status varchar(16) NOT NULL DEFAULT 'RESOLVED';
ALTER TABLE media.video_scope_items DROP CONSTRAINT IF EXISTS chk_video_scope_identity_status;
ALTER TABLE media.video_scope_items ADD CONSTRAINT chk_video_scope_identity_status CHECK (identity_status IN ('RESOLVED','AMBIGUOUS'));
`},
		{Version: 64, Name: "allow_origin_video_scope_ledger_without_inspection", Compatible: true, SQL: `
ALTER TABLE usage.llm_calls DROP CONSTRAINT IF EXISTS llm_calls_pkey;
ALTER TABLE usage.llm_calls ALTER COLUMN inspection_id DROP NOT NULL;
ALTER TABLE usage.llm_calls ADD CONSTRAINT llm_calls_pkey PRIMARY KEY (call_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_llm_calls_tenant_call ON usage.llm_calls(tenant_id,call_id);
ALTER TABLE usage.llm_calls DROP CONSTRAINT IF EXISTS chk_llm_call_inspection_scope;
ALTER TABLE usage.llm_calls ADD CONSTRAINT chk_llm_call_inspection_scope CHECK (inspection_id IS NOT NULL OR comparison_mode = 'VIDEO_SCOPE');
`},
		{Version: 65, Name: "allow_origin_video_attempt_without_inspection", Compatible: true, SQL: `
ALTER TABLE capture.video_attempts DROP CONSTRAINT IF EXISTS fk_video_attempt_responsibility;
CREATE OR REPLACE FUNCTION capture.validate_video_attempt_responsibility() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.kind = 'ORIGIN' THEN
    IF NOT EXISTS (SELECT 1 FROM capture.capture_drafts d WHERE d.tenant_id=NEW.tenant_id AND d.responsibility_id=NEW.responsibility_id AND d.kind='ORIGIN') THEN
      RAISE EXCEPTION 'origin video attempt has no capture responsibility';
    END IF;
  ELSIF NOT EXISTS (SELECT 1 FROM inspections.responsibilities r WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.responsibility_id) THEN
    RAISE EXCEPTION 'video attempt has no inspection responsibility';
  END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS trg_validate_video_attempt_responsibility ON capture.video_attempts;
CREATE TRIGGER trg_validate_video_attempt_responsibility BEFORE INSERT OR UPDATE OF tenant_id,responsibility_id,kind ON capture.video_attempts FOR EACH ROW EXECUTE FUNCTION capture.validate_video_attempt_responsibility();
`},
		{Version: 66, Name: "allow_pre_submission_video_preparation", Compatible: true, SQL: `
ALTER TABLE media.video_preparation_jobs ALTER COLUMN submission_id DROP NOT NULL;
`},
		{Version: 67, Name: "persist_received_video_recapture_state", Compatible: true, SQL: `
ALTER TABLE recapture.requests DROP CONSTRAINT IF EXISTS chk_recapture_status;
ALTER TABLE recapture.requests ADD CONSTRAINT chk_recapture_status CHECK (status IN ('REQUESTED','ACCESSED','RECEIVED','SUBMITTED','EXPIRED','CANCELED'));
DROP INDEX IF EXISTS recapture.idx_recapture_one_active;
CREATE UNIQUE INDEX idx_recapture_one_active ON recapture.requests(tenant_id, inspection_id) WHERE status IN ('REQUESTED','ACCESSED','RECEIVED');
`},
		{Version: 68, Name: "durable_video_plan_execution", Compatible: true, SQL: `
ALTER TABLE analysis.video_plans ADD COLUMN IF NOT EXISTS lease_token uuid;
ALTER TABLE analysis.video_plans ADD COLUMN IF NOT EXISTS lease_until timestamptz;
ALTER TABLE analysis.video_plans ADD COLUMN IF NOT EXISTS attempts integer NOT NULL DEFAULT 0;
ALTER TABLE analysis.video_plans ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE analysis.video_plans ADD COLUMN IF NOT EXISTS error_code varchar(64) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_video_plan_due ON analysis.video_plans(status,next_attempt_at,lease_until,created_at);
ALTER TABLE analysis.video_plans DROP CONSTRAINT IF EXISTS chk_video_plan_attempts;
ALTER TABLE analysis.video_plans ADD CONSTRAINT chk_video_plan_attempts CHECK(attempts >= 0);
GRANT SELECT, INSERT, UPDATE ON analysis.video_plans TO inspection_runtime;
`},
		{Version: 69, Name: "active_video_plan_generation", Compatible: true, SQL: `
ALTER TABLE inspections.inspections ADD COLUMN IF NOT EXISTS active_video_plan_id uuid;
UPDATE inspections.inspections i
SET active_video_plan_id = (
  SELECT p.id FROM analysis.video_plans p
  WHERE p.tenant_id=i.tenant_id AND p.inspection_id=i.id AND p.status='SEALED'
  ORDER BY p.generation DESC, p.created_at DESC, p.id DESC LIMIT 1
)
WHERE i.media_kind='VIDEO' AND i.active_video_plan_id IS NULL
  AND EXISTS (SELECT 1 FROM analysis.video_plans p WHERE p.tenant_id=i.tenant_id AND p.inspection_id=i.id AND p.status='SEALED');
ALTER TABLE inspections.inspections DROP CONSTRAINT IF EXISTS fk_inspection_active_video_plan;
ALTER TABLE inspections.inspections ADD CONSTRAINT fk_inspection_active_video_plan FOREIGN KEY(tenant_id,active_video_plan_id) REFERENCES analysis.video_plans(tenant_id,id);
ALTER TABLE analysis.classification_runs ADD COLUMN IF NOT EXISTS video_plan_id uuid;
ALTER TABLE analysis.classification_runs DROP CONSTRAINT IF EXISTS fk_classification_run_video_plan;
ALTER TABLE analysis.classification_runs ADD CONSTRAINT fk_classification_run_video_plan FOREIGN KEY(tenant_id,video_plan_id) REFERENCES analysis.video_plans(tenant_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_classification_run_video_plan ON analysis.classification_runs(tenant_id,inspection_id,video_plan_id) WHERE video_plan_id IS NOT NULL;
GRANT SELECT, UPDATE ON inspections.inspections TO inspection_runtime;
GRANT SELECT, INSERT ON analysis.classification_runs TO inspection_runtime;
`},
		{Version: 70, Name: "version_video_preparations_by_profile", Compatible: true, SQL: `
ALTER TABLE media.video_preparation_jobs DROP CONSTRAINT IF EXISTS video_preparation_jobs_tenant_id_media_id_key;
DROP INDEX IF EXISTS media.idx_video_job_media;
CREATE UNIQUE INDEX IF NOT EXISTS uq_video_preparation_profile_generation
  ON media.video_preparation_jobs(tenant_id,media_id,profile_digest,generation);
ALTER TABLE media.video_preparation_jobs ADD COLUMN IF NOT EXISTS client_mutation_id varchar(128) NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS uq_video_preparation_mutation
  ON media.video_preparation_jobs(tenant_id,client_mutation_id) WHERE client_mutation_id <> '';
CREATE INDEX IF NOT EXISTS idx_video_preparation_current
  ON media.video_preparation_jobs(tenant_id,media_id,status,created_at DESC,id DESC);
GRANT SELECT, INSERT, UPDATE ON media.video_preparation_jobs TO inspection_runtime;
`},
		{Version: 71, Name: "idempotent_video_plan_reanalysis", Compatible: true, SQL: `
ALTER TABLE analysis.video_plans ADD COLUMN IF NOT EXISTS client_mutation_id varchar(128) NOT NULL DEFAULT '';
ALTER TABLE analysis.video_plans ADD COLUMN IF NOT EXISTS expected_plan_id uuid;
ALTER TABLE analysis.video_plans DROP CONSTRAINT IF EXISTS fk_video_plan_expected_plan;
ALTER TABLE analysis.video_plans ADD CONSTRAINT fk_video_plan_expected_plan FOREIGN KEY(tenant_id,expected_plan_id) REFERENCES analysis.video_plans(tenant_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_video_plan_reanalysis_mutation
  ON analysis.video_plans(tenant_id,client_mutation_id) WHERE client_mutation_id <> '';
GRANT SELECT, INSERT, UPDATE ON analysis.video_plans TO inspection_runtime;
`},
		{Version: 72, Name: "video_plan_preparations_and_carried_results", Compatible: true, SQL: `
ALTER TABLE analysis.video_plans ADD COLUMN IF NOT EXISTS parent_plan_id uuid;
ALTER TABLE analysis.video_plans DROP CONSTRAINT IF EXISTS fk_video_plan_parent;
ALTER TABLE analysis.video_plans ADD CONSTRAINT fk_video_plan_parent FOREIGN KEY(tenant_id,parent_plan_id) REFERENCES analysis.video_plans(tenant_id,id);
ALTER TABLE analysis.video_plan_items ADD COLUMN IF NOT EXISTS carried_result_job_id uuid;
CREATE UNIQUE INDEX IF NOT EXISTS uq_comparison_job_tenant_id ON analysis.comparison_jobs(tenant_id,id);
ALTER TABLE analysis.video_plan_items DROP CONSTRAINT IF EXISTS fk_video_plan_item_carried_result;
ALTER TABLE analysis.video_plan_items ADD CONSTRAINT fk_video_plan_item_carried_result FOREIGN KEY(tenant_id,carried_result_job_id) REFERENCES analysis.comparison_jobs(tenant_id,id);
CREATE TABLE IF NOT EXISTS analysis.video_plan_preparations (
  tenant_id uuid NOT NULL, plan_id uuid NOT NULL, preparation_id uuid NOT NULL,
  role varchar(16) NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(tenant_id,plan_id,preparation_id),
  FOREIGN KEY(tenant_id,plan_id) REFERENCES analysis.video_plans(tenant_id,id),
  FOREIGN KEY(tenant_id,preparation_id) REFERENCES media.video_preparation_jobs(tenant_id,id),
  CHECK(role IN ('MAIN','SUPPLEMENT','ORIGIN'))
);
CREATE INDEX IF NOT EXISTS idx_video_plan_preparations_preparation ON analysis.video_plan_preparations(tenant_id,preparation_id);
ALTER TABLE analysis.video_plan_preparations ENABLE ROW LEVEL SECURITY;
ALTER TABLE analysis.video_plan_preparations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON analysis.video_plan_preparations USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON analysis.video_plan_preparations TO inspection_runtime;
GRANT SELECT, UPDATE ON analysis.video_plan_items TO inspection_runtime;
GRANT SELECT, INSERT, UPDATE ON analysis.video_plans TO inspection_runtime;
`},
		{Version: 73, Name: "video_recapture_coverage_jobs", Compatible: true, SQL: `
CREATE UNIQUE INDEX IF NOT EXISTS uq_recapture_request_tenant_id ON recapture.requests(tenant_id,id);
CREATE TABLE IF NOT EXISTS recapture.video_coverage_jobs (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, preparation_id uuid NOT NULL,
  request_id uuid NOT NULL, status varchar(16) NOT NULL DEFAULT 'QUEUED',
  attempts integer NOT NULL DEFAULT 0, next_attempt_at timestamptz NOT NULL,
  lease_token uuid, lease_until timestamptz, error_code varchar(64) NOT NULL DEFAULT '',
  covered_ids jsonb NOT NULL DEFAULT '[]', created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(tenant_id, preparation_id),
  FOREIGN KEY(tenant_id, preparation_id) REFERENCES media.video_preparation_jobs(tenant_id,id),
  FOREIGN KEY(tenant_id, request_id) REFERENCES recapture.requests(tenant_id,id),
  CHECK(status IN ('QUEUED','PROCESSING','COMPLETED','FAILED'))
);
CREATE INDEX IF NOT EXISTS idx_video_coverage_due ON recapture.video_coverage_jobs(status,next_attempt_at,lease_until);
ALTER TABLE recapture.video_coverage_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE recapture.video_coverage_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON recapture.video_coverage_jobs USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE ON recapture.video_coverage_jobs TO inspection_runtime;
`},
		{Version: 74, Name: "scope_video_plan_frame_identity_index", Compatible: true, SQL: `
DROP INDEX IF EXISTS analysis.idx_video_plan_frame_tenant_id;
CREATE UNIQUE INDEX idx_video_plan_frame_tenant_id ON analysis.video_plan_item_frames(tenant_id,id);
DROP INDEX IF EXISTS media.idx_video_evidence_reference_id;
CREATE UNIQUE INDEX idx_video_evidence_reference_id ON media.video_evidence_references(tenant_id,id);
`},
		{Version: 75, Name: "recapture_unresolved_requirements", Compatible: true, SQL: `
ALTER TABLE recapture.request_requirements DROP CONSTRAINT IF EXISTS chk_recapture_requirement_status;
ALTER TABLE recapture.request_requirements ADD CONSTRAINT chk_recapture_requirement_status CHECK (status IN ('OPEN','CORRECTED','EXPIRED','UNRESOLVED','CANCELED'));
`},
		{Version: 76, Name: "retention_video_purge_delete_grants", Compatible: true, SQL: `
GRANT DELETE ON analysis.video_plan_preparations, analysis.video_plan_items, analysis.video_plan_item_frames, analysis.video_plans TO inspection_runtime;
GRANT DELETE ON media.video_preparation_jobs, media.video_scope_items, media.video_frames TO inspection_runtime;
`},
		{Version: 77, Name: "video_membership_tenant_foreign_keys", Compatible: true, SQL: `
-- Tables first created by model synchronization skipped the inline composite
-- keys of their CREATE TABLE IF NOT EXISTS. NOT VALID enforces every new write
-- without failing on historical rows of existing environments.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='fk_video_plan_item_plan' AND conrelid='analysis.video_plan_items'::regclass) THEN
    ALTER TABLE analysis.video_plan_items ADD CONSTRAINT fk_video_plan_item_plan FOREIGN KEY(tenant_id,plan_id) REFERENCES analysis.video_plans(tenant_id,id) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='fk_video_plan_item_frame_item' AND conrelid='analysis.video_plan_item_frames'::regclass) THEN
    ALTER TABLE analysis.video_plan_item_frames ADD CONSTRAINT fk_video_plan_item_frame_item FOREIGN KEY(tenant_id,plan_item_id) REFERENCES analysis.video_plan_items(tenant_id,id) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='fk_video_plan_item_frame_frame' AND conrelid='analysis.video_plan_item_frames'::regclass) THEN
    ALTER TABLE analysis.video_plan_item_frames ADD CONSTRAINT fk_video_plan_item_frame_frame FOREIGN KEY(tenant_id,frame_id) REFERENCES media.video_frames(tenant_id,id) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='fk_video_scope_preparation' AND conrelid='media.video_scope_items'::regclass) THEN
    ALTER TABLE media.video_scope_items ADD CONSTRAINT fk_video_scope_preparation FOREIGN KEY(tenant_id,preparation_id) REFERENCES media.video_preparation_jobs(tenant_id,id) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='fk_video_scope_frame' AND conrelid='media.video_scope_items'::regclass) THEN
    ALTER TABLE media.video_scope_items ADD CONSTRAINT fk_video_scope_frame FOREIGN KEY(tenant_id,frame_id) REFERENCES media.video_frames(tenant_id,id) NOT VALID;
  END IF;
END $$;
`},
		{Version: 78, Name: "sensitive_review_and_recoverable_video_plans", Compatible: true, SQL: `
ALTER TABLE inspections.inspections DROP CONSTRAINT IF EXISTS chk_inspection_status;
ALTER TABLE inspections.inspections ADD CONSTRAINT chk_inspection_status CHECK (status IN ('PLANNED','INVITED','IN_PROGRESS','SUBMITTED','ANALYZING','RECAPTURE_PENDING','REVIEW_REQUIRED','PROCESSING_FAILED','REPLACEMENT_PENDING','COMPLETED','CANCELED','INVALIDATED'));
ALTER TABLE analysis.video_plans DROP CONSTRAINT IF EXISTS chk_video_plan_status;
ALTER TABLE analysis.video_plans ADD CONSTRAINT chk_video_plan_status CHECK (status IN ('BUILDING','SEALED','FAILED','BLOCKED'));
CREATE TABLE IF NOT EXISTS platform.worker_runtime_settings (
  worker varchar(96) PRIMARY KEY,
  screening_bypassed boolean NOT NULL,
  source_variable varchar(96) NOT NULL DEFAULT '',
  reported_at timestamptz NOT NULL DEFAULT now()
);
GRANT USAGE ON SCHEMA platform TO inspection_runtime;
GRANT SELECT, INSERT, UPDATE ON platform.worker_runtime_settings TO inspection_runtime;

WITH terminal_preparations AS (
  SELECT p.tenant_id, p.id, p.inspection_id,
         CASE WHEN coalesce(current_p.error_code, '')='SENSITIVE_CONTENT' OR coalesce(origin_p.error_code, '')='SENSITIVE_CONTENT'
              THEN 'SENSITIVE_CONTENT' ELSE 'PREPARATION_FAILED' END AS failure_code
  FROM analysis.video_plans p
  LEFT JOIN media.video_preparation_jobs current_p ON current_p.tenant_id=p.tenant_id AND current_p.id=p.current_preparation_id
  LEFT JOIN media.video_preparation_jobs origin_p ON origin_p.tenant_id=p.tenant_id AND origin_p.id=p.origin_preparation_id
  WHERE p.status='BUILDING' AND (current_p.status IN ('FAILED','CANCELED') OR origin_p.status IN ('FAILED','CANCELED'))
)
UPDATE analysis.video_plans p
SET status='BLOCKED', error_code=t.failure_code, lease_token=NULL, lease_until=NULL, next_attempt_at=now()
FROM terminal_preparations t
WHERE p.tenant_id=t.tenant_id AND p.id=t.id;

UPDATE inspections.inspections i
SET status=CASE WHEN p.error_code='SENSITIVE_CONTENT' THEN 'REVIEW_REQUIRED' ELSE 'PROCESSING_FAILED' END,
    updated_at=now(), version=i.version + 1
FROM analysis.video_plans p
WHERE p.tenant_id=i.tenant_id AND p.inspection_id=i.id AND p.status='BLOCKED' AND i.status='ANALYZING';

CREATE TABLE IF NOT EXISTS inspections.video_replacements (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL,
  inspection_id uuid NOT NULL,
  media_id uuid NOT NULL,
  responsibility_id uuid NOT NULL,
  invitation_id uuid NOT NULL,
  deadline_at timestamptz NOT NULL,
  reason varchar(2000) NOT NULL,
  status varchar(20) NOT NULL,
  idempotency_key varchar(128) NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uq_video_replacement_idempotency UNIQUE (tenant_id,idempotency_key),
  CONSTRAINT chk_video_replacement_status CHECK (status IN ('REQUESTED','SUBMITTED','EXPIRED','CANCELED'))
);
CREATE INDEX IF NOT EXISTS idx_video_replacement_deadline ON inspections.video_replacements(status,deadline_at);
CREATE INDEX IF NOT EXISTS idx_video_replacement_inspection ON inspections.video_replacements(tenant_id,inspection_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_video_replacement_active ON inspections.video_replacements(tenant_id,inspection_id) WHERE status='REQUESTED';
ALTER TABLE inspections.video_replacements ENABLE ROW LEVEL SECURITY;
ALTER TABLE inspections.video_replacements FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON inspections.video_replacements USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON inspections.video_replacements TO inspection_runtime;

ALTER TABLE capture.capture_drafts DROP CONSTRAINT IF EXISTS chk_capture_draft_kind;
ALTER TABLE capture.capture_drafts ADD CONSTRAINT chk_capture_draft_kind CHECK (kind IN ('ORIGIN','INSPECTION','RECAPTURE','VIDEO_REPLACEMENT'));

CREATE OR REPLACE FUNCTION platform.discover_scheduler_tenants(
  p_now timestamptz,
  p_terminal_updated_before timestamptz,
  p_deadline_before timestamptz
) RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog
AS $$
  SELECT s.tenant_id FROM schedules.schedules s
  WHERE s.status='ACTIVE' AND s.next_due_at<=p_now
  UNION
  SELECT r.tenant_id FROM schedules.reminder_plans r
  WHERE r.status='PLANNED' AND r.remind_at<=p_now
  UNION
  SELECT i.tenant_id FROM inspections.inspections i
  WHERE i.status IN ('COMPLETED','CANCELED','INVALIDATED') AND i.updated_at<=p_terminal_updated_before
  UNION
  SELECT i.tenant_id FROM inspections.inspections i
  WHERE i.status IN ('PLANNED','INVITED','IN_PROGRESS','SUBMITTED','ANALYZING') AND i.deadline_at<=p_deadline_before
$$;
REVOKE ALL ON FUNCTION platform.discover_scheduler_tenants(timestamptz, timestamptz, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.discover_scheduler_tenants(timestamptz, timestamptz, timestamptz) TO inspection_runtime;
`},
		{Version: 79, Name: "inventory_baseline_versions", Compatible: true, SQL: `
ALTER TABLE origins.origin_versions ALTER COLUMN responsibility_id DROP NOT NULL;
ALTER TABLE origins.origin_versions ADD COLUMN IF NOT EXISTS purpose text;
ALTER TABLE origins.origin_versions ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT 'CAPTURE';
ALTER TABLE origins.origin_versions ADD COLUMN IF NOT EXISTS source_inspection_id uuid;
ALTER TABLE origins.origin_versions ADD COLUMN IF NOT EXISTS source_version_id uuid;
ALTER TABLE origins.origin_versions ADD COLUMN IF NOT EXISTS has_inventory boolean NOT NULL DEFAULT false;
ALTER TABLE origins.origin_versions ADD COLUMN IF NOT EXISTS draft_state text;
ALTER TABLE origins.origin_versions ADD COLUMN IF NOT EXISTS activated_by_membership_id uuid;
ALTER TABLE origins.origin_versions ADD COLUMN IF NOT EXISTS discarded_at timestamptz;
ALTER TABLE origins.origin_versions DROP CONSTRAINT IF EXISTS chk_origin_version_status;
ALTER TABLE origins.origin_versions ADD CONSTRAINT chk_origin_version_status CHECK (status IN ('DRAFT','SUBMITTED','ACTIVE','SUPERSEDED','INVALIDATED','DISCARDED'));
ALTER TABLE origins.origin_versions ADD CONSTRAINT chk_origin_version_purpose CHECK (purpose IS NULL OR purpose IN ('MOVE_IN','CONSTRUCTION_HANDOVER','POST_RENOVATION','INVENTORY_UPDATE','CONTESTATION_ADJUSTMENT'));
ALTER TABLE origins.origin_versions ADD CONSTRAINT chk_origin_version_source CHECK (source IN ('CAPTURE','LEGACY_ORIGIN','COMPARATIVE_INSPECTION','CONTESTATION'));
ALTER TABLE origins.origin_versions ADD CONSTRAINT chk_origin_version_draft_state CHECK (draft_state IS NULL OR draft_state IN ('PREPARING','READY','FAILED'));
ALTER TABLE origins.origin_versions ADD CONSTRAINT chk_origin_inventory_purpose CHECK (NOT (has_inventory AND status IN ('ACTIVE','SUPERSEDED')) OR purpose IS NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS uq_origin_version_tenant_id ON origins.origin_versions(tenant_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_membership_tenant_id ON access.memberships(tenant_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_inspection_tenant_id ON inspections.inspections(tenant_id,id);
ALTER TABLE origins.origin_versions ADD CONSTRAINT fk_origin_version_source_inspection FOREIGN KEY(tenant_id,source_inspection_id) REFERENCES inspections.inspections(tenant_id,id);
ALTER TABLE origins.origin_versions ADD CONSTRAINT fk_origin_version_source_version FOREIGN KEY(tenant_id,source_version_id) REFERENCES origins.origin_versions(tenant_id,id);
ALTER TABLE origins.origin_versions ADD CONSTRAINT fk_origin_version_activator FOREIGN KEY(tenant_id,activated_by_membership_id) REFERENCES access.memberships(tenant_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_origin_inventory_draft ON origins.origin_versions(tenant_id,origin_id) WHERE has_inventory AND status='DRAFT';
`},
		{Version: 80, Name: "inventory_tables", Compatible: true, SQL: `
CREATE UNIQUE INDEX IF NOT EXISTS uq_inventory_environment_tenant_id ON origins.inventory_environments(tenant_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_inventory_item_tenant_id ON origins.inventory_items(tenant_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_inventory_evidence_tenant_id ON origins.inventory_item_evidence(tenant_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_inventory_decision_tenant_id ON origins.inventory_decisions(tenant_id,id);
ALTER TABLE origins.inventory_environments ADD CONSTRAINT fk_inventory_environment_version FOREIGN KEY(tenant_id,origin_version_id) REFERENCES origins.origin_versions(tenant_id,id);
ALTER TABLE origins.inventory_items ADD CONSTRAINT fk_inventory_item_version FOREIGN KEY(tenant_id,origin_version_id) REFERENCES origins.origin_versions(tenant_id,id);
ALTER TABLE origins.inventory_items ADD CONSTRAINT fk_inventory_item_environment FOREIGN KEY(tenant_id,environment_id) REFERENCES origins.inventory_environments(tenant_id,id);
ALTER TABLE origins.inventory_items ADD CONSTRAINT fk_inventory_item_merged_into FOREIGN KEY(tenant_id,merged_into_id) REFERENCES origins.inventory_items(tenant_id,id);
ALTER TABLE origins.inventory_items ADD CONSTRAINT fk_inventory_item_decider FOREIGN KEY(tenant_id,decided_by) REFERENCES access.memberships(tenant_id,id);
ALTER TABLE origins.inventory_item_evidence ADD CONSTRAINT fk_inventory_evidence_item FOREIGN KEY(tenant_id,item_id) REFERENCES origins.inventory_items(tenant_id,id);
ALTER TABLE origins.inventory_item_evidence ADD CONSTRAINT fk_inventory_evidence_media FOREIGN KEY(tenant_id,media_id) REFERENCES media.media_objects(tenant_id,id);
ALTER TABLE origins.inventory_item_evidence ADD CONSTRAINT fk_inventory_evidence_frame FOREIGN KEY(tenant_id,frame_id) REFERENCES media.video_frames(tenant_id,id);
ALTER TABLE origins.inventory_decisions ADD CONSTRAINT fk_inventory_decision_version FOREIGN KEY(tenant_id,origin_version_id) REFERENCES origins.origin_versions(tenant_id,id);
ALTER TABLE origins.inventory_decisions ADD CONSTRAINT fk_inventory_decision_actor FOREIGN KEY(tenant_id,actor_membership_id) REFERENCES access.memberships(tenant_id,id);
ALTER TABLE origins.inventory_environments ADD CONSTRAINT chk_inventory_environment_name CHECK(char_length(name) BETWEEN 1 AND 80);
ALTER TABLE origins.inventory_environments ADD CONSTRAINT chk_inventory_environment_decision CHECK(decision IN ('PENDING','ACCEPTED','EDITED','REJECTED','ADDED'));
ALTER TABLE origins.inventory_items ADD CONSTRAINT chk_inventory_item_name CHECK(char_length(name) BETWEEN 1 AND 80);
ALTER TABLE origins.inventory_items ADD CONSTRAINT chk_inventory_item_condition CHECK(condition IN ('NEW','GOOD','FAIR','POOR','NOT_ASSESSABLE'));
ALTER TABLE origins.inventory_items ADD CONSTRAINT chk_inventory_item_observation CHECK(observation IN ('OBSERVED','PARTIAL','NOT_OBSERVED','NOT_ASSESSABLE'));
ALTER TABLE origins.inventory_items ADD CONSTRAINT chk_inventory_item_quantity CHECK(quantity IS NULL OR quantity BETWEEN 1 AND 999);
ALTER TABLE origins.inventory_items ADD CONSTRAINT chk_inventory_item_confidence CHECK(confidence_bps IS NULL OR confidence_bps BETWEEN 0 AND 10000);
ALTER TABLE origins.inventory_items ADD CONSTRAINT chk_inventory_item_origin CHECK(origin IN ('AI','MANUAL'));
ALTER TABLE origins.inventory_items ADD CONSTRAINT chk_inventory_item_decision CHECK(decision IN ('PENDING','ACCEPTED','EDITED','REJECTED','ADDED'));
ALTER TABLE origins.inventory_items ADD CONSTRAINT chk_inventory_item_rejection_reason CHECK(rejection_reason IS NULL OR rejection_reason IN ('NOT_EXISTS','NOT_RELEVANT','DUPLICATE','PERSONAL_OBJECT','OTHER'));
ALTER TABLE origins.inventory_items ADD CONSTRAINT chk_inventory_item_rejected_reason CHECK(decision<>'REJECTED' OR rejection_reason IS NOT NULL);
ALTER TABLE origins.inventory_item_evidence ADD CONSTRAINT chk_inventory_evidence_kind CHECK(kind IN ('PHOTO','VIDEO_FRAME'));
ALTER TABLE origins.inventory_item_evidence ADD CONSTRAINT chk_inventory_evidence_frame CHECK((kind='VIDEO_FRAME') = (frame_id IS NOT NULL));
ALTER TABLE origins.inventory_decisions ADD CONSTRAINT chk_inventory_decision_target CHECK(target_type IN ('ENVIRONMENT','ITEM','VERSION'));
ALTER TABLE origins.inventory_decisions ADD CONSTRAINT chk_inventory_decision_action CHECK(action IN ('ACCEPT','EDIT','REJECT','ADD','MERGE','SPLIT','REMOVE_ENVIRONMENT','ACTIVATE','DISCARD'));
DO $$ DECLARE t text; BEGIN FOREACH t IN ARRAY ARRAY['origins.inventory_environments','origins.inventory_items','origins.inventory_item_evidence','origins.inventory_decisions'] LOOP
 EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY',t); EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY',t);
 EXECUTE format('CREATE POLICY tenant_isolation ON %s USING (tenant_id=nullif(current_setting(''app.tenant_id'',true),'''')::uuid) WITH CHECK (tenant_id=nullif(current_setting(''app.tenant_id'',true),'''')::uuid)',t);
END LOOP; END $$;
GRANT SELECT,INSERT,UPDATE,DELETE ON origins.inventory_environments,origins.inventory_items,origins.inventory_item_evidence TO inspection_runtime;
-- The decision trail is append-only for the runtime role.
REVOKE UPDATE,DELETE ON origins.inventory_decisions FROM inspection_runtime;
GRANT SELECT,INSERT ON origins.inventory_decisions TO inspection_runtime;
`},
		{Version: 81, Name: "analysis_prompt_tasks_and_inventory_jobs", Compatible: true, SQL: `
ALTER TABLE analysis.prompts ADD COLUMN IF NOT EXISTS task text NOT NULL DEFAULT 'FINDINGS';
ALTER TABLE analysis.prompt_snapshots ADD COLUMN IF NOT EXISTS task text NOT NULL DEFAULT 'FINDINGS';
DROP INDEX IF EXISTS analysis.idx_analysis_prompts_analysis_type;
DROP INDEX IF EXISTS analysis.analysis_prompts_analysis_type_key;
DROP INDEX IF EXISTS analysis.idx_analysis_prompt_snapshot_digest;
CREATE UNIQUE INDEX IF NOT EXISTS uq_analysis_prompt_task ON analysis.prompts(analysis_type,task);
CREATE UNIQUE INDEX IF NOT EXISTS uq_analysis_prompt_snapshot_task_digest ON analysis.prompt_snapshots(analysis_type,task,canonical_digest);
ALTER TABLE analysis.prompts ADD CONSTRAINT chk_analysis_prompt_task CHECK(task IN ('FINDINGS','INVENTORY_CATALOG','INVENTORY_COMPARE'));
ALTER TABLE analysis.prompt_snapshots ADD CONSTRAINT chk_analysis_snapshot_task CHECK(task IN ('FINDINGS','INVENTORY_CATALOG','INVENTORY_COMPARE'));
ALTER TABLE inspections.inspections ADD COLUMN IF NOT EXISTS inventory_prompt_snapshot_id uuid;
ALTER TABLE inspections.inspections ADD CONSTRAINT fk_inspection_inventory_prompt_snapshot FOREIGN KEY(inventory_prompt_snapshot_id) REFERENCES analysis.prompt_snapshots(id);

CREATE UNIQUE INDEX IF NOT EXISTS uq_comparison_job_tenant_id ON analysis.comparison_jobs(tenant_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_inventory_job_tenant_id ON analysis.inventory_jobs(tenant_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_inventory_pair_tenant_id ON analysis.inventory_evidence_pairs(tenant_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_inventory_result_tenant_id ON analysis.inventory_item_results(tenant_id,id);
ALTER TABLE analysis.inventory_jobs ADD CONSTRAINT fk_inventory_job_inspection FOREIGN KEY(tenant_id,inspection_id) REFERENCES inspections.inspections(tenant_id,id);
ALTER TABLE analysis.inventory_jobs ADD CONSTRAINT fk_inventory_job_origin_version FOREIGN KEY(tenant_id,origin_version_id) REFERENCES origins.origin_versions(tenant_id,id);
ALTER TABLE analysis.inventory_jobs ADD CONSTRAINT fk_inventory_job_prompt_snapshot FOREIGN KEY(prompt_snapshot_id) REFERENCES analysis.prompt_snapshots(id);
ALTER TABLE analysis.inventory_jobs ADD CONSTRAINT chk_inventory_job_kind CHECK(kind IN ('CATALOG','COMPARE'));
ALTER TABLE analysis.inventory_jobs ADD CONSTRAINT chk_inventory_job_status CHECK(status IN ('PENDING','RUNNING','SUCCEEDED','FAILED','CANCELED'));
ALTER TABLE analysis.inventory_jobs ADD CONSTRAINT chk_inventory_job_attempts CHECK(attempts >= 0);
ALTER TABLE analysis.inventory_evidence_pairs ADD CONSTRAINT fk_inventory_pair_job FOREIGN KEY(tenant_id,job_id) REFERENCES analysis.inventory_jobs(tenant_id,id);
ALTER TABLE analysis.inventory_evidence_pairs ADD CONSTRAINT fk_inventory_pair_origin_media FOREIGN KEY(tenant_id,origin_media_id) REFERENCES media.media_objects(tenant_id,id);
ALTER TABLE analysis.inventory_evidence_pairs ADD CONSTRAINT fk_inventory_pair_current_media FOREIGN KEY(tenant_id,current_media_id) REFERENCES media.media_objects(tenant_id,id);
ALTER TABLE analysis.inventory_evidence_pairs ADD CONSTRAINT fk_inventory_pair_origin_frame FOREIGN KEY(tenant_id,origin_frame_id) REFERENCES media.video_frames(tenant_id,id);
ALTER TABLE analysis.inventory_evidence_pairs ADD CONSTRAINT fk_inventory_pair_current_frame FOREIGN KEY(tenant_id,current_frame_id) REFERENCES media.video_frames(tenant_id,id);
ALTER TABLE analysis.inventory_evidence_pairs ADD CONSTRAINT chk_inventory_pair_decision CHECK(decision IN ('SAME_VIEW','NOT_COMPARABLE','UNCERTAIN'));
ALTER TABLE analysis.inventory_evidence_pairs ADD CONSTRAINT chk_inventory_pair_origin_frame CHECK(origin_kind IN ('PHOTO','VIDEO_FRAME') AND (origin_kind='VIDEO_FRAME') = (origin_frame_id IS NOT NULL));
ALTER TABLE analysis.inventory_evidence_pairs ADD CONSTRAINT chk_inventory_pair_current_frame CHECK(current_kind IN ('PHOTO','VIDEO_FRAME') AND (current_kind='VIDEO_FRAME') = (current_frame_id IS NOT NULL));
ALTER TABLE analysis.inventory_evidence_pairs ADD CONSTRAINT chk_inventory_pair_confirmed CHECK(NOT confirmed OR decision='SAME_VIEW');
-- Photo evidence has no frame id; NULLS NOT DISTINCT keeps one pair per photo.
DROP INDEX IF EXISTS analysis.uq_inventory_pair_identity;
CREATE UNIQUE INDEX uq_inventory_pair_identity ON analysis.inventory_evidence_pairs(tenant_id,job_id,item_identity_id,origin_media_id,origin_frame_id) NULLS NOT DISTINCT;
ALTER TABLE analysis.inventory_item_results ADD CONSTRAINT fk_inventory_result_job FOREIGN KEY(tenant_id,job_id) REFERENCES analysis.inventory_jobs(tenant_id,id);
ALTER TABLE analysis.inventory_item_results ADD CONSTRAINT fk_inventory_result_inspection FOREIGN KEY(tenant_id,inspection_id) REFERENCES inspections.inspections(tenant_id,id);
ALTER TABLE analysis.inventory_item_results ADD CONSTRAINT chk_inventory_result_status CHECK(status IN ('PRESENT_UNCHANGED','CONDITION_CHANGED','MISSING','NOT_OBSERVED','OUTSIDE_INVENTORY','IMPROVED'));
ALTER TABLE analysis.inventory_item_results ADD CONSTRAINT chk_inventory_result_conditions CHECK((condition_before IS NULL OR condition_before IN ('NEW','GOOD','FAIR','POOR','NOT_ASSESSABLE')) AND (condition_after IS NULL OR condition_after IN ('NEW','GOOD','FAIR','POOR','NOT_ASSESSABLE')));
ALTER TABLE analysis.inventory_item_results ADD CONSTRAINT chk_inventory_result_reason CHECK(reason_code IS NULL OR reason_code IN ('OBSTRUCTED','FRAMING','QUALITY','NO_CORRESPONDING_VIEW','ENVIRONMENT_NOT_CAPTURED','ANALYSIS_FAILED'));
-- Outside-inventory rows describe a new object instead of a baseline item.
ALTER TABLE analysis.inventory_item_results ALTER COLUMN item_identity_id DROP NOT NULL;
ALTER TABLE analysis.inventory_item_results ADD CONSTRAINT chk_inventory_result_outside_identity CHECK((status='OUTSIDE_INVENTORY') = (item_identity_id IS NULL));
ALTER TABLE analysis.inventory_item_results ADD CONSTRAINT chk_inventory_result_outside_details CHECK((status='OUTSIDE_INVENTORY' AND outside_type_code IS NOT NULL AND char_length(outside_type_code)>0 AND outside_name IS NOT NULL AND char_length(outside_name) BETWEEN 1 AND 80) OR (status<>'OUTSIDE_INVENTORY' AND outside_type_code IS NULL AND outside_name IS NULL));
DO $$ DECLARE t text; BEGIN FOREACH t IN ARRAY ARRAY['analysis.inventory_jobs','analysis.inventory_evidence_pairs','analysis.inventory_item_results'] LOOP
 EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY',t); EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY',t);
 EXECUTE format('CREATE POLICY tenant_isolation ON %s USING (tenant_id=nullif(current_setting(''app.tenant_id'',true),'''')::uuid) WITH CHECK (tenant_id=nullif(current_setting(''app.tenant_id'',true),'''')::uuid)',t);
END LOOP; END $$;
GRANT SELECT,INSERT,UPDATE,DELETE ON analysis.inventory_jobs,analysis.inventory_evidence_pairs,analysis.inventory_item_results TO inspection_runtime;

-- Findings reference exactly one job. Existing findings are attributed to the
-- comparison job of their analysis run; the immutability trigger is paused
-- only for this backfill inside the migration transaction.
ALTER TABLE analysis.findings ADD COLUMN IF NOT EXISTS comparison_job_id uuid;
ALTER TABLE analysis.findings ADD COLUMN IF NOT EXISTS inventory_job_id uuid;
ALTER TABLE analysis.findings ADD COLUMN IF NOT EXISTS item_identity_id uuid;
ALTER TABLE analysis.findings DISABLE TRIGGER immutable_finding;
UPDATE analysis.findings f SET comparison_job_id=r.job_id FROM analysis.analysis_runs r WHERE f.tenant_id=r.tenant_id AND f.analysis_run_id=r.id AND f.comparison_job_id IS NULL AND f.inventory_job_id IS NULL;
ALTER TABLE analysis.findings ENABLE TRIGGER immutable_finding;
ALTER TABLE analysis.findings ADD CONSTRAINT fk_finding_comparison_job FOREIGN KEY(tenant_id,comparison_job_id) REFERENCES analysis.comparison_jobs(tenant_id,id);
ALTER TABLE analysis.findings ADD CONSTRAINT fk_finding_inventory_job FOREIGN KEY(tenant_id,inventory_job_id) REFERENCES analysis.inventory_jobs(tenant_id,id);
ALTER TABLE analysis.findings ADD CONSTRAINT chk_finding_one_job CHECK((comparison_job_id IS NULL) <> (inventory_job_id IS NULL));
ALTER TABLE analysis.findings ADD CONSTRAINT chk_finding_item_identity CHECK(item_identity_id IS NULL OR inventory_job_id IS NOT NULL);

ALTER TABLE usage.llm_calls DROP CONSTRAINT IF EXISTS chk_llm_call_comparison_mode;
ALTER TABLE usage.llm_calls ADD CONSTRAINT chk_llm_call_comparison_mode CHECK(comparison_mode IS NULL OR comparison_mode IN ('CURRENT_ONLY','COMPARE_ORIGIN_CURRENT','CHECKLIST_ONLY','FIXED_ORIGIN','PLANNED_STAGE','PREVIOUS_INSPECTION','BEFORE_AFTER','VIDEO_SCOPE','VIDEO_MATCH','VIDEO_CONDITION','VIDEO_VERIFY','INVENTORY_CATALOG','INVENTORY_MATCH','INVENTORY_COMPARE'));
`},
		{Version: 82, Name: "environment_capture_contracts", Compatible: true, SQL: `
ALTER TABLE templates.template_versions DROP CONSTRAINT IF EXISTS chk_template_schema_version;
ALTER TABLE templates.template_versions ADD CONSTRAINT chk_template_schema_version CHECK(schema_version IN (1,2));
ALTER TABLE invitations.invitations ADD COLUMN IF NOT EXISTS reviewer_membership_id uuid;
ALTER TABLE invitations.invitations ADD CONSTRAINT fk_invitation_reviewer_membership FOREIGN KEY(tenant_id,reviewer_membership_id) REFERENCES access.memberships(tenant_id,id);
ALTER TABLE media.video_preparation_jobs ADD COLUMN IF NOT EXISTS frames_sampled integer NOT NULL DEFAULT 0;
ALTER TABLE media.video_preparation_jobs ADD COLUMN IF NOT EXISTS frames_retained integer NOT NULL DEFAULT 0;
ALTER TABLE media.video_preparation_jobs ADD COLUMN IF NOT EXISTS frames_representative integer NOT NULL DEFAULT 0;
ALTER TABLE media.video_preparation_jobs ADD COLUMN IF NOT EXISTS requirement_key text;
ALTER TABLE media.video_preparation_jobs ADD CONSTRAINT chk_video_preparation_frame_metrics CHECK(frames_sampled >= 0 AND frames_retained >= 0 AND frames_representative >= 0 AND frames_retained <= frames_sampled AND frames_representative <= frames_retained);
ALTER TABLE media.video_frames ADD COLUMN IF NOT EXISTS representative boolean NOT NULL DEFAULT false;
ALTER TABLE analysis.video_plan_preparations ADD COLUMN IF NOT EXISTS requirement_key text;
ALTER TABLE capture.video_attempts ADD COLUMN IF NOT EXISTS requirement_key text;
`},
		{Version: 83, Name: "counterparty_invitations_and_sessions", Compatible: true, SQL: `
CREATE UNIQUE INDEX IF NOT EXISTS uq_counterparty_tenant_id ON origins.counterparty_invitations(tenant_id,id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_participant_tenant_id ON participants.participants(tenant_id,id);
ALTER TABLE origins.counterparty_invitations ADD CONSTRAINT fk_counterparty_origin_version FOREIGN KEY(tenant_id,origin_version_id) REFERENCES origins.origin_versions(tenant_id,id);
ALTER TABLE origins.counterparty_invitations ADD CONSTRAINT fk_counterparty_participant FOREIGN KEY(tenant_id,participant_id) REFERENCES participants.participants(tenant_id,id);
ALTER TABLE origins.counterparty_invitations ADD CONSTRAINT fk_counterparty_adjustment_version FOREIGN KEY(tenant_id,adjustment_version_id) REFERENCES origins.origin_versions(tenant_id,id);
ALTER TABLE origins.counterparty_invitations ADD CONSTRAINT fk_counterparty_resolver FOREIGN KEY(tenant_id,resolved_by) REFERENCES access.memberships(tenant_id,id);
ALTER TABLE origins.counterparty_invitations ADD CONSTRAINT chk_counterparty_response CHECK(response IN ('PENDING','ACKNOWLEDGED','CONTESTED','NO_RESPONSE'));
ALTER TABLE origins.counterparty_invitations ADD CONSTRAINT chk_counterparty_resolution CHECK(resolution IN ('OPEN','DECLINED','ACCEPTED'));
ALTER TABLE origins.counterparty_invitations ADD CONSTRAINT chk_counterparty_statement CHECK(statement IS NULL OR char_length(statement)<=4000);
ALTER TABLE origins.counterparty_invitations ADD CONSTRAINT chk_counterparty_otp_attempts CHECK(otp_attempts >= 0);
ALTER TABLE origins.baseline_policies ADD CONSTRAINT fk_baseline_policy_tenant FOREIGN KEY(tenant_id) REFERENCES tenancy.tenants(tenant_id);
ALTER TABLE origins.baseline_policies ADD CONSTRAINT chk_baseline_deadline CHECK(contestation_deadline_days BETWEEN 1 AND 90);
ALTER TABLE origins.counterparty_invitations ENABLE ROW LEVEL SECURITY; ALTER TABLE origins.counterparty_invitations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON origins.counterparty_invitations USING(tenant_id=nullif(current_setting('app.tenant_id',true),'')::uuid) WITH CHECK(tenant_id=nullif(current_setting('app.tenant_id',true),'')::uuid);
ALTER TABLE origins.baseline_policies ENABLE ROW LEVEL SECURITY; ALTER TABLE origins.baseline_policies FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON origins.baseline_policies USING(tenant_id=nullif(current_setting('app.tenant_id',true),'')::uuid) WITH CHECK(tenant_id=nullif(current_setting('app.tenant_id',true),'')::uuid);
GRANT SELECT,INSERT,UPDATE,DELETE ON origins.counterparty_invitations,origins.baseline_policies TO inspection_runtime;

-- A capture session belongs to an invitation and responsibility; a
-- counterparty session belongs only to its counterparty invitation.
ALTER TABLE invitations.external_sessions ALTER COLUMN invitation_id DROP NOT NULL;
ALTER TABLE invitations.external_sessions ALTER COLUMN responsibility_id DROP NOT NULL;
ALTER TABLE invitations.external_sessions ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'CAPTURE';
ALTER TABLE invitations.external_sessions ADD COLUMN IF NOT EXISTS counterparty_invitation_id uuid;
ALTER TABLE invitations.external_sessions ADD CONSTRAINT chk_external_session_kind CHECK(kind IN ('CAPTURE','COUNTERPARTY'));
ALTER TABLE invitations.external_sessions ADD CONSTRAINT chk_external_session_owner CHECK((kind='CAPTURE' AND invitation_id IS NOT NULL AND responsibility_id IS NOT NULL AND counterparty_invitation_id IS NULL) OR (kind='COUNTERPARTY' AND invitation_id IS NULL AND responsibility_id IS NULL AND counterparty_invitation_id IS NOT NULL));
ALTER TABLE invitations.external_sessions ADD CONSTRAINT fk_external_counterparty_invitation FOREIGN KEY(tenant_id,counterparty_invitation_id) REFERENCES origins.counterparty_invitations(tenant_id,id);
`},
		{Version: 84, Name: "baseline_video_retention", Compatible: true, SQL: `
ALTER TABLE media.video_sources ADD COLUMN IF NOT EXISTS retention_anchor text NOT NULL DEFAULT 'FIXED';
ALTER TABLE media.video_sources ALTER COLUMN source_expiry_at DROP NOT NULL;
ALTER TABLE media.video_sources ADD CONSTRAINT chk_video_source_retention_anchor CHECK(retention_anchor IN ('FIXED','ACTIVE_BASELINE'));
-- Retained sources whose frame bank is ready get the fixed expiry they would
-- have received at FRAME_BANK_READY; sources still preparing receive it then.
UPDATE media.video_sources
SET retention_days=COALESCE(retention_days,30),
    source_expiry_at=first_frame_ready_at + (COALESCE(retention_days,30) * interval '1 day')
WHERE status='RETAINED' AND retention_anchor='FIXED'
  AND first_frame_ready_at IS NOT NULL AND source_expiry_at IS NULL;
ALTER TABLE media.video_sources ADD CONSTRAINT chk_video_source_retention_expiry CHECK(status<>'RETAINED' OR (retention_anchor='FIXED' AND (source_expiry_at IS NOT NULL OR first_frame_ready_at IS NULL)) OR (retention_anchor='ACTIVE_BASELINE' AND source_expiry_at IS NULL));
ALTER TABLE media.video_policies ADD COLUMN IF NOT EXISTS superseded_baseline_retention_days integer NOT NULL DEFAULT 365;
ALTER TABLE media.video_policies ALTER COLUMN current_retention_days SET DEFAULT 30;
ALTER TABLE media.video_policies ADD CONSTRAINT chk_video_policy_superseded_retention CHECK(superseded_baseline_retention_days BETWEEN 1 AND 3650);
`},
		{Version: 85, Name: "report_snapshot_schema_v2", Compatible: true, SQL: `
ALTER TABLE reports.report_snapshots ADD COLUMN IF NOT EXISTS schema_version integer NOT NULL DEFAULT 1;
ALTER TABLE reports.report_snapshots ADD CONSTRAINT chk_report_snapshot_schema_version CHECK(schema_version IN (1,2));
`},
		// Re-analysis appends a new generation of inventory jobs; readers use the
		// latest generation per requirement while earlier results stay auditable.
		{Version: 86, Name: "inventory_job_generations", Compatible: true, SQL: `
ALTER TABLE analysis.inventory_jobs ADD COLUMN IF NOT EXISTS generation integer NOT NULL DEFAULT 1;
ALTER TABLE analysis.inventory_jobs DROP CONSTRAINT IF EXISTS chk_inventory_job_generation;
ALTER TABLE analysis.inventory_jobs ADD CONSTRAINT chk_inventory_job_generation CHECK(generation >= 1);
DROP INDEX IF EXISTS analysis.uq_inventory_job_input;
CREATE UNIQUE INDEX uq_inventory_job_input ON analysis.inventory_jobs(tenant_id,inspection_id,kind,requirement_key,generation);
`},
	}
}

// LatestVersion returns the highest schema version known by the application.
func LatestVersion() int {
	latest := 0
	for _, step := range Foundation() {
		if step.Version > latest {
			latest = step.Version
		}
	}
	return latest
}
