package prepare_video_comparisons

import (
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPlanEvidenceReferencesCoverOriginAndCurrentIdempotently(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE media.video_evidence_references (id text, tenant_id text, preparation_id text, owner_type text, owner_id text, created_at datetime, UNIQUE(tenant_id, preparation_id, owner_type, owner_id))").Error; err != nil {
		t.Fatal(err)
	}
	origin, current := identity.NewID(), identity.NewID()
	plan := Plan{ID: identity.NewID(), TenantID: identity.NewID(), OriginPreparationID: &origin, CurrentPreparationID: current}
	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		if err := db.Transaction(func(tx *gorm.DB) error { return persistPlanEvidenceReferences(tx, plan, now) }); err != nil {
			t.Fatal(err)
		}
	}
	var references int64
	if err := db.Model(&database.VideoEvidenceReference{}).Where("tenant_id=? AND owner_type='VIDEO_PLAN' AND owner_id=?", plan.TenantID, plan.ID).Count(&references).Error; err != nil {
		t.Fatal(err)
	}
	if references != 2 {
		t.Fatalf("plan references=%d, want one for each of origin and current preparations", references)
	}
}

func TestDBPlanWriterRecordsG1PreparationRolesIdempotently(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS analysis").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE analysis.video_plan_preparations (tenant_id text NOT NULL, plan_id text NOT NULL, preparation_id text NOT NULL, role text NOT NULL, created_at datetime, PRIMARY KEY(tenant_id,plan_id,preparation_id))").Error; err != nil {
		t.Fatal(err)
	}
	tenant, planID := identity.NewID(), identity.NewID()
	current, origin := identity.NewID(), identity.NewID()
	plan := Plan{ID: planID, TenantID: tenant, CurrentPreparationID: current, OriginPreparationID: &origin}
	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		if err := db.Transaction(func(tx *gorm.DB) error { return persistVideoPlanPreparations(tx, plan, now) }); err != nil {
			t.Fatal(err)
		}
	}
	var rows []database.VideoPlanPreparation
	if err := db.Where("tenant_id=? AND plan_id=?", tenant, planID).Order("role").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Role != "MAIN" || rows[1].Role != "ORIGIN" {
		t.Fatalf("G1 plan preparations=%+v, want MAIN and ORIGIN", rows)
	}
	plan.SupplementPreparationID = &origin
	if err := db.Transaction(func(tx *gorm.DB) error { return persistVideoPlanPreparations(tx, plan, now) }); err == nil {
		t.Fatal("one preparation accepted with conflicting MAIN, SUPPLEMENT and ORIGIN roles")
	}
}
