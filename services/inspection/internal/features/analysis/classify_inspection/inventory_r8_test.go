package classify_inspection

import (
	"context"
	"testing"
	"time"

	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	requestinventoryjobs "inspection/services/inspection/internal/features/analysis/request_inventory_jobs"
	"inspection/services/inspection/internal/platform/database"
)

// Item 3 (round 8): the classification itself already waits for the newest
// COMPARE generation opened by reissueReport (Reanalyze); it resumes with the
// results of that generation once every job is terminal.
func TestR8ReanalysisGenerationHoldsClassificationUntilTerminal(t *testing.T) {
	seed := inventorytest.New(t, inventorytest.InspectionOptions{})
	baseline := seed.PinBaseline(t, room)
	job := seed.Job(t, "COMPARE", inventorytest.EnvironmentKey(baseline.Environments[0]), baseline.Version, &baseline.Environments[0].IdentityID)
	finish(t, seed, job, "SUCCEEDED", result(baseline.Items[0], "PRESENT_UNCHANGED", ""), result(baseline.Items[1], "PRESENT_UNCHANGED", ""))
	if facts := inventoryFacts(t, seed); !facts.Terminal {
		t.Fatal("first generation must be terminal")
	}
	if created, err := requestinventoryjobs.Reanalyze(context.Background(), seed.DB, seed.Tenant, seed.Inspection, time.Now().UTC()); err != nil || created != 1 {
		t.Fatalf("Reanalyze created=%d err=%v", created, err)
	}
	if facts := inventoryFacts(t, seed); facts.Terminal {
		t.Fatal("a pending re-analysis generation must hold classification")
	}
	var next database.InventoryJob
	if err := seed.DB.Where("inspection_id=? AND kind='COMPARE' AND generation=2", seed.Inspection).Take(&next).Error; err != nil {
		t.Fatal(err)
	}
	finish(t, seed, next, "SUCCEEDED", result(baseline.Items[0], "MISSING", ""), result(baseline.Items[1], "PRESENT_UNCHANGED", ""))
	facts := inventoryFacts(t, seed)
	if !facts.Terminal || facts.Facts.Missing != 1 {
		t.Fatalf("facts = %+v, want the terminal second generation (one missing item)", facts)
	}
}
