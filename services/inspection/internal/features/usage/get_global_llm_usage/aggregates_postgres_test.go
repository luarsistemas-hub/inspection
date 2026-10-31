package get_global_llm_usage

import (
	"context"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestCostAggregatesOnPostgres(t *testing.T) {
	dsn := os.Getenv("INSPECTION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("INSPECTION_TEST_DATABASE_URL não configurada")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := (database.Migrator{DB: db}).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	tenantID := identity.NewID()
	if err := tx.Create(&database.Tenant{ID: tenantID, TenantID: tenantID, Name: "aggregate-test-" + tenantID.String(), Language: "pt-BR", DefaultTimezone: "UTC", Status: "ACTIVE"}).Error; err != nil {
		t.Fatal(err)
	}
	inspectionID := identity.NewID()
	var cutoff struct {
		AppliedAt time.Time `gorm:"column:applied_at"`
	}
	if err := tx.Raw("SELECT applied_at FROM platform.schema_migrations WHERE version = 96").Scan(&cutoff).Error; err != nil {
		t.Fatal(err)
	}
	if cutoff.AppliedAt.IsZero() {
		t.Fatal("migration 96 cutoff is missing")
	}
	from := cutoff.AppliedAt.UTC().Add(time.Second)
	to := from.Add(2 * time.Minute)
	trueValue, falseValue := true, false
	statusOK, statusError := 200, 500
	costZero, costOne := float64(0), float64(1)
	sourceHeader, sourceUnknown, sourceMissing, sourceBody := "header", "unknown", "error_missing", "body"
	states := []database.LLMCallRecord{
		{CallID: identity.NewID(), TenantID: tenantID, InspectionID: &inspectionID, JobID: identity.NewID(), EventID: identity.NewID(), ExecutionID: identity.NewID(), CorrelationID: "confirmed", Attempt: 1, Mode: "live", ComparisonMode: "CURRENT_ONLY", ModelAlias: "model-a", PromptDigest: "digest", Provider: "test-provider", Model: "model-a", State: "FINISHED", TechnicalOutcome: "success", TransportDelivered: &trueValue, HTTPStatus: &statusOK, InputTokens: int64Pointer(5), ReportedCost: &costOne, CostSource: &sourceHeader, StartedAt: from.Add(10 * time.Second)},
		{CallID: identity.NewID(), TenantID: tenantID, InspectionID: &inspectionID, JobID: identity.NewID(), EventID: identity.NewID(), ExecutionID: identity.NewID(), CorrelationID: "unknown", Attempt: 1, Mode: "live", ComparisonMode: "CURRENT_ONLY", ModelAlias: "model-a", PromptDigest: "digest", Provider: "test-provider", Model: "model-a", State: "FINISHED", TechnicalOutcome: "success", TransportDelivered: &trueValue, HTTPStatus: &statusOK, ReportedCost: &costZero, CostSource: &sourceUnknown, StartedAt: from.Add(20 * time.Second)},
		{CallID: identity.NewID(), TenantID: tenantID, InspectionID: &inspectionID, JobID: identity.NewID(), EventID: identity.NewID(), ExecutionID: identity.NewID(), CorrelationID: "started", Attempt: 1, Mode: "live", ComparisonMode: "CURRENT_ONLY", ModelAlias: "model-a", PromptDigest: "digest", Provider: "test-provider", Model: "model-a", State: "STARTED", TechnicalOutcome: "started", StartedAt: from.Add(30 * time.Second)},
		{CallID: identity.NewID(), TenantID: tenantID, JobID: identity.NewID(), EventID: identity.NewID(), ExecutionID: identity.NewID(), CorrelationID: "not-sent", Attempt: 1, Mode: "live", ComparisonMode: "VIDEO_SCOPE", ModelAlias: "model-a", PromptDigest: "digest", Provider: "test-provider", Model: "model-a", State: "FINISHED", TechnicalOutcome: "invalid_input", TransportDelivered: &falseValue, StartedAt: from.Add(40 * time.Second)},
		{CallID: identity.NewID(), TenantID: tenantID, InspectionID: &inspectionID, JobID: identity.NewID(), EventID: identity.NewID(), ExecutionID: identity.NewID(), CorrelationID: "error", Attempt: 1, Mode: "live", ComparisonMode: "CURRENT_ONLY", ModelAlias: "model-a", PromptDigest: "digest", Provider: "test-provider", Model: "model-a", State: "FINISHED", TechnicalOutcome: "http_error", TransportDelivered: &trueValue, HTTPStatus: &statusError, CostSource: &sourceMissing, StartedAt: from.Add(50 * time.Second)},
		{CallID: identity.NewID(), TenantID: tenantID, InspectionID: &inspectionID, JobID: identity.NewID(), EventID: identity.NewID(), ExecutionID: identity.NewID(), CorrelationID: "body", Attempt: 1, Mode: "live", ComparisonMode: "CURRENT_ONLY", ModelAlias: "model-a", PromptDigest: "digest", Provider: "test-provider", Model: "model-a", State: "FINISHED", TechnicalOutcome: "success", TransportDelivered: &trueValue, HTTPStatus: &statusOK, CostSource: &sourceBody, StartedAt: from.Add(60 * time.Second)},
		{CallID: identity.NewID(), TenantID: tenantID, InspectionID: &inspectionID, JobID: identity.NewID(), EventID: identity.NewID(), ExecutionID: identity.NewID(), CorrelationID: "qa", Attempt: 1, Mode: "live", ComparisonMode: "CURRENT_ONLY", ModelAlias: "model-a", PromptDigest: "digest", Provider: "qa", Model: "model-a", State: "FINISHED", TechnicalOutcome: "success", TransportDelivered: &trueValue, HTTPStatus: &statusOK, ReportedCost: &costOne, CostSource: &sourceHeader, StartedAt: from.Add(70 * time.Second)},
	}
	for i := range states {
		if err := tx.Create(&states[i]).Error; err != nil {
			t.Fatalf("insert call %d: %v", i, err)
		}
	}

	result, err := handle(context.Background(), tx, Query{From: from, To: to, Mode: "live", First: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.AttemptedCalls != 6 || result.DeliveredCalls != 4 || result.KnownReportedCostDecimal != "1.000000000000" {
		t.Fatalf("default QA-excluded aggregate: %+v", result)
	}
	if len(result.Calls) != 1 || result.Calls[0].CostState != "unverified" {
		t.Fatalf("first call projection: %+v", result.Calls)
	}
	if !result.HasNextPage {
		t.Fatal("aggregate query did not preserve a next call page")
	}
	errorOnly, err := handle(context.Background(), tx, Query{From: from, To: to, Mode: "live", Provider: "test-provider", TechnicalOutcome: "http_error", First: 10})
	if err != nil {
		t.Fatal(err)
	}
	if errorOnly.AttemptedCalls != 1 || errorOnly.ErrorWithoutCostCalls != 1 || !errorOnly.CostComplete || !errorOnly.CoverageComplete {
		t.Fatalf("global completeness for an error without cost: %+v", errorOnly)
	}

	type tenantAggregate struct {
		TenantID         string `gorm:"column:tenant_id"`
		TenantName       string `gorm:"column:tenant_name"`
		Attempted        int    `gorm:"column:attempted_calls"`
		Confirmed        int    `gorm:"column:confirmed_cost_calls"`
		Unknown          int    `gorm:"column:unknown_cost_calls"`
		Uncertain        int    `gorm:"column:uncertain_cost_calls"`
		Errors           int    `gorm:"column:error_without_cost_calls"`
		Unverified       int    `gorm:"column:unverified_cost_calls"`
		CostComplete     bool   `gorm:"column:cost_complete"`
		CoverageComplete bool   `gorm:"column:coverage_complete"`
	}
	var ownTenant *tenantAggregate
	var afterName *string
	var afterID any
	for page := 0; page < 100; page++ {
		var pageRows []tenantAggregate
		if err := tx.Raw(`SELECT tenant_id::text,tenant_name,attempted_calls,confirmed_cost_calls,unknown_cost_calls,uncertain_cost_calls,error_without_cost_calls,unverified_cost_calls,cost_complete,coverage_complete FROM usage.read_llm_usage_by_tenant_v1(?,?,?,?,?,?,?,?,?,?,?,?,?)`, from, to, nil, nil, nil, nil, "live", nil, nil, false, afterName, afterID, 1).Scan(&pageRows).Error; err != nil {
			t.Fatal(err)
		}
		if len(pageRows) == 0 {
			break
		}
		if pageRows[0].TenantID == tenantID.String() {
			ownTenant = &pageRows[0]
			break
		}
		name, id := pageRows[0].TenantName, pageRows[0].TenantID
		afterName, afterID = &name, id
	}
	if ownTenant == nil || ownTenant.Attempted != 6 || ownTenant.Confirmed != 1 || ownTenant.Unknown != 1 || ownTenant.Uncertain != 1 || ownTenant.Errors != 1 || ownTenant.Unverified != 1 {
		t.Fatalf("tenant cost states: %+v", ownTenant)
	}
	if ownTenant.CostComplete || ownTenant.CoverageComplete {
		t.Fatalf("tenant containing unknown and unverified costs must be incomplete: %+v", ownTenant)
	}
	var errorTenant []struct {
		CostComplete     bool `gorm:"column:cost_complete"`
		CoverageComplete bool `gorm:"column:coverage_complete"`
	}
	if err := tx.Raw(`SELECT cost_complete,coverage_complete FROM usage.read_llm_usage_by_tenant_v1(?,?,?,?,?,?,?,?,?,?,?,?,?)`, from, to, "test-provider", nil, nil, "http_error", "live", nil, nil, false, nil, nil, 10).Scan(&errorTenant).Error; err != nil {
		t.Fatal(err)
	}
	if len(errorTenant) != 1 || !errorTenant[0].CostComplete || !errorTenant[0].CoverageComplete {
		t.Fatalf("tenant error-only completeness: %+v", errorTenant)
	}
	var filteredTenant []struct {
		Attempted int `gorm:"column:attempted_calls"`
		Confirmed int `gorm:"column:confirmed_cost_calls"`
	}
	if err := tx.Raw(`SELECT attempted_calls,confirmed_cost_calls FROM usage.read_llm_usage_by_tenant_v1(?,?,?,?,?,?,?,?,?,?,?,?,?)`, from, to, "test-provider", nil, nil, nil, "live", nil, "informed", false, nil, nil, 10).Scan(&filteredTenant).Error; err != nil {
		t.Fatal(err)
	}
	if len(filteredTenant) != 1 || filteredTenant[0].Attempted != 1 || filteredTenant[0].Confirmed != 1 {
		t.Fatalf("tenant provider and cost filters: %+v", filteredTenant)
	}
	type inspectionAggregate struct {
		InspectionID *string    `gorm:"column:inspection_id"`
		Label        string     `gorm:"column:inspection_label"`
		LastStarted  *time.Time `gorm:"column:last_started_at"`
		Attempted    int        `gorm:"column:attempted_calls"`
	}
	readInspections := func(afterTime *time.Time, afterWithout bool, afterInspectionID any) []inspectionAggregate {
		t.Helper()
		var pageRows []inspectionAggregate
		if err := tx.Raw(`SELECT inspection_id::text,inspection_label,last_started_at,attempted_calls FROM usage.read_llm_usage_by_inspection_v1(?::uuid,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, from, to, nil, nil, nil, nil, "live", nil, nil, false, afterTime, afterWithout, afterInspectionID, 1).Scan(&pageRows).Error; err != nil {
			t.Fatal(err)
		}
		return pageRows
	}
	noInspectionPage := readInspections(nil, false, nil)
	if len(noInspectionPage) != 1 || noInspectionPage[0].InspectionID != nil || noInspectionPage[0].Attempted != 1 {
		t.Fatalf("no-inspection page: %+v", noInspectionPage)
	}
	inspectionPage := readInspections(nil, true, nil)
	if len(inspectionPage) != 1 || inspectionPage[0].InspectionID == nil || *inspectionPage[0].InspectionID != inspectionID.String() || inspectionPage[0].Attempted != 5 {
		t.Fatalf("next inspection page: %+v", inspectionPage)
	}
	var filteredInspection []struct {
		Attempted int `gorm:"column:attempted_calls"`
		Confirmed int `gorm:"column:confirmed_cost_calls"`
	}
	if err := tx.Raw(`SELECT attempted_calls,confirmed_cost_calls FROM usage.read_llm_usage_by_inspection_v1(?::uuid,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, from, to, "test-provider", nil, nil, nil, "live", nil, "informed", false, nil, false, nil, 10).Scan(&filteredInspection).Error; err != nil {
		t.Fatal(err)
	}
	if len(filteredInspection) != 1 || filteredInspection[0].Attempted != 1 || filteredInspection[0].Confirmed != 1 {
		t.Fatalf("inspection provider and cost filters: %+v", filteredInspection)
	}
	var errorInspection []struct {
		CostComplete     bool `gorm:"column:cost_complete"`
		CoverageComplete bool `gorm:"column:coverage_complete"`
	}
	if err := tx.Raw(`SELECT cost_complete,coverage_complete FROM usage.read_llm_usage_by_inspection_v1(?::uuid,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, from, to, "test-provider", nil, nil, "http_error", "live", nil, nil, false, nil, false, nil, 10).Scan(&errorInspection).Error; err != nil {
		t.Fatal(err)
	}
	if len(errorInspection) != 1 || !errorInspection[0].CostComplete || !errorInspection[0].CoverageComplete {
		t.Fatalf("inspection error-only completeness: %+v", errorInspection)
	}

	callStates := map[string]bool{}
	callPage := Query{From: from, To: to, Mode: "live", First: 1}
	for page := 0; page < 10; page++ {
		pageResult, pageErr := handle(context.Background(), tx, callPage)
		if pageErr != nil {
			t.Fatal(pageErr)
		}
		for _, call := range pageResult.Calls {
			callStates[call.CostState] = true
		}
		if !pageResult.HasNextPage {
			break
		}
		callPage.After = pageResult.EndCursor
	}
	for _, state := range []string{"confirmed", "unknown", "uncertain", "not_sent", "error_without_cost", "unverified"} {
		if !callStates[state] {
			t.Errorf("missing call cost state %q in pages: %#v", state, callStates)
		}
	}
	filtered, err := handle(context.Background(), tx, Query{From: from, To: to, Mode: "live", Provider: "test-provider", CostState: "informed", First: 1})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.AttemptedCalls != 1 || filtered.KnownReportedCostDecimal != "1.000000000000" || len(filtered.Calls) != 1 || filtered.Calls[0].CostState != "confirmed" {
		t.Fatalf("provider and cost-state filters: %+v", filtered)
	}
	withQA, err := handle(context.Background(), tx, Query{From: from, To: to, Mode: "live", First: 1, IncludeTestData: true})
	if err != nil {
		t.Fatal(err)
	}
	if withQA.AttemptedCalls != 7 || withQA.KnownReportedCostDecimal != "2.000000000000" {
		t.Fatalf("includeTestData aggregate: %+v", withQA)
	}
}

func int64Pointer(value int64) *int64 { return &value }
