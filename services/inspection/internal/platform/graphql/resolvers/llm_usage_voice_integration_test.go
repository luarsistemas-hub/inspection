//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/usage/get_global_llm_usage"
	"inspection/services/inspection/internal/features/usage/get_inspection_llm_usage"
	"inspection/services/inspection/internal/features/usage/list_llm_usage_tenants"
	"inspection/services/inspection/internal/features/usage/llm_usage_by_inspection"
	"inspection/services/inspection/internal/features/usage/llm_usage_by_tenant"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
)

// TestTranscriptionLLMUsagePostgresIT251IT252IT253IT254IT256IT261 exercises
// the PostgreSQL ledger through GraphQL for voice attempts, partial cost,
// historical categories, an empty filtered view and read replay.
func TestTranscriptionLLMUsagePostgresIT251IT252IT253IT254IT256IT261(t *testing.T) {
	fx := newAPI06Fixture(t)
	defer cleanupLLMUsageTenant(t, fx)
	bus := mediator.New()
	deps := get_global_llm_usage.Dependencies{DB: fx.h.DB, Bus: bus}
	setups := []func() error{
		func() error { return get_global_llm_usage.Setup(deps) },
		func() error {
			return get_inspection_llm_usage.Setup(get_inspection_llm_usage.Dependencies{DB: fx.h.DB, Bus: bus})
		},
		func() error {
			return list_llm_usage_tenants.Setup(list_llm_usage_tenants.Dependencies{DB: fx.h.DB, Bus: bus})
		},
		func() error {
			return llm_usage_by_tenant.Setup(llm_usage_by_tenant.Dependencies{DB: fx.h.DB, Bus: bus})
		},
		func() error {
			return llm_usage_by_inspection.Setup(llm_usage_by_inspection.Dependencies{DB: fx.h.DB, Bus: bus})
		},
	}
	for _, setup := range setups {
		if err := setup(); err != nil {
			t.Fatal(err)
		}
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{
		DB: fx.h.DB, Bus: bus, Authorizer: auth.Authorizer{}, SuperAdminIssuer: "r2it", SuperAdminSubject: "ledger-admin",
	}}))
	meta, _ := requestctx.FromContext(fx.ctx)
	meta.Principal.Issuer, meta.Principal.Subject = "r2it", "ledger-admin"
	ctx := requestctx.WithMetadata(context.Background(), meta)
	started := time.Now().UTC().Truncate(time.Microsecond)
	visualCost := 0.0001
	first, second, visual := newLedgerCall(fx, started, 1, "TRANSCRIPTION", nil), newLedgerCall(fx, started.Add(time.Second), 2, "TRANSCRIPTION", nil), newLedgerCall(fx, started.Add(2*time.Second), 1, "CURRENT_ONLY", &visualCost)
	first.InputTokens, second.InputTokens, visual.InputTokens = int64ptr(8), int64ptr(12), int64ptr(100)
	first.AudioDurationMS, second.AudioDurationMS = int64ptr(30000), int64ptr(30000)
	unknownSource, headerSource, delivered := "unknown", "header", true
	first.CostSource, second.CostSource, visual.CostSource = &unknownSource, &unknownSource, &headerSource
	first.TransportDelivered, second.TransportDelivered, visual.TransportDelivered = &delivered, &delivered, &delivered
	status := 200
	first.HTTPStatus, second.HTTPStatus, visual.HTTPStatus = &status, &status, &status
	if err := fx.h.AdminDB.Create([]*database.LLMCallRecord{&first, &second, &visual}).Error; err != nil {
		t.Fatal(err)
	}
	usageQuery := func(query string) string { return executeTask06GraphQL(t, server, query, ctx) }
	query := fmt.Sprintf(`query { inspectionLLMUsage(inspectionId:"%s",first:10) { attemptedCalls inputTokens outputTokens knownReportedCostDecimal unknownCostCalls costComplete calls { callId attempt replayGeneration comparisonMode modelAlias audioDurationMs reportedCostDecimal costState } pageInfo { hasNextPage } } }`, fx.seed.InspectionID)
	var out struct {
		Data struct {
			Usage struct {
				AttemptedCalls int
				InputTokens    int
				KnownCost      string `json:"knownReportedCostDecimal"`
				Unknown        int    `json:"unknownCostCalls"`
				CostComplete   bool   `json:"costComplete"`
				Calls          []struct {
					CallID          string `json:"callId"`
					Attempt         int
					ComparisonMode  string
					CostState       string `json:"costState"`
					AudioDurationMS *int   `json:"audioDurationMs"`
				}
			} `json:"inspectionLLMUsage"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	raw := usageQuery(query)
	if err := json.Unmarshal([]byte(raw), &out); err != nil || len(out.Errors) != 0 {
		t.Fatalf("IT-251/252/253 inspection usage response=%s err=%v", raw, err)
	}
	if out.Data.Usage.AttemptedCalls != 3 || out.Data.Usage.InputTokens != 120 || out.Data.Usage.Unknown != 2 || out.Data.Usage.CostComplete || len(out.Data.Usage.Calls) != 3 {
		t.Fatalf("IT-251/252/253 transcription attempts/cost projection=%+v", out.Data.Usage)
	}
	transcriptionCalls := 0
	for _, call := range out.Data.Usage.Calls {
		if call.ComparisonMode == "TRANSCRIPTION" {
			transcriptionCalls++
		}
		if call.ComparisonMode == "TRANSCRIPTION" && call.CostState != "UNKNOWN" {
			t.Errorf("IT-251/253 transcription call projection=%+v", call)
		}
	}
	if transcriptionCalls != 2 {
		t.Fatalf("IT-251 transcription ledger has %d transcript attempts, want 2: %+v", transcriptionCalls, out.Data.Usage.Calls)
	}
	for _, call := range out.Data.Usage.Calls {
		if call.ComparisonMode == "TRANSCRIPTION" && (call.AudioDurationMS == nil || *call.AudioDurationMS != 30000) {
			t.Errorf("IT-251 audio duration projection=%+v", call)
		}
	}

	// IT-254 keeps historical visual calls in their original category and total.
	visualQuery := fmt.Sprintf(`query { inspectionLLMUsage(inspectionId:"%s",first:10) { attemptedCalls inputTokens calls { comparisonMode } } }`, fx.seed.InspectionID)
	raw = usageQuery(visualQuery)
	if !strings.Contains(raw, `"attemptedCalls":3`) || !strings.Contains(raw, `"comparisonMode":"CURRENT_ONLY"`) || !strings.Contains(raw, `"inputTokens":120`) {
		t.Fatalf("IT-254 historical visual usage changed: %s", raw)
	}

	// IT-256 an empty transcription filter returns no fabricated calls or cost.
	empty := `query { llmUsage(filter:{tenantId:"` + fx.seed.TenantID.String() + `",comparisonMode:"TRANSCRIPTION",provider:"missing-provider"}) { attemptedCalls knownReportedCostDecimal calls { callId } } }`
	raw = usageQuery(empty)
	if !strings.Contains(raw, `"attemptedCalls":0`) || !strings.Contains(raw, `"calls":[]`) {
		t.Fatalf("IT-256 empty transcription view=%s", raw)
	}

	// IT-261 replaying the read does not duplicate the provider-attempt rows.
	raw = usageQuery(query)
	if !strings.Contains(raw, `"attemptedCalls":3`) {
		t.Fatalf("IT-261 attempt replay changed ledger count: %s", raw)
	}
}

func newLedgerCall(fx api06Fixture, started time.Time, attempt int, mode string, cost *float64) database.LLMCallRecord {
	inspection := fx.seed.InspectionID
	call := database.LLMCallRecord{
		CallID: identity.NewID(), TenantID: fx.seed.TenantID, InspectionID: &inspection,
		JobID: identity.NewID(), EventID: identity.NewID(), ExecutionID: identity.NewID(),
		CorrelationID: "r2it-ledger-" + identity.NewID().String(), Attempt: attempt, ReplayGeneration: 1,
		Mode: "live", ComparisonMode: mode, ModelAlias: "GEMINI_2_5_FLASH_LITE", PromptDigest: "r2it-prompt",
		Provider: "litellm", Model: "gemini-2.5-flash-lite", State: "FINISHED", TechnicalOutcome: "success",
		StartedAt: started, UpdatedAt: started, InputTokens: int64ptr(0), OutputTokens: int64ptr(0), ReportedCost: cost,
	}
	return call
}

func int64ptr(v int64) *int64 { return &v }
