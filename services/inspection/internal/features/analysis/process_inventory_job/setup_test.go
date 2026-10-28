package process_inventory_job

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	inventoryexecutor "inspection/services/inspection/internal/features/analysis/inventory/core"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

func TestSetupRejectsMissingDependencies(t *testing.T) {
	if processor, err := Setup(Dependencies{}); err == nil || processor != nil {
		t.Fatalf("Setup(empty) = %v, %v; want dependency error", processor, err)
	}
	if processor, err := Setup(Dependencies{DB: &gorm.DB{}, Store: objectstore.Store{}}); err == nil || processor != nil {
		t.Fatalf("Setup(without gateway) = %v, %v; want dependency error", processor, err)
	}
}

func TestDeriveItemResultRules(t *testing.T) {
	cases := []struct {
		name  string
		claim ItemClaim
		facts ItemFacts
		want  ItemOutcome
	}{
		{"UT-205 unchanged without confirmed pair", ItemClaim{Status: "PRESENT_UNCHANGED"}, ItemFacts{ConditionBefore: "GOOD"}, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: "NO_CORRESPONDING_VIEW"}},
		{"UT-206 missing without confirmed view", ItemClaim{Status: "MISSING"}, ItemFacts{ConditionBefore: "GOOD", Observation: "OBSERVED"}, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: "NO_CORRESPONDING_VIEW"}},
		{"missing with confirmed pair", ItemClaim{Status: "MISSING"}, ItemFacts{ConditionBefore: "GOOD", Confirmed: true}, ItemOutcome{Status: "MISSING"}},
		{"missing with confirmed view of location", ItemClaim{Status: "MISSING"}, ItemFacts{ConditionBefore: "GOOD", Observation: "OBSERVED", EnvironmentViewed: true}, ItemOutcome{Status: "MISSING"}},
		{"UT-207 worsened with confirmed pair", ItemClaim{Status: "CONDITION_CHANGED", ConditionAfter: "FAIR"}, ItemFacts{ConditionBefore: "GOOD", Confirmed: true}, ItemOutcome{Status: "CONDITION_CHANGED", ConditionAfter: "FAIR"}},
		{"UT-208 improved with confirmed pair", ItemClaim{Status: "CONDITION_CHANGED", ConditionAfter: "GOOD"}, ItemFacts{ConditionBefore: "POOR", Confirmed: true}, ItemOutcome{Status: "IMPROVED", ConditionAfter: "GOOD"}},
		{"UT-209 declared never condition changed", ItemClaim{Status: "CONDITION_CHANGED", ConditionAfter: "POOR"}, ItemFacts{ConditionBefore: "GOOD", Declared: true}, ItemOutcome{Status: "PRESENT_UNCHANGED", ConditionAfter: "POOR"}},
		{"UT-211 obstructed", ItemClaim{Status: "NOT_OBSERVED", ReasonCode: "OBSTRUCTED"}, ItemFacts{ConditionBefore: "GOOD", Confirmed: true}, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: "OBSTRUCTED"}},
		{"unknown reason falls back", ItemClaim{Status: "NOT_OBSERVED", ReasonCode: "BLUR"}, ItemFacts{}, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: "NO_CORRESPONDING_VIEW"}},
		{"change without assessable condition", ItemClaim{Status: "CONDITION_CHANGED", ConditionAfter: "NOT_ASSESSABLE"}, ItemFacts{ConditionBefore: "GOOD", Confirmed: true}, ItemOutcome{Status: "NOT_OBSERVED", ReasonCode: "QUALITY"}},
		{"unchanged claim with worse condition", ItemClaim{Status: "PRESENT_UNCHANGED", ConditionAfter: "POOR"}, ItemFacts{ConditionBefore: "GOOD", Confirmed: true}, ItemOutcome{Status: "CONDITION_CHANGED", ConditionAfter: "POOR"}},
		{"outside status is not an item claim", ItemClaim{Status: "OUTSIDE_INVENTORY"}, ItemFacts{}, ItemOutcome{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveItemResult(tc.claim, tc.facts); got != tc.want {
				t.Fatalf("DeriveItemResult = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// unitHarness wires the processor to a seeded database and a scripted gateway.
type unitHarness struct {
	seed      *inventorytest.Seed
	gateway   *inventorytest.Gateway
	processor *Processor
	executor  *inventoryexecutor.Executor
}

func newHarness(t *testing.T, seed *inventorytest.Seed) *unitHarness {
	t.Helper()
	gateway := &inventorytest.Gateway{}
	processor, err := Setup(Dependencies{DB: seed.DB, Store: seed.Store.Store(), Gateway: gateway, Now: func() time.Time { return seed.Now }, Within: seed.Within})
	if err != nil {
		t.Fatal(err)
	}
	executor := &inventoryexecutor.Executor{DB: seed.DB, ClaimDB: seed.DB, Processor: processor, Now: func() time.Time { return seed.Now }, HeartbeatInterval: time.Hour}
	return &unitHarness{seed: seed, gateway: gateway, processor: processor, executor: executor}
}

// process runs one job through Process with a plain transaction as fence.
func (h *unitHarness) process(t *testing.T, job database.InventoryJob) error {
	t.Helper()
	return h.processor.Process(context.Background(), job, func(write func(*gorm.DB) error) error { return h.seed.DB.Transaction(write) })
}

// drain runs the executor until no job is due, advancing the clock past backoff.
func (h *unitHarness) drain(t *testing.T) {
	t.Helper()
	for i := 0; i < 50; i++ {
		worked, err := h.executor.RunDue(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			var pending database.InventoryJob
			if err := h.seed.DB.Where("status='PENDING'").Order("next_attempt_at").Take(&pending).Error; err != nil {
				return
			}
			h.seed.Now = pending.NextAttemptAt
		}
	}
	t.Fatal("executor did not drain")
}

func reloadJob(t *testing.T, db *gorm.DB, id identity.ID) database.InventoryJob {
	t.Helper()
	var job database.InventoryJob
	if err := db.Where("id=?", id).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	return job
}

// userData decodes the JSON data field of an inventory user prompt.
func userData(t *testing.T, request llm.StructuredRequest) map[string]any {
	t.Helper()
	index := strings.Index(request.UserPrompt, "{")
	if index < 0 {
		t.Fatalf("user prompt without JSON data: %q", request.UserPrompt)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(request.UserPrompt[index:]), &data); err != nil {
		t.Fatal(err)
	}
	return data
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

var errGateway = errors.New("gateway unavailable")
