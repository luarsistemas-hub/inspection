//go:build integration

package process_inventory_job

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
)

func TestTask06SubmittedRecaptureDoesNotReplaceInspectionScopeRequirement(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	seed, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	requirementKey := "env:" + identity.NewID().String()
	inspectionRequirement := capturecore.Requirement{Key: requirementKey, EnvironmentIdentityID: requirementKey[len("env:"):], EnvironmentTypeCode: "BALCONY", EnvironmentName: "Varanda", EvidenceKind: "PHOTO", Expected: true}
	recaptureRequirement := capturecore.Requirement{Key: "env:focused", EnvironmentIdentityID: "focused", EnvironmentTypeCode: "BALCONY", EnvironmentName: "Varanda", EvidenceKind: "PHOTO", Expected: true}
	inspectionPayload, _ := json.Marshal([]capturecore.Requirement{inspectionRequirement})
	recapturePayload, _ := json.Marshal([]capturecore.Requirement{recaptureRequirement})
	var inspectionDraft database.CaptureDraft
	var responsibility database.Responsibility
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", seed.TenantID, seed.DraftID).Take(&inspectionDraft).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", seed.TenantID, inspectionDraft.ID).Updates(map[string]any{"kind": "INSPECTION", "status": "SUBMITTED", "requirements": inspectionPayload}).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND inspection_id=?", seed.TenantID, seed.InspectionID).Take(&responsibility).Error; err != nil {
			return err
		}
		recaptureResponsibility := database.Responsibility{ID: identity.NewID(), TenantID: seed.TenantID, InspectionID: seed.InspectionID, ParticipantID: responsibility.ParticipantID, Status: "PENDING", Version: 1, CreatedAt: time.Now().UTC().Add(time.Second), UpdatedAt: time.Now().UTC().Add(time.Second)}
		if err := tx.Create(&recaptureResponsibility).Error; err != nil {
			return err
		}
		recapture := database.CaptureDraft{ID: identity.NewID(), TenantID: seed.TenantID, ResponsibilityID: recaptureResponsibility.ID, Kind: "RECAPTURE", MediaKind: "PHOTO", TemplateVersionID: inspectionDraft.TemplateVersionID, ReferencePayload: inspectionDraft.ReferencePayload, PolicyPayload: inspectionDraft.PolicyPayload, Requirements: recapturePayload, Status: "SUBMITTED", Version: 2, CreatedAt: time.Now().UTC().Add(2 * time.Second), UpdatedAt: time.Now().UTC().Add(2 * time.Second)}
		return tx.Create(&recapture).Error
	}); err != nil {
		t.Fatal(err)
	}
	job := database.InventoryJob{TenantID: seed.TenantID, InspectionID: seed.InspectionID, RequirementKey: requirementKey, Kind: "COMPARE"}
	var got *capturecore.Requirement
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		var err error
		got, _, err = loadSubmittedCapture(ctx, tx, job)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Key != inspectionRequirement.Key || got.EnvironmentIdentityID != inspectionRequirement.EnvironmentIdentityID {
		t.Fatalf("submitted capture requirement = %+v; want original inspection scope requirement %+v", got, inspectionRequirement)
	}
}

func TestTask06RealComparisonRecordsMissingAndUnmatchedCaptureReasons(t *testing.T) {
	t.Run("v2 without room answer", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		f.openCapture = true
		baseline := f.pin(strictLiving(inventorytest.BaselineItem{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}))
		created := f.createMoveOut(baseline, catalog.EnvironmentSchemaVersion)
		f.answer(created.roomKey["Sala"], nil, "LOCKED")
		f.completeFindingsComparison()
		f.submitCapture(f.seed.ResponsibilityID)
		var submitted database.CaptureDraft
		var responsibility database.Responsibility
		f.within(func(tx *gorm.DB) error {
			if err := tx.Where("tenant_id=? AND id=?", f.seed.TenantID, f.seed.DraftID).Take(&submitted).Error; err != nil {
				return err
			}
			if err := tx.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, f.seed.InspectionID).Take(&responsibility).Error; err != nil {
				return err
			}
			now := f.now().Add(time.Second)
			recaptureResponsibility := database.Responsibility{ID: identity.NewID(), TenantID: f.seed.TenantID, InspectionID: f.seed.InspectionID, ParticipantID: responsibility.ParticipantID, Status: "SUBMITTED", Version: 2, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&recaptureResponsibility).Error; err != nil {
				return err
			}
			recaptureRequirements, _ := json.Marshal([]capturecore.Requirement{{Key: "env:recapture-only", EnvironmentIdentityID: "recapture-only", EnvironmentTypeCode: "LIVING_ROOM", EnvironmentName: "Sala", EvidenceKind: "PHOTO", Expected: true}})
			return tx.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: f.seed.TenantID, ResponsibilityID: recaptureResponsibility.ID, Kind: "RECAPTURE", MediaKind: "PHOTO", TemplateVersionID: submitted.TemplateVersionID, ReferencePayload: submitted.ReferencePayload, PolicyPayload: submitted.PolicyPayload, Requirements: recaptureRequirements, Status: "SUBMITTED", Version: 2, CreatedAt: now, UpdatedAt: now}).Error
		})
		f.drain()
		row := f.results()[baseline.items["Sofá"].IdentityID.String()]
		if row.Status != "NOT_OBSERVED" || value(row.ReasonCode) != "ENVIRONMENT_NOT_CAPTURED" {
			t.Fatalf("v2 result = %+v; want NOT_OBSERVED/ENVIRONMENT_NOT_CAPTURED", row)
		}
	})

	t.Run("v3 baseline room has no scope match", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		f.openCapture = true
		baseline := f.pin(strictLiving(inventorytest.BaselineItem{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}))
		created := f.createMoveOutFromBaseline(baseline, catalog.ExpectedRoom{Key: "cozinha", TypeCode: "KITCHEN", Name: "Cozinha", Components: []catalog.ExpectedComponent{{Key: "geral", TypeCode: "OTHER", Label: "Visão geral", Views: []string{"wide"}}}})
		f.observeRoom(created.roomKey["Cozinha"])
		f.completeFindingsComparison()
		f.submitCapture(f.seed.ResponsibilityID)
		f.drain()
		row := f.results()[baseline.items["Sofá"].IdentityID.String()]
		if row.Status != "NOT_OBSERVED" || value(row.ReasonCode) != reasonEnvironmentUnmatched {
			t.Fatalf("v3 result = %+v; want NOT_OBSERVED/%s", row, reasonEnvironmentUnmatched)
		}
	})
}
