//go:build integration

package promote_inspection_to_origin

import (
	"bytes"
	"fmt"
	"io"
	"testing"
	"time"

	"inspection/libs/identity"
	inventoryexecutor "inspection/services/inspection/internal/features/analysis/inventory/core"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	processinventory "inspection/services/inspection/internal/features/analysis/process_inventory_job"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"

	"gorm.io/gorm"
)

func TestRequestReplacementCancelsRunningJobAndExecutorLateWriteIsFenced(t *testing.T) {
	p := newPromotionFixtureWithOrigin(t, "COMPLETED")
	var oldDraft database.OriginVersion
	var job database.InventoryJob
	requirementKey := "env:new:" + p.f.EnvironmentIdentityID.String()
	displayObjectKey := "tenant/" + p.f.TenantID.String() + "/display/" + identity.NewID().String()
	var promptSnapshotID identity.ID
	var promptSnapshot database.AnalysisPromptSnapshot
	if err := p.h.AdminDB.Where("task=?", "INVENTORY_CATALOG").Order("created_at DESC").Take(&promptSnapshot).Error; err != nil {
		t.Fatal(err)
	}
	promptSnapshotID = promptSnapshot.ID
	var sourceMedia database.MediaObject
	if err := p.h.AdminDB.Where("tenant_id=? AND id=?", p.f.TenantID, p.f.MediaID).Take(&sourceMedia).Error; err != nil {
		t.Fatal(err)
	}
	original, err := p.h.Store.Client.Get(p.ctx, p.h.Store.Bucket, sourceMedia.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	imageBytes, err := io.ReadAll(original)
	_ = original.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.h.Store.Client.Put(p.ctx, p.h.Store.Bucket, displayObjectKey, bytes.NewReader(imageBytes), int64(len(imageBytes)), sourceMedia.ContentType); err != nil {
		t.Fatal(err)
	}
	if err := p.h.WithinTenant(p.ctx, p.f.TenantID, func(tx *gorm.DB) error {
		var err error
		oldDraft, err = p.inventory.CreateDraft(tx, inventorycore.DraftInput{TenantID: p.f.TenantID, AssetID: p.f.AssetID, Source: "CONTESTATION", SourceInspectionID: p.f.InspectionID, SourceVersionID: p.activeID, ActorMembershipID: p.f.MembershipID})
		if err != nil {
			return err
		}
		environmentID := identity.NewID()
		if err := tx.Create(&database.InventoryEnvironment{ID: environmentID, TenantID: p.f.TenantID, OriginVersionID: oldDraft.ID, IdentityID: p.f.EnvironmentIdentityID, TypeCode: "LIVING_ROOM", Name: "Sala", Expected: true, Decision: "PENDING", RequirementKey: &requirementKey, Position: 0, Version: 1, CreatedAt: p.now, UpdatedAt: p.now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaDerivative{ID: identity.NewID(), TenantID: p.f.TenantID, MediaID: p.f.MediaID, ObjectKey: displayObjectKey, Kind: "DISPLAY", SHA256: sourceMedia.SHA256, ContentType: sourceMedia.ContentType, Width: 1, Height: 1, SizeBytes: int64(len(imageBytes)), CreatedAt: p.now}).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var originalItems int64
	if err := p.h.AdminDB.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=?", p.f.TenantID, oldDraft.ID).Count(&originalItems).Error; err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	gateway := &inventorytest.Gateway{Respond: func(_ llm.StructuredRequest) (string, error) {
		started <- struct{}{}
		<-release
		return fmt.Sprintf(`{"environments":[{"typeCode":"LIVING_ROOM","name":"Sala","items":[{"typeCode":"SOFA","name":"Sofá","condition":"GOOD","observation":"OBSERVED","confidenceBps":9000,"evidence":[{"mediaId":%q}]}]}]}`, p.f.MediaID.String()), nil
	}}
	processor, err := processinventory.Setup(processinventory.Dependencies{DB: p.h.DB, Store: p.h.Store, Gateway: gateway, Now: func() time.Time { return p.now }, Within: p.h.WithinTenant})
	if err != nil {
		t.Fatal(err)
	}
	executor := &inventoryexecutor.Executor{DB: p.h.DB, ClaimDB: p.h.AdminDB, Processor: processor, Now: func() time.Time { return p.now }, HeartbeatInterval: time.Hour}
	job = database.InventoryJob{ID: identity.NewID(), TenantID: p.f.TenantID, InspectionID: p.f.InspectionID, OriginVersionID: oldDraft.ID, Kind: "CATALOG", RequirementKey: requirementKey, Generation: 1, EnvironmentIdentityID: &p.f.EnvironmentIdentityID, Status: "PENDING", NextAttemptAt: p.now.Add(-time.Hour), PromptSnapshotID: &promptSnapshotID, CreatedAt: p.now, UpdatedAt: p.now}
	if err := p.h.WithinTenant(p.ctx, p.f.TenantID, func(tx *gorm.DB) error { return tx.Create(&job).Error }); err != nil {
		t.Fatal(err)
	}
	executorDone := make(chan error, 1)
	go func() {
		_, runErr := executor.RunDue(p.ctx)
		executorDone <- runErr
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		var current database.InventoryJob
		_ = p.h.AdminDB.Where("tenant_id=? AND id=?", p.f.TenantID, job.ID).Take(&current).Error
		select {
		case runErr := <-executorDone:
			t.Fatalf("catalog executor returned before provider call (err=%v job=%+v)", runErr, current)
		default:
		}
		close(release)
		t.Fatalf("catalog executor did not reach the blocked provider call (job=%+v)", current)
	}
	view := p.status()
	if view.ConflictingDraft == nil || view.ConflictingDraft.ID != oldDraft.ID || view.ConflictingDraft.OwnInspection {
		close(release)
		t.Fatalf("request fixture did not expose another draft: %+v", view.ConflictingDraft)
	}
	requestRaw, err := p.bus.Send(p.ctx, Command{TenantID: p.f.TenantID, InspectionID: p.f.InspectionID, ClientMutationID: "replace-running-worker", Purpose: "INVENTORY_UPDATE", ReplaceDraftID: &oldDraft.ID, ConfirmationDigest: view.ConfirmationDigest})
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	promotion := requestRaw.(Result).Promotion
	var canceledBeforeRelease database.InventoryJob
	if err := p.h.AdminDB.Where("tenant_id=? AND id=?", p.f.TenantID, job.ID).Take(&canceledBeforeRelease).Error; err != nil {
		close(release)
		t.Fatal(err)
	}
	if canceledBeforeRelease.Status != "CANCELED" || canceledBeforeRelease.LeaseToken != nil {
		close(release)
		t.Fatalf("request did not cancel the claimed job before worker release: %+v", canceledBeforeRelease)
	}
	close(release)
	if err := <-executorDone; err != nil {
		t.Fatal(err)
	}
	var storedDraft database.OriginVersion
	var storedJob database.InventoryJob
	if err := p.h.AdminDB.Where("tenant_id=? AND id=?", p.f.TenantID, oldDraft.ID).Take(&storedDraft).Error; err != nil {
		t.Fatal(err)
	}
	if err := p.h.AdminDB.Where("tenant_id=? AND id=?", p.f.TenantID, job.ID).Take(&storedJob).Error; err != nil {
		t.Fatal(err)
	}
	var lateItems, newDraftItems int64
	var writtenItems []database.InventoryItem
	if err := p.h.AdminDB.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=?", p.f.TenantID, oldDraft.ID).Count(&lateItems).Error; err != nil {
		t.Fatal(err)
	}
	if err := p.h.AdminDB.Where("tenant_id=? AND origin_version_id=?", p.f.TenantID, oldDraft.ID).Find(&writtenItems).Error; err != nil {
		t.Fatal(err)
	}
	if err := p.h.AdminDB.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=?", p.f.TenantID, *promotion.DraftVersionID).Count(&newDraftItems).Error; err != nil {
		t.Fatal(err)
	}
	// CONTESTATION drafts copy the active inventory, so the stale result must
	// leave their item count unchanged from the value captured before execution.
	if storedDraft.Status != "DISCARDED" || storedJob.Status != "CANCELED" || storedJob.LeaseToken != nil || storedJob.LeaseUntil != nil || lateItems != originalItems || newDraftItems != 0 {
		t.Fatalf("replacement did not fence the late executor write: draft=%+v job=%+v originalItems=%d lateItems=%d items=%+v newDraftItems=%d", storedDraft, storedJob, originalItems, lateItems, writtenItems, newDraftItems)
	}
}
