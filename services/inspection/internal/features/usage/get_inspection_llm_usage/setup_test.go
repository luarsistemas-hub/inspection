package get_inspection_llm_usage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestInspectionLLMUsageAggregatesDeliveredKnownDataAndPaginates(t *testing.T) {
	db := usageQueryDB(t)
	bus := mediator.New()
	if err := Setup(Dependencies{DB: db, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	tenantID, inspectionID := identity.NewID(), identity.NewID()
	if err := db.Create(&database.Inspection{
		ID: inspectionID, TenantID: tenantID, BusinessUnitID: identity.NewID(), AssetID: identity.NewID(),
		ParticipantID: identity.NewID(), TemplateID: identity.NewID(), TemplateVersionID: identity.NewID(),
		AnalysisPromptSnapshotID: identity.NewID(), Source: "TEST", SourceKey: identity.NewID().String(), Status: "COMPLETED",
		ReminderInstants: json.RawMessage("[]"), ContextSnapshot: json.RawMessage("{}"), CreatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	inputOne, outputOne, costOne := int64(10), int64(5), 0.25
	inputTwo := int64(20)
	rows := []database.LLMCallRecord{
		{CallID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, JobID: identity.NewID(), EventID: identity.NewID(), ExecutionID: identity.NewID(), CorrelationID: "corr-1", Attempt: 1, Mode: "live", ComparisonMode: "CURRENT_ONLY", ModelAlias: "inspection-vision", PromptDigest: "digest", State: "FINISHED", TechnicalOutcome: "success", TransportDelivered: boolPtr(true), InputTokens: &inputOne, OutputTokens: &outputOne, CachedInputTokens: int64Ptr(10), ImageCount: intPtr(2), RequestBodyBytes: int64Ptr(4096), ReportedCost: &costOne, StartedAt: now.Add(-4 * time.Minute), FinishedAt: timePtr(now.Add(-4 * time.Minute)), Provider: "provider", Model: "model"},
		{CallID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, JobID: identity.NewID(), EventID: identity.NewID(), ExecutionID: identity.NewID(), CorrelationID: "corr-2", Attempt: 2, Mode: "live", ComparisonMode: "CURRENT_ONLY", ModelAlias: "inspection-vision", PromptDigest: "digest", State: "FINISHED", TechnicalOutcome: "malformed_response", TransportDelivered: boolPtr(true), InputTokens: &inputTwo, StartedAt: now.Add(-3 * time.Minute), FinishedAt: timePtr(now.Add(-3 * time.Minute)), Provider: "provider", Model: "model"},
		{CallID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, JobID: identity.NewID(), EventID: identity.NewID(), ExecutionID: identity.NewID(), CorrelationID: "corr-3", Attempt: 1, Mode: "live", ComparisonMode: "CURRENT_ONLY", ModelAlias: "inspection-vision", PromptDigest: "digest", State: "FINISHED", TechnicalOutcome: "transport", TransportDelivered: boolPtr(false), InputTokens: int64Ptr(100), ReportedCost: floatPtr(1), StartedAt: now.Add(-2 * time.Minute), FinishedAt: timePtr(now.Add(-2 * time.Minute)), Provider: "provider", Model: "model"},
		{CallID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, JobID: identity.NewID(), EventID: identity.NewID(), ExecutionID: identity.NewID(), CorrelationID: "corr-4", Attempt: 1, Mode: "live", ComparisonMode: "CURRENT_ONLY", ModelAlias: "inspection-vision", PromptDigest: "digest", State: "STARTED", TechnicalOutcome: "pending", StartedAt: now.Add(-time.Minute), Provider: "", Model: ""},
		{CallID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, JobID: identity.NewID(), EventID: identity.NewID(), ExecutionID: identity.NewID(), CorrelationID: "corr-mock", Attempt: 1, Mode: "mock", ComparisonMode: "CURRENT_ONLY", ModelAlias: "inspection-vision", PromptDigest: "digest", State: "FINISHED", TechnicalOutcome: "success", TransportDelivered: boolPtr(true), InputTokens: int64Ptr(99), ReportedCost: floatPtr(0.5), StartedAt: now.Add(-30 * time.Second), FinishedAt: timePtr(now.Add(-30 * time.Second)), Provider: "mock", Model: "mock"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	raw, err := bus.Ask(context.Background(), Query{TenantID: tenantID, InspectionID: inspectionID, Mode: "live", First: 2})
	if err != nil {
		t.Fatal(err)
	}
	result := raw.(Result)
	if result.AttemptedCalls != 4 || result.DeliveredCalls != 2 || result.IncompleteCalls != 1 || result.InputTokens != 30 || result.OutputTokens != 5 || result.CachedInputTokens != 10 || result.CacheHitCalls != 1 || result.KnownCacheCalls != 1 || result.UnknownCacheCalls != 1 || result.KnownReportedCost != costOne || result.UnknownCostCalls != 1 || result.CostComplete || !result.HasNextPage || len(result.Calls) != 2 {
		t.Fatalf("unexpected aggregate: %+v", result)
	}
	if result.Calls[0].CorrelationID != "corr-4" || result.Calls[1].CorrelationID != "corr-3" {
		t.Fatalf("unexpected order: %+v", result.Calls)
	}
	raw, err = bus.Ask(context.Background(), Query{TenantID: tenantID, InspectionID: inspectionID, Mode: "live", First: 2, After: result.EndCursor})
	if err != nil {
		t.Fatal(err)
	}
	next := raw.(Result)
	if len(next.Calls) != 2 || next.Calls[0].CorrelationID != "corr-2" || next.Calls[1].CorrelationID != "corr-1" || next.HasNextPage {
		t.Fatalf("unexpected next page: %+v", next)
	}
	if _, err := graph.DecodeCursor(result.EndCursor); err != nil {
		t.Fatalf("invalid end cursor: %v", err)
	}
}

func usageQueryDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"usage", "inspections", "platform"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`CREATE TABLE inspections.inspections (
		id blob PRIMARY KEY, tenant_id blob NOT NULL, business_unit_id blob NOT NULL, asset_id blob NOT NULL,
		participant_id blob NOT NULL, template_id blob NOT NULL, template_version_id blob NOT NULL,
		analysis_prompt_snapshot_id blob, project_id blob, stage_id blob, source text NOT NULL,
		source_key text NOT NULL, source_reason text, state_reason text, status text NOT NULL,
		evidence_count integer NOT NULL DEFAULT 0, due_at datetime, deadline_at datetime,
		reminder_instants blob NOT NULL, context_snapshot blob NOT NULL, version integer NOT NULL DEFAULT 1,
		created_at datetime NOT NULL, updated_at datetime
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE usage.llm_calls (
		call_id blob PRIMARY KEY, tenant_id blob NOT NULL, inspection_id blob NOT NULL, job_id blob NOT NULL,
		event_id blob NOT NULL, execution_id blob NOT NULL, correlation_id text NOT NULL, attempt integer NOT NULL,
		replay_generation integer NOT NULL, mode text NOT NULL, comparison_mode text NOT NULL, model_alias text NOT NULL,
		prompt_digest text NOT NULL, provider text NOT NULL, model text NOT NULL, gateway_request_id text NOT NULL,
		state text NOT NULL, technical_outcome text NOT NULL, transport_delivered numeric, http_status integer,
		input_tokens integer, output_tokens integer, cached_input_tokens integer, image_count integer,
		request_body_bytes integer, reported_cost numeric, duration_ms integer,
		started_at datetime NOT NULL, finished_at datetime, updated_at datetime
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE platform.schema_migrations (version integer PRIMARY KEY, applied_at datetime NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func boolPtr(value bool) *bool           { return &value }
func int64Ptr(value int64) *int64        { return &value }
func intPtr(value int) *int              { return &value }
func floatPtr(value float64) *float64    { return &value }
func timePtr(value time.Time) *time.Time { return &value }
