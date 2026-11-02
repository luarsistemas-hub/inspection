//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"inspection/services/inspection/internal/features/usage/get_global_llm_usage"
	"inspection/services/inspection/internal/features/usage/get_inspection_llm_usage"
	"inspection/services/inspection/internal/features/usage/list_llm_usage_tenants"
	"inspection/services/inspection/internal/features/usage/llm_usage_by_inspection"
	"inspection/services/inspection/internal/features/usage/llm_usage_by_tenant"
	"inspection/services/inspection/internal/features/usage/record_llm_call"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

func TestLLMUsageTranscriptionPostgresIT255IT257IT258IT259IT260IT262IT263IT264(t *testing.T) {
	fx := newAPI06Fixture(t)
	defer cleanupLLMUsageTenant(t, fx)
	server, _ := usageGraphQLServer(t, fx)
	meta, _ := requestctx.FromContext(fx.ctx)
	meta.Principal.Issuer, meta.Principal.Subject = "r2it", "ledger-admin"
	globalCtx := requestctx.WithMetadata(context.Background(), meta)
	started := time.Now().UTC().Truncate(time.Microsecond)
	ctx := globalCtx

	// IT-255 malformed/unusable provider cost metadata remains an unknown-cost attempt.
	badMetadata := newLedgerCall(fx, started, 1, "TRANSCRIPTION", nil)
	badMetadata.CostSource = stringPtr("unknown")
	badMetadata.AudioDurationMS = int64ptr(4200)
	badMetadata.TransportDelivered = usageBoolPtr(true)
	badMetadata.HTTPStatus = usageIntPtr(200)
	if err := fx.h.AdminDB.Create(&badMetadata).Error; err != nil {
		t.Fatal(err)
	}
	malformedLedger, err := record_llm_call.Setup(record_llm_call.Dependencies{DB: fx.h.DB})
	if err != nil {
		t.Fatal(err)
	}
	if err := malformedLedger.ReportLateCost(ctx, record_llm_call.LateCost{TenantID: fx.seed.TenantID.String(), CallID: badMetadata.CallID.String(), Cost: math.NaN(), Source: "body"}); err == nil {
		t.Fatal("IT-255 malformed cost metadata should remain unavailable")
	}
	view := runUsageQuery(t, server, ctx, fmt.Sprintf(`query { inspectionLLMUsage(inspectionId:%q,first:5) { attemptedCalls unknownCostCalls calls { callId audioDurationMs audioInputTokens costState } } }`, fx.seed.InspectionID))
	if !strings.Contains(view, `"attemptedCalls":1`) || !strings.Contains(view, `"unknownCostCalls":1`) || !strings.Contains(view, `"audioDurationMs":4200`) || !strings.Contains(view, `"costState":"UNKNOWN"`) {
		t.Fatalf("IT-255 malformed cost metadata lost attempt or duration: %s", view)
	}

	// IT-257 pagination applies the same filters and keeps aggregate totals stable.
	for i := 2; i <= 7; i++ {
		call := newLedgerCall(fx, started.Add(time.Duration(i)*time.Second), i, "TRANSCRIPTION", nil)
		call.CostSource = stringPtr("unknown")
		if err := fx.h.AdminDB.Create(&call).Error; err != nil {
			t.Fatal(err)
		}
	}
	first := runUsageQuery(t, server, ctx, fmt.Sprintf(`query { inspectionLLMUsage(inspectionId:%q,first:2) { attemptedCalls calls { callId } pageInfo { endCursor hasNextPage } } }`, fx.seed.InspectionID))
	if !strings.Contains(first, `"attemptedCalls":7`) || !strings.Contains(first, `"hasNextPage":true`) {
		t.Fatalf("IT-257 first page=%s", first)
	}
	var page struct {
		Data struct {
			Usage struct {
				Page struct {
					End string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"inspectionLLMUsage"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(first), &page); err != nil {
		t.Fatal(err)
	}
	second := runUsageQuery(t, server, ctx, fmt.Sprintf(`query { inspectionLLMUsage(inspectionId:%q,first:2,after:%q) { attemptedCalls calls { callId } pageInfo { hasNextPage } } }`, fx.seed.InspectionID, page.Data.Usage.Page.End))
	if !strings.Contains(second, `"attemptedCalls":7`) || strings.Count(second, `"callId"`) != 2 {
		t.Fatalf("IT-257 subsequent page=%s", second)
	}

	// IT-258 tenant admins cannot query another tenant's usage through inspection scope.
	otherTenant := newAPI06Fixture(t)
	defer cleanupLLMUsageTenant(t, otherTenant)
	foreignCall := newLedgerCall(otherTenant, started, 1, "TRANSCRIPTION", nil)
	if err := otherTenant.h.AdminDB.Create(&foreignCall).Error; err != nil {
		t.Fatal(err)
	}
	denied := runUsageQuery(t, server, fx.ctx, fmt.Sprintf(`query { inspectionLLMUsage(inspectionId:%q) { attemptedCalls calls { callId } } }`, otherTenant.seed.InspectionID))
	if !strings.Contains(denied, `"errors"`) || strings.Contains(denied, foreignCall.CallID.String()) {
		t.Fatalf("IT-258 cross-tenant result=%s", denied)
	}

	// IT-259 the read shows one attempt with its latest finalized status.
	latest := newLedgerCall(fx, started.Add(90*time.Second), 1, "TRANSCRIPTION", nil)
	latest.State, latest.TechnicalOutcome = "STARTED", "pending"
	latest.CostSource = stringPtr("unknown")
	if err := fx.h.AdminDB.Create(&latest).Error; err != nil {
		t.Fatal(err)
	}
	pendingView := runUsageQuery(t, server, ctx, fmt.Sprintf(`query { inspectionLLMUsage(inspectionId:%q,first:20) { attemptedCalls calls { callId state } } }`, fx.seed.InspectionID))
	if !strings.Contains(pendingView, `"attemptedCalls":8`) || !strings.Contains(pendingView, fmt.Sprintf(`"callId":"%s","state":"STARTED"`, latest.CallID)) {
		t.Fatalf("IT-259 initial usage read did not contain one in-flight attempt: %s", pendingView)
	}
	latest.State, latest.TechnicalOutcome = "FINISHED", "failed"
	latest.TransportDelivered = usageBoolPtr(true)
	latest.HTTPStatus = usageIntPtr(500)
	latest.FinishedAt = usageTimePtr(started.Add(time.Minute))
	if err := fx.h.AdminDB.Model(&database.LLMCallRecord{}).Where("call_id=?", latest.CallID).Updates(map[string]any{"state": latest.State, "technical_outcome": latest.TechnicalOutcome, "transport_delivered": true, "http_status": 500, "finished_at": latest.FinishedAt}).Error; err != nil {
		t.Fatal(err)
	}
	latestView := runUsageQuery(t, server, ctx, fmt.Sprintf(`query { inspectionLLMUsage(inspectionId:%q,first:20) { attemptedCalls calls { callId state technicalOutcome } } }`, fx.seed.InspectionID))
	if !strings.Contains(latestView, `"attemptedCalls":8`) || !strings.Contains(latestView, `"state":"FINISHED"`) || !strings.Contains(latestView, `"technicalOutcome":"failed"`) {
		t.Fatalf("IT-259 latest status view=%s", latestView)
	}

	// IT-260 a real storage read failure is surfaced as GraphQL error, never a zero aggregate.
	brokenBus := mediator.New()
	if err := get_global_llm_usage.Setup(get_global_llm_usage.Dependencies{DB: nil, Bus: brokenBus}); err == nil {
		t.Fatal("IT-260 expected invalid dependency to be rejected")
	}
	if err := brokenBus.RegisterQuery(get_global_llm_usage.Query{}, func(context.Context, any) (any, error) { return nil, fmt.Errorf("usage read failed") }); err != nil {
		t.Fatal(err)
	}
	broken := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: fx.h.DB, Bus: brokenBus, Authorizer: auth.Authorizer{}, SuperAdminIssuer: "r2it", SuperAdminSubject: "ledger-admin"}}))
	failed := runUsageQuery(t, broken, globalCtx, `query { llmUsage { attemptedCalls } }`)
	if !strings.Contains(failed, `"errors"`) || strings.Contains(failed, `"attemptedCalls":0`) {
		t.Fatalf("IT-260 failed read response=%s", failed)
	}

	// IT-262 report cost once for a failed call without changing its outcome or creating a transcript.
	call := newLedgerCall(fx, started.Add(2*time.Minute), 1, "TRANSCRIPTION", nil)
	call.State, call.TechnicalOutcome, call.CostSource = "FINISHED", "failed", stringPtr("unknown")
	call.TransportDelivered, call.HTTPStatus = usageBoolPtr(true), usageIntPtr(500)
	call.FinishedAt = usageTimePtr(started.Add(3 * time.Minute))
	if err := fx.h.AdminDB.Create(&call).Error; err != nil {
		t.Fatal(err)
	}
	ledger, err := record_llm_call.Setup(record_llm_call.Dependencies{DB: fx.h.DB})
	if err != nil {
		t.Fatal(err)
	}
	report := record_llm_call.LateCost{TenantID: fx.seed.TenantID.String(), CallID: call.CallID.String(), Cost: 0.0002, Source: "body", ReportedAt: started.Add(4 * time.Minute)}
	if err := ledger.ReportLateCost(ctx, report); err != nil {
		t.Fatalf("IT-262 late cost rejected: %v", err)
	}
	if err := ledger.ReportLateCost(ctx, report); err == nil {
		t.Fatal("IT-262 second cost report should fail")
	}
	if err := fx.h.AdminDB.Model(&database.LLMCallRecord{}).Where("call_id=?", call.CallID).Update("technical_outcome", "success").Error; err == nil {
		t.Fatal("IT-262 unrelated finished-call mutation should fail")
	}
	var stored database.LLMCallRecord
	if err := fx.h.AdminDB.First(&stored, "call_id=?", call.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.State != "FINISHED" || stored.TechnicalOutcome != "failed" || stored.ReportedCost == nil || *stored.ReportedCost != report.Cost {
		t.Fatalf("IT-262 call mutated unexpectedly: %+v", stored)
	}
	lateCostView := runUsageQuery(t, server, ctx, fmt.Sprintf(`query { inspectionLLMUsage(inspectionId:%q,first:20) { calls { callId state technicalOutcome reportedCostDecimal costSource } } }`, fx.seed.InspectionID))
	if !strings.Contains(lateCostView, call.CallID.String()) || !strings.Contains(lateCostView, `"reportedCostDecimal":"0.0002`) || !strings.Contains(lateCostView, `"costSource":"BODY"`) {
		t.Fatalf("IT-262 late cost provenance missing from usage projection: %s", lateCostView)
	}
	var transcripts int64
	if err := fx.h.AdminDB.Table("transcription.transcript_revisions").Where("call_id=?", call.CallID).Count(&transcripts).Error; err != nil {
		t.Fatal(err)
	}
	if transcripts != 0 {
		t.Fatalf("IT-262 invented %d transcript revisions", transcripts)
	}
	visual := newLedgerCall(fx, started.Add(5*time.Minute), 1, "CURRENT_ONLY", nil)
	visual.CostSource = stringPtr("unknown")
	if err := fx.h.AdminDB.Create(&visual).Error; err != nil {
		t.Fatal(err)
	}

	// IT-263 deleting the temporary audio has no effect on the retained usage row.
	retained := runUsageQuery(t, server, ctx, fmt.Sprintf(`query { inspectionLLMUsage(inspectionId:%q,first:20) { calls { callId audioDurationMs } } }`, fx.seed.InspectionID))
	if !strings.Contains(retained, call.CallID.String()) || !strings.Contains(retained, `"audioDurationMs":4200`) {
		t.Fatalf("IT-263 usage not visible after audio lifecycle: %s", retained)
	}

	// IT-264 tenant, inspection and transcription filters isolate a larger set.
	global := runUsageQuery(t, server, globalCtx, fmt.Sprintf(`query { llmUsage(filter:{from:%q,to:%q,tenantId:%q,inspectionId:%q,comparisonMode:"TRANSCRIPTION"},first:50) { attemptedCalls calls { callId comparisonMode inspectionId } } }`, started.Add(-time.Hour).Format(time.RFC3339Nano), started.Add(time.Hour).Format(time.RFC3339Nano), fx.seed.TenantID, fx.seed.InspectionID))
	if !strings.Contains(global, `"attemptedCalls":9`) || strings.Contains(global, `"comparisonMode":"CURRENT_ONLY"`) {
		t.Fatalf("IT-264 filtered usage=%s", global)
	}
	unfiltered := runUsageQuery(t, server, globalCtx, fmt.Sprintf(`query { llmUsage(filter:{from:%q,to:%q,tenantId:%q,inspectionId:%q},first:50) { attemptedCalls } }`, started.Add(-time.Hour).Format(time.RFC3339Nano), started.Add(time.Hour).Format(time.RFC3339Nano), fx.seed.TenantID, fx.seed.InspectionID))
	if !strings.Contains(unfiltered, `"attemptedCalls":10`) {
		t.Fatalf("IT-264 unfiltered total should include the visual attempt: %s", unfiltered)
	}
}

func usageGraphQLServer(t *testing.T, fx api06Fixture) (*handler.Server, *mediator.Bus) {
	t.Helper()
	bus := mediator.New()
	setups := []func() error{
		func() error {
			return get_global_llm_usage.Setup(get_global_llm_usage.Dependencies{DB: fx.h.DB, Bus: bus})
		},
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
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: fx.h.DB, Bus: bus, Authorizer: auth.Authorizer{}, SuperAdminIssuer: "r2it", SuperAdminSubject: "ledger-admin"}}))
	return server, bus
}

func cleanupLLMUsageTenant(t *testing.T, fx api06Fixture) {
	t.Helper()
	err := fx.h.AdminDB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL app.retention_purge='on'").Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=?", fx.seed.TenantID).Delete(&database.LLMCallRecord{}).Error
	})
	if err != nil {
		t.Errorf("cleanup test LLM ledger tenant: %v", err)
	}
}

func runUsageQuery(t *testing.T, server http.Handler, ctx context.Context, query string) string {
	t.Helper()
	return executeTask06GraphQL(t, server, query, ctx)
}

func stringPtr(v string) *string          { return &v }
func usageBoolPtr(v bool) *bool           { return &v }
func usageIntPtr(v int) *int              { return &v }
func usageTimePtr(v time.Time) *time.Time { return &v }
