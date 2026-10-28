package classify_inspection

import (
	"context"
	"testing"

	"inspection/libs/identity"
	analysis "inspection/services/inspection/internal/features/analysis/core"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	"inspection/services/inspection/internal/platform/database"
)

var room = inventorytest.BaselineEnvironment{Name: "Sala", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{{Name: "Sofá", Condition: "GOOD"}, {Name: "Mesa", Condition: "GOOD"}}}

func inventoryFacts(t *testing.T, seed *inventorytest.Seed) inventoryClassification {
	t.Helper()
	var inspection database.Inspection
	if err := seed.DB.Where("id=?", seed.Inspection).Take(&inspection).Error; err != nil {
		t.Fatal(err)
	}
	result, has, err := classifyInventoryJobs(context.Background(), seed.DB, seed.Tenant, inspection, seed.Now)
	if err != nil || !has {
		t.Fatalf("classifyInventoryJobs = %v, %v", has, err)
	}
	return result
}

func finish(t *testing.T, seed *inventorytest.Seed, job database.InventoryJob, status string, results ...database.InventoryItemResult) {
	t.Helper()
	if err := seed.DB.Model(&database.InventoryJob{}).Where("id=?", job.ID).Update("status", status).Error; err != nil {
		t.Fatal(err)
	}
	for i := range results {
		results[i].ID, results[i].TenantID, results[i].JobID, results[i].InspectionID, results[i].CreatedAt = identity.NewID(), seed.Tenant, job.ID, seed.Inspection, seed.Now
		if err := seed.DB.Create(&results[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func result(item database.InventoryItem, status, reason string) database.InventoryItemResult {
	id := item.IdentityID
	row := database.InventoryItemResult{ItemIdentityID: &id, Status: status}
	if reason != "" {
		row.ReasonCode = &reason
	}
	return row
}

func TestUT249InventoryJobsNotTerminalHoldClassification(t *testing.T) {
	seed := inventorytest.New(t, inventorytest.InspectionOptions{})
	baseline := seed.PinBaseline(t, room)
	if facts := inventoryFacts(t, seed); facts.Terminal {
		t.Fatal("no COMPARE job yet must hold classification")
	}
	job := seed.Job(t, "COMPARE", inventorytest.EnvironmentKey(baseline.Environments[0]), baseline.Version, &baseline.Environments[0].IdentityID)
	if facts := inventoryFacts(t, seed); facts.Terminal {
		t.Fatal("a PENDING inventory job must hold classification")
	}
	finish(t, seed, job, "SUCCEEDED", result(baseline.Items[0], "PRESENT_UNCHANGED", ""), result(baseline.Items[1], "PRESENT_UNCHANGED", ""))
	facts := inventoryFacts(t, seed)
	if !facts.Terminal || facts.Facts != (analysis.InventoryFacts{BaselineItems: 2}) {
		t.Fatalf("facts = %+v, want terminal and all present (UT-240)", facts)
	}
}

func TestUT250AnalysisFailedItemsNeverClassifyNormal(t *testing.T) {
	seed := inventorytest.New(t, inventorytest.InspectionOptions{})
	baseline := seed.PinBaseline(t, room)
	job := seed.Job(t, "COMPARE", inventorytest.EnvironmentKey(baseline.Environments[0]), baseline.Version, &baseline.Environments[0].IdentityID)
	finish(t, seed, job, "FAILED", result(baseline.Items[0], "NOT_OBSERVED", "ANALYSIS_FAILED"), result(baseline.Items[1], "NOT_OBSERVED", "ANALYSIS_FAILED"))
	decision := analysis.ClassifyInventory(analysis.Classify([]analysis.ComparisonFacts{{Terminal: true}}), inventoryFacts(t, seed).Facts)
	if decision.Classification != analysis.ClassificationAttention {
		t.Fatalf("classification = %s, want ATTENTION", decision.Classification)
	}
}

func TestUT243And251ImpossibleEnvironmentsRequireAttention(t *testing.T) {
	seed := inventorytest.New(t, inventorytest.InspectionOptions{})
	baseline := seed.PinBaseline(t, room, inventorytest.BaselineEnvironment{Name: "Garagem", TypeCode: "GARAGE", Items: []inventorytest.BaselineItem{{Name: "Portão", Condition: "GOOD"}}})
	for _, env := range baseline.Environments {
		seed.Answer(t, inventorytest.EnvironmentKey(env), nil, "LOCKED")
		job := seed.Job(t, "COMPARE", inventorytest.EnvironmentKey(env), baseline.Version, &env.IdentityID)
		var rows []database.InventoryItemResult
		for _, item := range baseline.Items {
			if item.EnvironmentID == env.ID {
				rows = append(rows, result(item, "NOT_OBSERVED", "ENVIRONMENT_NOT_CAPTURED"))
			}
		}
		finish(t, seed, job, "SUCCEEDED", rows...)
	}
	facts := inventoryFacts(t, seed).Facts
	if facts.EnvironmentsNotCaptured != 2 || facts.NotObserved != 3 {
		t.Fatalf("facts = %+v", facts)
	}
	decision := analysis.ClassifyInventory(analysis.Classify([]analysis.ComparisonFacts{{Terminal: true}}), facts)
	if decision.Classification != analysis.ClassificationAttention {
		t.Fatalf("classification = %s, want ATTENTION", decision.Classification)
	}
}

func TestUT244And247FactsCountMissingAndIgnoreInformational(t *testing.T) {
	seed := inventorytest.New(t, inventorytest.InspectionOptions{})
	baseline := seed.PinBaseline(t, room)
	job := seed.Job(t, "COMPARE", inventorytest.EnvironmentKey(baseline.Environments[0]), baseline.Version, &baseline.Environments[0].IdentityID)
	outsideName, outsideType := "Cadeira", "CHAIR"
	finish(t, seed, job, "SUCCEEDED", result(baseline.Items[0], "MISSING", ""), result(baseline.Items[1], "IMPROVED", ""), database.InventoryItemResult{Status: "OUTSIDE_INVENTORY", OutsideName: &outsideName, OutsideTypeCode: &outsideType})
	facts := inventoryFacts(t, seed).Facts
	if facts != (analysis.InventoryFacts{Missing: 1, BaselineItems: 2}) {
		t.Fatalf("facts = %+v, want one missing and nothing for improved/outside", facts)
	}
}
