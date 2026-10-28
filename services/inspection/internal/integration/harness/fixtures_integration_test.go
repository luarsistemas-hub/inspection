//go:build integration

package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"testing"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/features/templates/catalog"
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

	// v1 pinned until template schema 1 is removed
	fixture, err := h.SeedTask06Fixture(ctx, Task06FixtureOptions{SchemaVersion: 1, RequirementKey: "room-1"})
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

func TestTask06FixtureDefaultsToEnvironmentCaptureAndSeedsEnvironmentFixtures(t *testing.T) {
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
	if fixture.EnvironmentIdentityID == (identity.ID{}) {
		t.Fatal("default environment identity is empty")
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var version database.TemplateVersion
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, fixture.TemplateVersionID).Take(&version).Error; err != nil {
			return err
		}
		var document catalog.TemplateDocument
		if err := json.Unmarshal(version.DefinitionJSON, &document); err != nil {
			return err
		}
		if version.SchemaVersion != catalog.EnvironmentSchemaVersion || document.CaptureLayout != catalog.EnvironmentsLayout || document.EnvironmentCapture == nil {
			t.Fatalf("default template is not canonical environment schema 2: version=%d document=%+v", version.SchemaVersion, document)
		}
		var draft database.CaptureDraft
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Take(&draft).Error; err != nil {
			return err
		}
		var requirements []capturecore.Requirement
		if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
			return err
		}
		wantKey := "env:new:" + fixture.EnvironmentIdentityID.String()
		if len(requirements) != 1 || requirements[0].Key != wantKey || requirements[0].EnvironmentIdentityID != fixture.EnvironmentIdentityID.String() || requirements[0].EnvironmentTypeCode != "LIVING_ROOM" {
			t.Fatalf("unexpected default environment requirement: %+v", requirements)
		}
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		if inspection.InventoryPromptSnapshotID != nil {
			t.Fatalf("FINDINGS fixture unexpectedly has inventory prompt snapshot: %v", inspection.InventoryPromptSnapshotID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	baseline, err := h.SeedInventoryBaseline(ctx, Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.EnvironmentIdentityIDs) != 1 || len(baseline.ItemIDs) != 1 || len(baseline.EvidenceIDs) != 1 {
		t.Fatalf("inventory baseline is incomplete: %+v", baseline)
	}
	if err := h.WithinTenant(ctx, baseline.TenantID, func(tx *gorm.DB) error {
		var version database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", baseline.TenantID, baseline.OriginVersionID).Take(&version).Error; err != nil {
			return err
		}
		if version.Status != "ACTIVE" || !version.HasInventory || version.Source != "CAPTURE" || version.Purpose == nil || *version.Purpose != "MOVE_IN" {
			t.Fatalf("unexpected inventory baseline version: %+v", version)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	videoFixture, err := h.SeedVideoEnvironmentFixture(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, videoFixture.TenantID, func(tx *gorm.DB) error {
		var attempt database.VideoAttempt
		if err := tx.Where("tenant_id=? AND id=?", videoFixture.TenantID, videoFixture.VideoAttemptID).Take(&attempt).Error; err != nil {
			return err
		}
		var preparation database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", videoFixture.TenantID, videoFixture.PreparationID).Take(&preparation).Error; err != nil {
			return err
		}
		var frame database.VideoFrame
		if err := tx.Where("tenant_id=? AND id=?", videoFixture.TenantID, videoFixture.FrameID).Take(&frame).Error; err != nil {
			return err
		}
		var scope database.VideoScopeItem
		if err := tx.Where("tenant_id=? AND id=?", videoFixture.TenantID, videoFixture.ScopeItemID).Take(&scope).Error; err != nil {
			return err
		}
		key := "env:new:" + videoFixture.EnvironmentIdentityID.String()
		if attempt.RequirementKey == nil || *attempt.RequirementKey != key || preparation.Status != "READY" || preparation.RequirementKey == nil || *preparation.RequirementKey != key || frame.ObjectKey == "" || scope.FrameID != frame.ID {
			t.Fatalf("unexpected environment video fixture: attempt=%+v preparation=%+v frame=%+v scope=%+v", attempt, preparation, frame, scope)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSeedVideoOriginFixturePersistsReadyActiveFrameScope(t *testing.T) {
	ctx := context.Background()
	h, err := NewFromEnv(ctx)
	if err != nil {
		t.Skipf("integration dependencies unavailable: %v", err)
	}
	defer h.Close()
	f, err := h.SeedVideoOriginFixture(ctx, VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		var draft database.CaptureDraft
		if err := tx.Where("tenant_id=? AND responsibility_id=?", f.TenantID, f.ResponsibilityID).Take(&draft).Error; err != nil {
			return err
		}
		if draft.Kind != "INSPECTION" {
			t.Fatalf("video source fixture capture draft kind=%q; want INSPECTION", draft.Kind)
		}
		var origin database.Origin
		if err := tx.First(&origin, "id = ?", f.OriginID).Error; err != nil {
			return err
		}
		if origin.ActiveVersionID == nil || *origin.ActiveVersionID != f.OriginVersionID {
			t.Fatalf("origin not active: %+v", origin)
		}
		var version database.OriginVersion
		if err := tx.First(&version, "id = ?", f.OriginVersionID).Error; err != nil {
			return err
		}
		if version.Status != "ACTIVE" || version.MediaKind != "VIDEO" || version.VideoPreparationID == nil || *version.VideoPreparationID != f.PreparationID {
			t.Fatalf("unexpected origin version: %+v", version)
		}
		var preparation database.VideoPreparationJob
		if err := tx.First(&preparation, "id = ?", f.PreparationID).Error; err != nil {
			return err
		}
		if preparation.Status != "READY" || preparation.ManifestDigest == "" {
			t.Fatalf("unexpected preparation: %+v", preparation)
		}
		var frame database.VideoFrame
		if err := tx.First(&frame, "id = ?", f.FrameID).Error; err != nil {
			return err
		}
		var scope int64
		if err := tx.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND preparation_id=? AND frame_id=?", f.TenantID, f.PreparationID, f.FrameID).Count(&scope).Error; err != nil {
			return err
		}
		if scope != 1 {
			t.Fatalf("scope rows=%d, want 1", scope)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var frame database.VideoFrame
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error { return tx.First(&frame, "id = ?", f.FrameID).Error }); err != nil {
		t.Fatal(err)
	}
	object, err := h.Store.Client.Get(ctx, h.Store.Bucket, frame.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	defer object.Close()
	data, err := io.ReadAll(object)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != frame.SHA256 {
		t.Fatalf("MinIO frame digest mismatch: %s", frame.ObjectKey)
	}
}
