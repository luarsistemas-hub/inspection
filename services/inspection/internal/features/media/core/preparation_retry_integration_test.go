//go:build integration

package core

import (
	"context"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestResumeBlockedPlanReplacesEnvironmentLinkWithRuntimeRole(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "PROCESSING_FAILED"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	submissionID := identity.NewID()
	if err := h.AdminDB.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: fixture.DraftID, ResponsibilityID: fixture.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: []byte(`{}`), PayloadDigest: "retry-fixture", SubmittedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	mainPreparationID, failedPreparationID, retriedPreparationID := identity.NewID(), identity.NewID(), identity.NewID()
	mainKey, bathroomKey := "env:kitchen", "env:bathroom"
	for _, prep := range []database.VideoPreparationJob{
		{ID: mainPreparationID, TenantID: fixture.TenantID, MediaID: fixture.MediaID, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, PendingObjectKeys: []byte(`[]`), CreatedAt: now},
		{ID: failedPreparationID, TenantID: fixture.TenantID, MediaID: fixture.MediaID, SubmissionID: &submissionID, Status: "FAILED", Phase: "FAILED", PreparationVersion: 1, Generation: 2, NextAttemptAt: now, PendingObjectKeys: []byte(`[]`), CreatedAt: now.Add(time.Second)},
		{ID: retriedPreparationID, TenantID: fixture.TenantID, MediaID: fixture.MediaID, SubmissionID: &submissionID, Status: "QUEUED", Phase: "RECEIVED", PreparationVersion: 2, Generation: 3, NextAttemptAt: now, PendingObjectKeys: []byte(`[]`), CreatedAt: now.Add(2 * time.Second)},
	} {
		if err := h.AdminDB.Create(&prep).Error; err != nil {
			t.Fatal(err)
		}
	}
	plan := database.VideoPlan{ID: identity.NewID(), TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, SubmissionID: submissionID, CurrentPreparationID: mainPreparationID, Generation: 1, Status: "BLOCKED", Attempts: 4, NextAttemptAt: now, ErrorCode: "PREPARATION_FAILED", CreatedAt: now}
	if err := h.AdminDB.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = h.AdminDB.Model(&database.VideoPlan{}).Where("tenant_id=? AND id=?", fixture.TenantID, plan.ID).Update("status", "FAILED").Error
	})
	for _, link := range []database.VideoPlanPreparation{
		{TenantID: fixture.TenantID, PlanID: plan.ID, PreparationID: mainPreparationID, Role: "MAIN", RequirementKey: &mainKey, CreatedAt: now},
		{TenantID: fixture.TenantID, PlanID: plan.ID, PreparationID: failedPreparationID, Role: "SUPPLEMENT", RequirementKey: &bathroomKey, CreatedAt: now},
	} {
		if err := h.AdminDB.Create(&link).Error; err != nil {
			t.Fatal(err)
		}
	}
	var currentUser string
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Raw("SELECT current_user").Scan(&currentUser).Error; err != nil {
			return err
		}
		return ResumeBlockedPlans(tx, fixture.TenantID, failedPreparationID, retriedPreparationID, now.Add(time.Minute))
	}); err != nil {
		t.Fatalf("resume blocked plan using runtime connection: %v", err)
	}
	if currentUser != "inspection_runtime" {
		t.Fatalf("test connection role=%q, want inspection_runtime", currentUser)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var got database.VideoPlan
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, plan.ID).Take(&got).Error; err != nil {
			return err
		}
		var links []database.VideoPlanPreparation
		if err := tx.Where("tenant_id=? AND plan_id=?", fixture.TenantID, plan.ID).Order("role").Find(&links).Error; err != nil {
			return err
		}
		if got.Status != "BUILDING" || got.CurrentPreparationID != mainPreparationID || len(links) != 2 {
			t.Errorf("resumed plan=%+v links=%+v", got, links)
			return nil
		}
		var supplement *database.VideoPlanPreparation
		for i := range links {
			if links[i].Role == "SUPPLEMENT" {
				supplement = &links[i]
			}
		}
		if supplement == nil || supplement.PreparationID != retriedPreparationID || supplement.RequirementKey == nil || *supplement.RequirementKey != bathroomKey {
			t.Errorf("supplement link did not preserve role/key with replacement preparation: %+v", links)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
