package resolvers

import (
	"testing"

	"inspection/libs/identity"

	"gorm.io/gorm"
)

// seedTask06PlanFrames makes video frames members of a sealed plan so report
// snapshots pass the plan frame integrity check.
func seedTask06PlanFrames(t *testing.T, db *gorm.DB, tenantID, planID identity.ID, frameIDs ...identity.ID) {
	t.Helper()
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS analysis.video_plan_items (id blob primary key, tenant_id blob, plan_id blob, carried_result_job_id blob)`,
		`CREATE TABLE IF NOT EXISTS analysis.video_plan_item_frames (id blob primary key, tenant_id blob, plan_item_id blob, frame_id blob)`,
		`CREATE TABLE IF NOT EXISTS analysis.comparison_jobs (id blob primary key, tenant_id blob, video_plan_item_id blob)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	itemID := identity.NewID()
	if err := db.Exec(`INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id) VALUES (?,?,?)`, itemID, tenantID, planID).Error; err != nil {
		t.Fatal(err)
	}
	for _, frameID := range frameIDs {
		if err := db.Exec(`INSERT INTO analysis.video_plan_item_frames (id,tenant_id,plan_item_id,frame_id) VALUES (?,?,?,?)`, identity.NewID(), tenantID, itemID, frameID).Error; err != nil {
			t.Fatal(err)
		}
	}
}
