//go:build integration

package schedule_video_plan

import (
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestVV_IT_589CrossTenantFrameAndPlanReferencesAreRejected(t *testing.T) {
	tenantA := newTask04PlanFixture(t, false, true)
	planA := runTask04Plan(t, tenantA)
	tenantB := newTask04PlanFixture(t, false, true)
	var itemA database.VideoPlanItem
	if err := tenantA.h.AdminDB.Where("tenant_id=? AND plan_id=?", tenantA.tenantID, planA.ID).Take(&itemA).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cases := map[string]any{
		"plan item frame to tenant B frame":       &database.VideoPlanItemFrame{ID: identity.NewID(), TenantID: tenantA.tenantID, PlanItemID: itemA.ID, FrameID: tenantB.currentFrame, Role: "CURRENT", CreatedAt: now},
		"evidence reference to tenant B bank":     &database.VideoEvidenceReference{ID: identity.NewID(), TenantID: tenantA.tenantID, PreparationID: tenantB.currentPreparation, OwnerType: "VIDEO_PLAN", OwnerID: planA.ID, CreatedAt: now},
		"plan of tenant A on tenant B inspection": &database.VideoPlan{ID: identity.NewID(), TenantID: tenantA.tenantID, InspectionID: tenantB.inspectionID, SubmissionID: tenantA.submissionID, CurrentPreparationID: tenantA.currentPreparation, Generation: 9, PlanDigest: strings.Repeat("a", 64), Status: "SEALED", NextAttemptAt: now, CreatedAt: now},
	}
	for name, row := range cases {
		t.Run(name+"/composite foreign key", func(t *testing.T) {
			err := tenantA.h.AdminDB.Transaction(func(tx *gorm.DB) error { return tx.Create(row).Error })
			if err == nil || !strings.Contains(err.Error(), "23503") {
				t.Fatalf("cross-tenant reference error = %v, want foreign key violation", err)
			}
		})
	}
	t.Run("runtime role cannot write a tenant B row under tenant A context", func(t *testing.T) {
		row := database.VideoEvidenceReference{ID: identity.NewID(), TenantID: tenantB.tenantID, PreparationID: tenantB.currentPreparation, OwnerType: "VIDEO_PLAN", OwnerID: planA.ID, CreatedAt: now}
		err := tenantA.h.WithinTenant(tenantA.ctx, tenantA.tenantID, func(tx *gorm.DB) error { return tx.Create(&row).Error })
		if err == nil || !strings.Contains(err.Error(), "row-level security") {
			t.Fatalf("RLS cross-tenant write error = %v, want row-level security rejection", err)
		}
	})
	var leaked int64
	if err := tenantA.h.AdminDB.Model(&database.VideoPlanItemFrame{}).Where("tenant_id=? AND frame_id=?", tenantA.tenantID, tenantB.currentFrame).Count(&leaked).Error; err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatalf("cross-tenant membership persisted: %d", leaked)
	}
}
