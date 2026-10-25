//go:build integration

package prepare_video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	inventoryexecutor "inspection/services/inspection/internal/features/analysis/inventory/core"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	processinventoryjob "inspection/services/inspection/internal/features/analysis/process_inventory_job"
	requestinventoryjobs "inspection/services/inspection/internal/features/analysis/request_inventory_jobs"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

// addEnvironmentClip queues one more environment clip on the fixture's
// responsibility, as a second recorded environment would.
func addEnvironmentClip(t *testing.T, h *harness.Harness, fixture preparationFixture, clip []byte, key string) (identity.ID, identity.ID) {
	t.Helper()
	ctx, now := context.Background(), time.Now().UTC().Truncate(time.Microsecond)
	attemptID, mediaID, submissionID, preparationID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	sum := sha256.Sum256(clip)
	digest := hex.EncodeToString(sum[:])
	objectKey := "qa-video-preparation/" + fixture.tenantID.String() + "/" + mediaID.String()
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, objectKey, bytes.NewReader(clip), int64(len(clip)), "video/mp4"); err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var draft database.CaptureDraft
		if err := tx.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Take(&draft).Error; err != nil {
			return err
		}
		requirementKey := key
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.tenantID, ResponsibilityID: fixture.responsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "it011-" + mediaID.String(), PayloadDigest: digest, RequirementKey: &requirementKey, AdmittedAt: now, ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.tenantID, ResponsibilityID: fixture.responsibilityID, ObjectKey: objectKey, ContentType: "video/mp4", SHA256: digest, SizeBytes: int64(len(clip)), Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: key, IdempotencyKey: "it011-media-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fixture.tenantID, DraftID: draft.ID, ResponsibilityID: fixture.responsibilityID, VersionNumber: 2, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: digest, SubmittedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.tenantID, MediaID: mediaID, SubmissionID: &submissionID, RequirementKey: &requirementKey, Status: "QUEUED", Phase: "RECEIVED", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, PendingObjectKeys: []byte("[]"), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	return mediaID, preparationID
}

func TestIT011VideoEnvironmentClipsPrepareWithProfileV2AndCatalogDraft(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	sala, cozinha := "env:"+identity.NewID().String(), "env:"+identity.NewID().String()
	fixture := seedPreparationFixture(t, h, staticH264Clip(t), "video/mp4", "CURRENT", "CAMERA", sala)
	secondMedia, secondPrep := addEnvironmentClip(t, h, fixture, staticH264Clip(t), cozinha)
	deferUnrelatedPreparationJobs(t, h, fixture, time.Now().UTC().Add(time.Hour))
	if err := h.AdminDB.Model(&database.VideoPreparationJob{}).Where("id IN ?", []identity.ID{fixture.preparationID, secondPrep}).Updates(map[string]any{"profile_digest": video.DefaultProfile().Digest(), "next_attempt_at": time.Now().UTC().Add(-time.Second)}).Error; err != nil {
		t.Fatal(err)
	}
	requirements := []capturecore.Requirement{
		{Key: sala, EnvironmentIdentityID: strings.TrimPrefix(sala, "env:"), EnvironmentTypeCode: "LIVING_ROOM", EnvironmentName: "Sala", EvidenceKind: "VIDEO", Required: true, Expected: true, MinimumMedia: 1, MaximumMedia: 3},
		{Key: cozinha, EnvironmentIdentityID: strings.TrimPrefix(cozinha, "env:"), EnvironmentTypeCode: "KITCHEN", EnvironmentName: "Cozinha", EvidenceKind: "VIDEO", Required: true, Expected: true, MinimumMedia: 1, MaximumMedia: 3},
	}
	var inspection database.Inspection
	var catalogPrompt database.AnalysisPromptSnapshot
	if err := h.AdminDB.Where("task='INVENTORY_CATALOG'").Order("created_at DESC").Take(&catalogPrompt).Error; err != nil {
		t.Fatal(err)
	}
	templateVersion := identity.NewID()
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var responsibility database.Responsibility
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.responsibilityID).Take(&responsibility).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, responsibility.InspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		var template database.TemplateVersion
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, inspection.TemplateVersionID).Take(&template).Error; err != nil {
			return err
		}
		template.ID, template.VersionNumber, template.SchemaVersion, template.IdempotencyKey, template.Status = templateVersion, template.VersionNumber+100, 2, "it011-"+templateVersion.String(), "SUPERSEDED"
		if err := tx.Create(&template).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.tenantID, inspection.ID).Updates(map[string]any{"template_version_id": templateVersion, "inventory_prompt_snapshot_id": catalogPrompt.ID, "media_kind": "VIDEO"}).Error; err != nil {
			return err
		}
		raw, _ := json.Marshal(requirements)
		return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Updates(map[string]any{"requirements": raw, "status": "SUBMITTED", "media_kind": "VIDEO"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var draft database.CaptureDraft
		if err := tx.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Take(&draft).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		for key, media := range map[string]identity.ID{sala: fixture.mediaID, cozinha: secondMedia} {
			ids, _ := json.Marshal([]identity.ID{media})
			if err := tx.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: fixture.tenantID, DraftID: draft.ID, RequirementKey: key, MediaIDs: ids, Flags: json.RawMessage(`[]`), Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	request, err := requestinventoryjobs.Setup(requestinventoryjobs.Dependencies{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	deliver := func(eventType string, payload any) {
		t.Helper()
		raw, _ := json.Marshal(payload)
		if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
			return request(ctx, tx, events.RawEnvelope{ID: identity.NewID(), Type: eventType, TenantID: fixture.tenantID, Payload: raw})
		}); err != nil {
			t.Fatal(err)
		}
	}
	countJobs := func() int64 {
		var count int64
		if err := h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND inspection_id=? AND kind='CATALOG'", fixture.tenantID, inspection.ID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		return count
	}
	deliver("capture.video_submitted.v2", events.CaptureVideoSubmittedV2Payload{InspectionID: inspection.ID, ResponsibilityID: fixture.responsibilityID, SubmissionVersionID: identity.NewID()})
	if countJobs() != 0 {
		t.Fatal("CATALOG jobs created before the environment preparations are READY")
	}

	executor := checkpointExecutor(t, h, video.FFmpeg{Threads: 1}, time.Now)
	executor.deps.FreeScratchBytes = func(string) (uint64, error) { return 9 << 30, nil }
	for i := 0; i < 2; i++ {
		if worked, err := executor.RunDue(ctx); err != nil || !worked {
			t.Fatalf("preparation %d: RunDue=%t err=%v", i, worked, err)
		}
	}
	for _, preparation := range []identity.ID{fixture.preparationID, secondPrep} {
		var job database.VideoPreparationJob
		var representative int64
		if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
			if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, preparation).Take(&job).Error; err != nil {
				return err
			}
			return tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=? AND representative", fixture.tenantID, preparation).Count(&representative).Error
		}); err != nil {
			t.Fatal(err)
		}
		if job.Status != "READY" || job.FramesRetained >= job.FramesSampled || representative == 0 || representative > 12 {
			t.Fatalf("preparation %s: status=%s sampled=%d retained=%d representative=%d, want profile v2 selection", preparation, job.Status, job.FramesSampled, job.FramesRetained, representative)
		}
	}
	deliver("media.video_prepared.v1", events.VideoPreparedPayload{MediaID: secondMedia, PreparationID: secondPrep, ManifestDigest: "it011"})
	if countJobs() != 2 {
		t.Fatalf("CATALOG jobs = %d, want one per environment", countJobs())
	}

	gateway := &inventorytest.Gateway{Respond: func(request llm.StructuredRequest) (string, error) {
		return `{"environments":[{"typeCode":"LIVING_ROOM","name":"Ambiente","items":[]}]}`, nil
	}}
	processor, err := processinventoryjob.Setup(processinventoryjob.Dependencies{DB: h.DB, Store: h.Store, Gateway: gateway, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	inventory := &inventoryexecutor.Executor{DB: h.DB, ClaimDB: h.AdminDB, Processor: processor, Now: time.Now, HeartbeatInterval: time.Hour}
	for i := 0; i < 2; i++ {
		if worked, err := inventory.RunDue(ctx); err != nil || !worked {
			t.Fatalf("inventory job %d: RunDue=%t err=%v", i, worked, err)
		}
	}
	var draft database.OriginVersion
	if err := h.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND source_inspection_id=?", fixture.tenantID, inspection.ID).Take(&draft).Error
	}); err != nil {
		t.Fatal(err)
	}
	if draft.DraftState == nil || *draft.DraftState != "READY" {
		t.Fatalf("draft state = %v, want READY", draft.DraftState)
	}
	for _, call := range gateway.Calls("INVENTORY_CATALOG") {
		if len(call.Images) == 0 || len(call.Images) > 12 || call.Images[0].Source != "VIDEO_FRAME" {
			t.Fatalf("catalog request images = %d (%v), want 1..12 representative frames", len(call.Images), call.Images)
		}
	}
}
