package request_inventory_jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

func TestSetupRejectsMalformedAndUnsupportedInventoryEvents(t *testing.T) {
	handler, err := Setup(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	for _, envelope := range []events.RawEnvelope{
		{Type: "capture.submitted.v1", Payload: json.RawMessage(`{`), TenantID: identity.NewID()},
		{Type: "inventory.job_requested.v1", Payload: json.RawMessage(`{"jobId":"x"}`), TenantID: identity.NewID()},
		{Type: "inventory.unknown.v1", Payload: json.RawMessage(`{}`), TenantID: identity.NewID()},
	} {
		if err := handler(context.Background(), &gorm.DB{}, envelope); !errors.Is(err, messaging.ErrPermanent) {
			t.Fatalf("event %s error=%v, want permanent", envelope.Type, err)
		}
	}
}

func TestRequestStopsOnCanceledContextBeforeDatabaseAccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Request(ctx, nil, identity.NewID(), identity.NewID(), time.Now().UTC()); !errors.Is(err, context.Canceled) {
		t.Fatalf("request error=%v, want context canceled", err)
	}
}

func TestUT180SubmittedPhotoCatalogCreatesDraftAndOneJobPerCapturedEnvironment(t *testing.T) {
	seed := inventorytest.New(t, inventorytest.InspectionOptions{Requirements: []capturecore.Requirement{
		inventorytest.EnvironmentRequirement("env:new:"+identity.NewID().String(), "LIVING_ROOM", "Sala", true),
		inventorytest.EnvironmentRequirement("env:new:"+identity.NewID().String(), "KITCHEN", "Cozinha", true),
		inventorytest.EnvironmentRequirement("env:new:"+identity.NewID().String(), "GARAGE", "Garagem", true),
	}})
	keys := requirementKeys(t, seed)
	seed.Answer(t, keys[0], []identity.ID{seed.Photo(t, keys[0])}, "")
	seed.Answer(t, keys[1], []identity.ID{seed.Photo(t, keys[1])}, "")
	seed.Answer(t, keys[2], nil, "LOCKED")
	deliver(t, seed, "capture.submitted.v1")

	var versions []database.OriginVersion
	if err := seed.DB.Find(&versions).Error; err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Source != "CAPTURE" || !versions[0].HasInventory || versions[0].DraftState == nil || *versions[0].DraftState != "PREPARING" || versions[0].SourceInspectionID == nil || *versions[0].SourceInspectionID != seed.Inspection {
		t.Fatalf("draft = %+v", versions)
	}
	jobs := jobsOf(t, seed)
	if len(jobs) != 2 {
		t.Fatalf("jobs = %d, want one CATALOG job per captured environment", len(jobs))
	}
	for _, job := range jobs {
		if job.Kind != "CATALOG" || job.OriginVersionID != versions[0].ID || job.RequirementKey == keys[2] || job.EnvironmentIdentityID == nil {
			t.Fatalf("unexpected job %+v", job)
		}
	}
	var environments int64
	seed.DB.Model(&database.InventoryEnvironment{}).Where("origin_version_id=?", versions[0].ID).Count(&environments)
	if environments != 2 {
		t.Fatalf("draft environments = %d, want 2 (impossible environment skipped)", environments)
	}
}

func TestUT181RedeliveredSubmissionIsIdempotent(t *testing.T) {
	seed := inventorytest.New(t, inventorytest.InspectionOptions{Requirements: []capturecore.Requirement{
		inventorytest.EnvironmentRequirement("env:new:"+identity.NewID().String(), "BEDROOM", "Quarto", true),
	}})
	key := requirementKeys(t, seed)[0]
	seed.Answer(t, key, []identity.ID{seed.Photo(t, key)}, "")
	deliver(t, seed, "capture.submitted.v1")
	deliver(t, seed, "capture.submitted.v1")
	var versions, environments int64
	seed.DB.Model(&database.OriginVersion{}).Count(&versions)
	seed.DB.Model(&database.InventoryEnvironment{}).Count(&environments)
	if versions != 1 || environments != 1 || len(jobsOf(t, seed)) != 1 {
		t.Fatalf("versions=%d environments=%d jobs=%d, want 1/1/1", versions, environments, len(jobsOf(t, seed)))
	}
}

func TestUT182PinnedInventoryCreatesCompareJobsForBaselineAndAddedEnvironments(t *testing.T) {
	addedKey := "env:new:" + identity.NewID().String()
	seed := inventorytest.New(t, inventorytest.InspectionOptions{})
	baseline := seed.PinBaseline(t,
		inventorytest.BaselineEnvironment{Name: "Sala", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}}},
		inventorytest.BaselineEnvironment{Name: "Cozinha", TypeCode: "KITCHEN", Items: []inventorytest.BaselineItem{{Name: "Pia", TypeCode: "SINK", Condition: "GOOD", Evidence: 1}}},
	)
	setRequirements(t, seed, []capturecore.Requirement{
		inventorytest.EnvironmentRequirement(inventorytest.EnvironmentKey(baseline.Environments[0]), "LIVING_ROOM", "Sala", false),
		inventorytest.EnvironmentRequirement(inventorytest.EnvironmentKey(baseline.Environments[1]), "KITCHEN", "Cozinha", false),
		inventorytest.EnvironmentRequirement(addedKey, "OFFICE", "Escritório", true),
	})
	seed.Answer(t, inventorytest.EnvironmentKey(baseline.Environments[0]), []identity.ID{seed.Photo(t, "a")}, "")
	seed.Answer(t, inventorytest.EnvironmentKey(baseline.Environments[1]), nil, "NO_ACCESS")
	seed.Answer(t, addedKey, []identity.ID{seed.Photo(t, addedKey)}, "")
	var before int64
	seed.DB.Model(&database.InventoryEnvironment{}).Where("origin_version_id=?", baseline.Version).Count(&before)
	deliver(t, seed, "capture.submitted.v1")

	jobs := jobsOf(t, seed)
	if len(jobs) != 3 {
		t.Fatalf("jobs = %d, want 2 baseline (one impossible) + 1 added", len(jobs))
	}
	byKey := map[string]database.InventoryJob{}
	for _, job := range jobs {
		if job.Kind != "COMPARE" || job.OriginVersionID != baseline.Version || job.PromptSnapshotID == nil || *job.PromptSnapshotID != seed.ComparePrompt {
			t.Fatalf("unexpected job %+v", job)
		}
		byKey[job.RequirementKey] = job
	}
	for _, env := range baseline.Environments {
		job, ok := byKey[inventorytest.EnvironmentKey(env)]
		if !ok || *job.EnvironmentIdentityID != env.IdentityID {
			t.Fatalf("baseline environment %s has no COMPARE job keyed by its identity", env.Name)
		}
	}
	if _, ok := byKey[addedKey]; !ok {
		t.Fatal("environment added during capture has no COMPARE job")
	}
	var after int64
	seed.DB.Model(&database.InventoryEnvironment{}).Where("origin_version_id=?", baseline.Version).Count(&after)
	if after != before {
		t.Fatalf("pinned version environments changed from %d to %d", before, after)
	}
}

func TestUT183LegacyReferenceCreatesNoInventoryJobs(t *testing.T) {
	seed := inventorytest.New(t, inventorytest.InspectionOptions{Requirements: []capturecore.Requirement{inventorytest.EnvironmentRequirement("env:new:"+identity.NewID().String(), "BEDROOM", "Quarto", true)}})
	key := requirementKeys(t, seed)[0]
	seed.Answer(t, key, []identity.ID{seed.Photo(t, key)}, "")
	legacy := identity.NewID()
	origin := database.Origin{ID: identity.NewID(), TenantID: seed.Tenant, AssetID: seed.Asset, ActiveVersionID: &legacy, Version: 1, CreatedAt: seed.Now, UpdatedAt: seed.Now}
	mustCreate(t, seed.DB, &origin)
	mustCreate(t, seed.DB, &database.OriginVersion{ID: legacy, TenantID: seed.Tenant, OriginID: origin.ID, VersionNumber: 1, Source: "CAPTURE", Status: "ACTIVE", IdempotencyKey: "legacy", CreatedAt: seed.Now})
	if err := seed.DB.Model(&database.ReferenceSnapshot{}).Where("inspection_id=?", seed.Inspection).Update("reference_version_id", legacy).Error; err != nil {
		t.Fatal(err)
	}
	deliver(t, seed, "capture.submitted.v1")
	var drafts int64
	seed.DB.Model(&database.OriginVersion{}).Where("status='DRAFT'").Count(&drafts)
	if len(jobsOf(t, seed)) != 0 || drafts != 0 {
		t.Fatalf("legacy reference produced jobs=%d drafts=%d", len(jobsOf(t, seed)), drafts)
	}
}

func TestUT184V1InspectionCreatesNoInventoryJobs(t *testing.T) {
	for name, options := range map[string]inventorytest.InspectionOptions{
		"schema v1":           {SchemaVersion: 1},
		"no inventory prompt": {NoInventoryPrompt: true},
	} {
		t.Run(name, func(t *testing.T) {
			options.Requirements = []capturecore.Requirement{inventorytest.EnvironmentRequirement("env:new:"+identity.NewID().String(), "BEDROOM", "Quarto", true)}
			seed := inventorytest.New(t, options)
			key := requirementKeys(t, seed)[0]
			seed.Answer(t, key, []identity.ID{seed.Photo(t, key)}, "")
			deliver(t, seed, "capture.submitted.v1")
			if len(jobsOf(t, seed)) != 0 {
				t.Fatal("v1 inspection produced inventory jobs")
			}
		})
	}
}

func TestUT185VideoJobsWaitForEveryEnvironmentPreparation(t *testing.T) {
	seed := inventorytest.New(t, inventorytest.InspectionOptions{MediaKind: "VIDEO", Requirements: []capturecore.Requirement{
		inventorytest.EnvironmentRequirement("env:new:"+identity.NewID().String(), "LIVING_ROOM", "Sala", true),
		inventorytest.EnvironmentRequirement("env:new:"+identity.NewID().String(), "KITCHEN", "Cozinha", true),
	}})
	keys := requirementKeys(t, seed)
	first, _, _ := seed.Clip(t, keys[0], "READY", 3)
	second, secondPrep, _ := seed.Clip(t, keys[1], "PROCESSING", 0)
	seed.Answer(t, keys[0], []identity.ID{first}, "")
	seed.Answer(t, keys[1], []identity.ID{second}, "")
	deliver(t, seed, "capture.video_submitted.v2")
	if len(jobsOf(t, seed)) != 0 {
		t.Fatal("jobs created while one environment preparation is pending")
	}
	if err := seed.DB.Model(&database.VideoPreparationJob{}).Where("id=?", secondPrep).Update("status", "READY").Error; err != nil {
		t.Fatal(err)
	}
	deliverPayload(t, seed, "media.video_prepared.v1", events.VideoPreparedPayload{MediaID: second, PreparationID: secondPrep, ManifestDigest: "digest"})
	if len(jobsOf(t, seed)) != 2 {
		t.Fatalf("jobs = %d after every preparation is READY, want 2", len(jobsOf(t, seed)))
	}
}

func TestVideoFailedPreparationIsTerminalForTheGate(t *testing.T) {
	seed := inventorytest.New(t, inventorytest.InspectionOptions{MediaKind: "VIDEO", Requirements: []capturecore.Requirement{
		inventorytest.EnvironmentRequirement("env:new:"+identity.NewID().String(), "LIVING_ROOM", "Sala", true),
		inventorytest.EnvironmentRequirement("env:new:"+identity.NewID().String(), "KITCHEN", "Cozinha", true),
	}})
	keys := requirementKeys(t, seed)
	first, _, _ := seed.Clip(t, keys[0], "READY", 3)
	second, secondPrep, _ := seed.Clip(t, keys[1], "FAILED", 0)
	seed.Answer(t, keys[0], []identity.ID{first}, "")
	seed.Answer(t, keys[1], []identity.ID{second}, "")
	deliverPayload(t, seed, "inventory.job_requested.v1", events.InventoryJobRequestedPayload{JobID: secondPrep, InspectionID: seed.Inspection})
	if len(jobsOf(t, seed)) != 2 {
		t.Fatalf("jobs = %d, a FAILED clip must not hold the draft back", len(jobsOf(t, seed)))
	}
}

func TestCatalogWithoutCapturedEnvironmentsIsReadyAtOnce(t *testing.T) {
	seed := inventorytest.New(t, inventorytest.InspectionOptions{Requirements: []capturecore.Requirement{inventorytest.EnvironmentRequirement("env:new:"+identity.NewID().String(), "GARAGE", "Garagem", true)}})
	seed.Answer(t, requirementKeys(t, seed)[0], nil, "LOCKED")
	deliver(t, seed, "capture.submitted.v1")
	var version database.OriginVersion
	if err := seed.DB.Take(&version).Error; err != nil {
		t.Fatal(err)
	}
	if version.DraftState == nil || *version.DraftState != "READY" {
		t.Fatalf("draft state = %v, want READY", version.DraftState)
	}
	outbox := seed.Outbox(t)
	if len(outbox) != 1 || outbox[0].Type != "inventory.draft_ready.v1" {
		t.Fatalf("outbox = %+v", outbox)
	}
	if _, err := events.DefaultRegistry().Validate(outbox[0].Payload); err != nil {
		t.Fatalf("draft_ready envelope violates its contract: %v", err)
	}
}

func TestOpenDraftOfAnotherFlowDoesNotFailTheDelivery(t *testing.T) {
	seed := inventorytest.New(t, inventorytest.InspectionOptions{Requirements: []capturecore.Requirement{inventorytest.EnvironmentRequirement("env:new:"+identity.NewID().String(), "BEDROOM", "Quarto", true)}})
	key := requirementKeys(t, seed)[0]
	seed.Answer(t, key, []identity.ID{seed.Photo(t, key)}, "")
	origin := database.Origin{ID: identity.NewID(), TenantID: seed.Tenant, AssetID: seed.Asset, Version: 1, CreatedAt: seed.Now, UpdatedAt: seed.Now}
	mustCreate(t, seed.DB, &origin)
	preparing := "PREPARING"
	mustCreate(t, seed.DB, &database.OriginVersion{ID: identity.NewID(), TenantID: seed.Tenant, OriginID: origin.ID, VersionNumber: 1, Source: "LEGACY_ORIGIN", HasInventory: true, Status: "DRAFT", DraftState: &preparing, IdempotencyKey: "other-draft", CreatedAt: seed.Now})
	deliver(t, seed, "capture.submitted.v1")
	if len(jobsOf(t, seed)) != 0 {
		t.Fatal("jobs attached to another flow's draft")
	}
	var audit database.AuditEvent
	if err := seed.DB.Where("tenant_id=? AND action='inventory.catalog_skipped' AND target_type='INSPECTION' AND target_id=?", seed.Tenant, seed.Inspection.String()).Take(&audit).Error; err != nil {
		t.Fatalf("catalog skip was not recorded: %v", err)
	}
	if audit.Reason == "" {
		t.Fatal("catalog skip audit does not identify the conflicting draft")
	}
}

func deliver(t *testing.T, seed *inventorytest.Seed, eventType string) {
	t.Helper()
	if eventType == "capture.video_submitted.v2" {
		deliverPayload(t, seed, eventType, events.CaptureVideoSubmittedV2Payload{InspectionID: seed.Inspection, ResponsibilityID: seed.Responsibility, SubmissionVersionID: identity.NewID()})
		return
	}
	deliverPayload(t, seed, eventType, map[string]any{"inspectionId": seed.Inspection})
}

func deliverPayload(t *testing.T, seed *inventorytest.Seed, eventType string, payload any) {
	t.Helper()
	handler, err := Setup(Dependencies{Now: func() time.Time { return seed.Now }})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(payload)
	if err := seed.DB.Transaction(func(tx *gorm.DB) error {
		return handler(context.Background(), tx, events.RawEnvelope{ID: identity.NewID(), Type: eventType, TenantID: seed.Tenant, Payload: raw})
	}); err != nil {
		t.Fatalf("deliver %s: %v", eventType, err)
	}
}

func jobsOf(t *testing.T, seed *inventorytest.Seed) []database.InventoryJob {
	t.Helper()
	var jobs []database.InventoryJob
	if err := seed.DB.Order("requirement_key").Find(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	return jobs
}

func requirementKeys(t *testing.T, seed *inventorytest.Seed) []string {
	t.Helper()
	var draft database.CaptureDraft
	if err := seed.DB.Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	var requirements []capturecore.Requirement
	if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		keys = append(keys, requirement.Key)
	}
	return keys
}

func setRequirements(t *testing.T, seed *inventorytest.Seed, requirements []capturecore.Requirement) {
	t.Helper()
	raw, _ := json.Marshal(requirements)
	if err := seed.DB.Model(&database.CaptureDraft{}).Where("id=?", seed.CaptureDraft).Update("requirements", raw).Error; err != nil {
		t.Fatal(err)
	}
}

func mustCreate(t *testing.T, db *gorm.DB, row any) {
	t.Helper()
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}
}
