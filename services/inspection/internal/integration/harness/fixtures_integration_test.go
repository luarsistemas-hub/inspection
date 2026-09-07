//go:build integration

package harness

import (
	"context"
	"testing"

	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestTask06FixtureSeedsCompleteGraph(t *testing.T) {
	ctx := context.Background()
	h, err := NewFromEnv(ctx)
	if err != nil {
		t.Skipf("integration dependencies unavailable: %v", err)
	}
	defer h.Close()

	fixture, err := h.SeedTask06Fixture(ctx, Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var inspection database.Inspection
		if err := tx.First(&inspection, "id = ?", fixture.InspectionID).Error; err != nil {
			return err
		}
		if inspection.Status != "SUBMITTED" || inspection.ProjectID == nil || *inspection.ProjectID != fixture.ProjectID {
			t.Fatalf("unexpected inspection fixture: %+v", inspection)
		}
		var answer database.RequirementAnswer
		if err := tx.First(&answer, "draft_id = ?", fixture.DraftID).Error; err != nil {
			return err
		}
		if answer.RequirementKey != "room-1" {
			t.Fatalf("unexpected requirement answer: %+v", answer)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
