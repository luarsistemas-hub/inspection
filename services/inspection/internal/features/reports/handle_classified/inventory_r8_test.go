package handle_classified

import (
	"context"
	"errors"
	"testing"
	"time"

	requestinventoryjobs "inspection/services/inspection/internal/features/analysis/request_inventory_jobs"
	"inspection/services/inspection/internal/platform/database"
)

// Item 3 (round 8): reissueReport opens a new COMPARE generation through the
// production Reanalyze. Until that generation is terminal, the classified
// handler must wait (retryable prerequisite) instead of publishing a version
// whose inventory lost every problem; once it is terminal, the report builds
// from the new results.
func TestR8PendingReanalysisGenerationHoldsInventoryReport(t *testing.T) {
	f := newReportFixture(t)
	if len(f.section.Problems) == 0 {
		t.Fatal("fixture must start with inventory problems")
	}
	created, err := requestinventoryjobs.Reanalyze(context.Background(), f.seed.DB, f.seed.Tenant, f.seed.Inspection, time.Now().UTC())
	if err != nil || created == 0 {
		t.Fatalf("Reanalyze created=%d err=%v", created, err)
	}
	var inspection database.Inspection
	var reference database.ReferenceSnapshot
	f.seed.DB.Where("id=?", f.seed.Inspection).Take(&inspection)
	f.seed.DB.Where("inspection_id=?", f.seed.Inspection).Take(&reference)
	section, err := buildInventoryReport(context.Background(), f.seed.DB, f.seed.Tenant, inspection, reference, time.Now().UTC(), nil)
	if !errors.Is(err, ErrClassifiedPrerequisitePending) {
		t.Fatalf("pending re-analysis generation built section=%+v err=%v; want ErrClassifiedPrerequisitePending", section, err)
	}
	var next []database.InventoryJob
	if err := f.seed.DB.Where("inspection_id=? AND kind='COMPARE' AND generation=2", f.seed.Inspection).Find(&next).Error; err != nil || len(next) != created {
		t.Fatalf("new generation jobs=%d err=%v", len(next), err)
	}
	for _, job := range next {
		if err := f.seed.DB.Model(&database.InventoryJob{}).Where("id=?", job.ID).Update("status", "SUCCEEDED").Error; err != nil {
			t.Fatal(err)
		}
	}
	section, _ = build(t, f.seed)
	if section == nil || section.Coverage == nil {
		t.Fatalf("terminal re-analysis generation did not build the inventory: %+v", section)
	}
}
