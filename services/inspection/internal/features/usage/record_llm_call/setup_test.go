package record_llm_call

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/observability"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestLedgerPersistsStartAndFinishWithPartialUsage(t *testing.T) {
	db := ledgerDB(t)
	ledger, err := Setup(Dependencies{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	tenantID, inspectionID := identity.NewID(), identity.NewID()
	jobID, eventID, executionID := identity.NewID(), identity.NewID(), identity.NewID()
	callID := identity.NewID()
	startedAt := time.Unix(100, 0).UTC()
	ctx := observability.WithCorrelation(context.Background(), observability.Correlation{TenantID: tenantID.String()})
	start := llm.CallStart{
		CallID: callID.String(), TenantID: tenantID.String(), InspectionID: inspectionID.String(), JobID: jobID.String(),
		EventID: eventID.String(), ExecutionID: executionID.String(), CorrelationID: "corr", Mode: "live",
		ComparisonMode: "CURRENT_ONLY", ModelAlias: "inspection-vision", PromptDigest: "digest", StartedAt: startedAt,
	}
	if err := ledger.Start(ctx, start); err != nil {
		t.Fatal(err)
	}
	inputTokens := int64(17)
	if err := ledger.Finish(ctx, llm.CallFinish{
		CallID: callID.String(), Provider: "provider", Model: "model", GatewayRequestID: "request",
		TechnicalOutcome: "success", TransportDelivered: true, HTTPStatus: 200, InputTokens: &inputTokens,
		Duration: 1500 * time.Millisecond, FinishedAt: startedAt.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	var row database.LLMCallRecord
	if err := db.Where("call_id = ?", callID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.State != "FINISHED" || row.TechnicalOutcome != "success" || row.Provider != "provider" || row.Model != "model" || row.GatewayRequestID != "request" {
		t.Fatalf("unexpected ledger row: %+v", row)
	}
	if row.TransportDelivered == nil || !*row.TransportDelivered || row.InputTokens == nil || *row.InputTokens != inputTokens || row.OutputTokens != nil || row.ReportedCost != nil {
		t.Fatalf("partial usage was not preserved: %+v", row)
	}
	if row.HTTPStatus == nil || *row.HTTPStatus != 200 || row.DurationMS == nil || *row.DurationMS != 1500 {
		t.Fatalf("technical metadata was not preserved: %+v", row)
	}
}

func TestLedgerFinishDoesNotOverwriteAFinishedCall(t *testing.T) {
	db := ledgerDB(t)
	ledger, err := Setup(Dependencies{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	tenantID, callID := identity.NewID(), identity.NewID()
	ctx := observability.WithCorrelation(context.Background(), observability.Correlation{TenantID: tenantID.String()})
	start := llm.CallStart{
		CallID: callID.String(), TenantID: tenantID.String(), InspectionID: identity.NewID().String(), JobID: identity.NewID().String(),
		EventID: identity.NewID().String(), ExecutionID: identity.NewID().String(), CorrelationID: "corr", Mode: "mock",
		ComparisonMode: "CURRENT_ONLY", ModelAlias: "inspection-vision", PromptDigest: "digest",
	}
	if err := ledger.Start(ctx, start); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Finish(ctx, llm.CallFinish{CallID: callID.String(), TechnicalOutcome: "success", FinishedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Finish(ctx, llm.CallFinish{CallID: callID.String(), TechnicalOutcome: "retry", FinishedAt: time.Now()}); err == nil {
		t.Fatal("expected second finish to fail")
	}
}

func ledgerDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"usage"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AutoMigrate(&database.LLMCallRecord{}); err != nil {
		t.Fatal(err)
	}
	return db
}
