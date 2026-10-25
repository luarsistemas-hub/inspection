//go:build integration

package process_inventory_job

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	classifyinspection "inspection/services/inspection/internal/features/analysis/classify_inspection"
	inventoryexecutor "inspection/services/inspection/internal/features/analysis/inventory/core"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	"inspection/services/inspection/internal/features/analysis/request_inventory_jobs"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/features/inventory/request_cataloging"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/features/notifications/record_in_app"
	generatesnapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	handleclassified "inspection/services/inspection/internal/features/reports/handle_classified"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/gorm"
)

// These flows use the disposable PostgreSQL (RLS, runtime role), RabbitMQ and
// MinIO of the integration harness, the real request handler, executor and
// processor, and a scripted in-process LLM gateway.

type flow struct {
	t         *testing.T
	ctx       context.Context
	h         *harness.Harness
	seed      harness.Task06Fixture
	gateway   *inventorytest.Gateway
	processor *Processor
	executor  *inventoryexecutor.Executor
	clock     *atomic.Int64
	catalog   identity.ID
	compare   identity.ID
	// originFrames makes pinned items cite retained video frames.
	originFrames bool
}

var pngBytes = []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 64))

func newFlow(t *testing.T, mediaKind string) *flow {
	t.Helper()
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	seed, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "SUBMITTED", SchemaVersion: 2, RequirementKey: "photo:overview", ComparisonMode: "CURRENT_ONLY"})
	if err != nil {
		t.Fatal(err)
	}
	f := &flow{t: t, ctx: ctx, h: h, seed: seed, gateway: &inventorytest.Gateway{}, clock: &atomic.Int64{}}
	f.clock.Store(time.Now().UTC().Truncate(time.Microsecond).UnixNano())
	for task, target := range map[string]*identity.ID{"INVENTORY_CATALOG": &f.catalog, "INVENTORY_COMPARE": &f.compare} {
		var snapshot database.AnalysisPromptSnapshot
		if err := h.AdminDB.Where("task=?", task).Order("created_at DESC").Take(&snapshot).Error; err != nil {
			t.Fatalf("seeded %s prompt: %v", task, err)
		}
		*target = snapshot.ID
	}
	f.within(func(tx *gorm.DB) error {
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", seed.TenantID, seed.InspectionID).Updates(map[string]any{"inventory_prompt_snapshot_id": f.catalog, "media_kind": mediaKind}).Error
	})
	processor, err := Setup(Dependencies{DB: h.DB, Store: h.Store, Gateway: f.gateway, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	f.processor = processor
	f.executor = &inventoryexecutor.Executor{DB: h.DB, ClaimDB: h.AdminDB, Processor: processor, Now: f.now, HeartbeatInterval: time.Hour}
	return f
}

func (f *flow) now() time.Time { return time.Unix(0, f.clock.Load()).UTC() }

func (f *flow) within(fn func(*gorm.DB) error) {
	f.t.Helper()
	if err := f.h.WithinTenant(f.ctx, f.seed.TenantID, fn); err != nil {
		f.t.Fatal(err)
	}
}

func (f *flow) requirements(requirements ...capturecore.Requirement) {
	f.t.Helper()
	raw, _ := json.Marshal(requirements)
	f.within(func(tx *gorm.DB) error {
		return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", f.seed.TenantID, f.seed.DraftID).Update("requirements", raw).Error
	})
}

func (f *flow) answer(key string, media []identity.ID, impossibility string) {
	f.t.Helper()
	if media == nil {
		media = []identity.ID{}
	}
	ids, _ := json.Marshal(media)
	now := f.now()
	f.within(func(tx *gorm.DB) error {
		return tx.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: f.seed.TenantID, DraftID: f.seed.DraftID, RequirementKey: key, MediaIDs: ids, Flags: json.RawMessage(`[]`), ImpossibilityReason: impossibility, Version: 1, CreatedAt: now, UpdatedAt: now}).Error
	})
}

func (f *flow) put(key string) {
	f.t.Helper()
	data := append(append([]byte{}, pngBytes...), []byte(key)...)
	if err := f.h.Store.Client.Put(f.ctx, f.h.Store.Bucket, key, bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
		f.t.Fatal(err)
	}
}

// photo stores a READY photo with a DISPLAY derivative in MinIO.
func (f *flow) photo(key string) identity.ID {
	f.t.Helper()
	id, now := identity.NewID(), f.now()
	object := "tenant/" + f.seed.TenantID.String() + "/display/" + id.String()
	f.put(object)
	f.within(func(tx *gorm.DB) error {
		if err := tx.Create(&database.MediaObject{ID: id, TenantID: f.seed.TenantID, ResponsibilityID: f.seed.ResponsibilityID, ObjectKey: object, ContentType: "image/png", SHA256: strings.Repeat("a", 64), SizeBytes: 1, Status: "READY", MediaKind: "PHOTO", IdempotencyKey: "it-photo-" + id.String(), RequirementKey: key, Description: "Evidência", CaptureSource: "CAMERA", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.MediaDerivative{ID: identity.NewID(), TenantID: f.seed.TenantID, MediaID: id, ObjectKey: object, Kind: "DISPLAY", SHA256: strings.Repeat("a", 64), ContentType: "image/png", Width: 1, Height: 1, SizeBytes: 1, CreatedAt: now}).Error
	})
	return id
}

// clip stores a READY environment clip with READY representative frames.
func (f *flow) clip(key string, frames int) (identity.ID, []identity.ID) {
	f.t.Helper()
	id, prep, now := identity.NewID(), identity.NewID(), f.now()
	ids := make([]identity.ID, 0, frames)
	f.within(func(tx *gorm.DB) error {
		if err := tx.Create(&database.MediaObject{ID: id, TenantID: f.seed.TenantID, ResponsibilityID: f.seed.ResponsibilityID, ObjectKey: "tenant/" + f.seed.TenantID.String() + "/media/" + id.String(), ContentType: "video/mp4", SHA256: strings.Repeat("c", 64), SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", IdempotencyKey: "it-clip-" + id.String(), RequirementKey: key, AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		requirementKey := key
		if err := tx.Create(&database.VideoPreparationJob{ID: prep, TenantID: f.seed.TenantID, MediaID: id, RequirementKey: &requirementKey, Status: "READY", Phase: "READY", PreparationVersion: 2, Generation: 1, NextAttemptAt: now, SourceSHA256: strings.Repeat("c", 64), ManifestDigest: strings.Repeat("c", 64), PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		for i := 0; i < frames; i++ {
			frame := identity.NewID()
			object := "tenant/" + f.seed.TenantID.String() + "/video-frames/" + frame.String() + ".jpg"
			f.put(object)
			if err := tx.Create(&database.VideoFrame{ID: frame, TenantID: f.seed.TenantID, SourceMediaID: id, PreparationID: prep, PTSMicros: int64(i+1) * 1_000_000, SHA256: strings.Repeat("d", 64), ObjectKey: object, Width: 1, Height: 1, Role: "BASELINE", Representative: true, Quality: "GOOD", SizeBytes: 1, CreatedAt: now}).Error; err != nil {
				return err
			}
			ids = append(ids, frame)
		}
		return nil
	})
	return id, ids
}

type pinned struct {
	version      identity.ID
	environments []database.InventoryEnvironment
	items        map[string]database.InventoryItem
}

// pin creates an ACTIVE inventory version (one origin photo per item) and pins
// the inspection to it.
func (f *flow) pin(environments ...inventorytest.BaselineEnvironment) pinned {
	f.t.Helper()
	p := pinned{version: identity.NewID(), items: map[string]database.InventoryItem{}}
	now, purpose := f.now(), "MOVE_IN"
	evidence := map[string]identity.ID{}
	frameEvidence := map[string][2]identity.ID{}
	for _, env := range environments {
		for _, item := range env.Items {
			if item.Evidence == 0 {
				continue
			}
			if f.originFrames {
				clip, frames := f.clip("origin", 1)
				frameEvidence[item.Name] = [2]identity.ID{clip, frames[0]}
				continue
			}
			evidence[item.Name] = f.photo("origin")
		}
	}
	f.within(func(tx *gorm.DB) error {
		origin := database.Origin{ID: identity.NewID(), TenantID: f.seed.TenantID, AssetID: f.seed.AssetID, ActiveVersionID: &p.version, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&origin).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.OriginVersion{ID: p.version, TenantID: f.seed.TenantID, OriginID: origin.ID, VersionNumber: 1, Purpose: &purpose, Source: "CAPTURE", HasInventory: true, Status: "ACTIVE", IdempotencyKey: "it-baseline-" + p.version.String(), CreatedAt: now, ActivatedAt: &now}).Error; err != nil {
			return err
		}
		for position, spec := range environments {
			env := database.InventoryEnvironment{ID: identity.NewID(), TenantID: f.seed.TenantID, OriginVersionID: p.version, IdentityID: identity.NewID(), TypeCode: spec.TypeCode, Name: spec.Name, Expected: true, Decision: "ACCEPTED", Position: position, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&env).Error; err != nil {
				return err
			}
			p.environments = append(p.environments, env)
			for _, spec := range spec.Items {
				typeCode := spec.TypeCode
				if typeCode == "" {
					typeCode = "OTHER"
				}
				item := database.InventoryItem{ID: identity.NewID(), TenantID: f.seed.TenantID, OriginVersionID: p.version, EnvironmentID: env.ID, IdentityID: identity.NewID(), TypeCode: typeCode, Name: spec.Name, Condition: spec.Condition, Observation: "OBSERVED", Origin: "AI", Declared: spec.Declared, Decision: "ACCEPTED", Version: 1, CreatedAt: now, UpdatedAt: now}
				if err := tx.Create(&item).Error; err != nil {
					return err
				}
				p.items[spec.Name] = item
				if media, ok := evidence[spec.Name]; ok {
					if err := tx.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: f.seed.TenantID, ItemID: item.ID, Kind: "PHOTO", MediaID: media, Position: 0, CreatedAt: now}).Error; err != nil {
						return err
					}
				}
				if frame, ok := frameEvidence[spec.Name]; ok {
					frameID := frame[1]
					if err := tx.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: f.seed.TenantID, ItemID: item.ID, Kind: "VIDEO_FRAME", MediaID: frame[0], FrameID: &frameID, Position: 0, CreatedAt: now}).Error; err != nil {
						return err
					}
				}
			}
		}
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", f.seed.TenantID, f.seed.InspectionID).Update("inventory_prompt_snapshot_id", f.compare).Error
	})
	f.replaceReference(p.version)
	return p
}

// replaceReference pins the inspection to a version. Reference snapshots are
// immutable; the fixture's snapshot is replaced as inspection creation would
// have written it.
func (f *flow) replaceReference(version identity.ID) {
	f.t.Helper()
	err := f.h.AdminDB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('app.tenant_id', ?, true), set_config('app.retention_purge', 'on', true)", f.seed.TenantID.String()).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, f.seed.InspectionID).Delete(&database.ReferenceSnapshot{}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ReferenceSnapshot{ID: identity.NewID(), TenantID: f.seed.TenantID, InspectionID: f.seed.InspectionID, ReferenceVersionID: &version, ComparisonMode: "FIXED_ORIGIN", Payload: json.RawMessage(`{}`), CreatedAt: f.now()}).Error
	})
	if err != nil {
		f.t.Fatal(err)
	}
}

// deliver runs the job request handler inside a tenant transaction.
func (f *flow) deliver(eventType string, payload any) {
	f.t.Helper()
	handler, err := request_inventory_jobs.Setup(request_inventory_jobs.Dependencies{Now: f.now})
	if err != nil {
		f.t.Fatal(err)
	}
	raw, _ := json.Marshal(payload)
	f.within(func(tx *gorm.DB) error {
		return handler(f.ctx, tx, events.RawEnvelope{ID: identity.NewID(), Type: eventType, TenantID: f.seed.TenantID, Payload: raw})
	})
}

func (f *flow) submitPhotos() {
	f.deliver("capture.submitted.v1", map[string]any{"inspectionId": f.seed.InspectionID})
}

// drain runs the executor until no job remains due, jumping over backoff.
func (f *flow) drain() {
	f.t.Helper()
	for i := 0; i < 100; i++ {
		worked, err := f.executor.RunDue(f.ctx)
		if err != nil {
			f.t.Fatal(err)
		}
		if worked {
			continue
		}
		var pending database.InventoryJob
		err = f.h.AdminDB.Where("tenant_id=? AND status='PENDING'", f.seed.TenantID).Order("next_attempt_at").Take(&pending).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return
		}
		if err != nil {
			f.t.Fatal(err)
		}
		f.clock.Store(pending.NextAttemptAt.UnixNano())
	}
	f.t.Fatal("executor did not drain")
}

func (f *flow) jobs() []database.InventoryJob {
	f.t.Helper()
	var jobs []database.InventoryJob
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, f.seed.InspectionID).Order("requirement_key").Find(&jobs).Error
	})
	return jobs
}

func (f *flow) results() map[string]database.InventoryItemResult {
	f.t.Helper()
	var rows []database.InventoryItemResult
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, f.seed.InspectionID).Find(&rows).Error
	})
	out := map[string]database.InventoryItemResult{}
	for _, row := range rows {
		key := "outside:" + value(row.OutsideName)
		if row.ItemIdentityID != nil {
			key = row.ItemIdentityID.String()
		}
		out[key] = row
	}
	return out
}

func (f *flow) outbox(eventType string) []database.OutboxIntent {
	f.t.Helper()
	var rows []database.OutboxIntent
	if err := f.h.AdminDB.Where("tenant_id=? AND type=?", f.seed.TenantID, eventType).Find(&rows).Error; err != nil {
		f.t.Fatal(err)
	}
	return rows
}

// matchAll proposes and verifies SAME_VIEW for the first candidate and answers
// INVENTORY_COMPARE and INVENTORY_CATALOG with the given functions.
func (f *flow) script(compare func(llm.StructuredRequest) string, catalog func(llm.StructuredRequest) string) {
	f.gateway.Respond = func(request llm.StructuredRequest) (string, error) {
		switch request.Mode {
		case "INVENTORY_MATCH":
			var in matchRequest
			if err := json.Unmarshal([]byte(request.UserPrompt), &in); err != nil {
				return "", err
			}
			if in.Stage == "verify" {
				return mustJSON(f.t, matchOutput{Pairs: in.ProposedPairs}), nil
			}
			return mustJSON(f.t, matchOutput{Pairs: []matchPair{{OriginEvidenceID: in.OriginEvidenceID, CurrentEvidenceID: in.CurrentEvidenceIDs[0], Decision: "SAME_VIEW", Reason: "mesma vista"}}}), nil
		case "INVENTORY_COMPARE":
			return compare(request), nil
		case "INVENTORY_CATALOG":
			return catalog(request), nil
		}
		return "", errors.New("unexpected mode")
	}
}

// firstEvidence returns the first evidence id offered to a catalog call.
func firstEvidence(t *testing.T, request llm.StructuredRequest) string {
	evidence := userData(t, request)["evidence"].([]any)
	return evidence[0].(map[string]any)["mediaId"].(string)
}

func oneItemCatalog(t *testing.T, name string) func(llm.StructuredRequest) string {
	return func(request llm.StructuredRequest) string {
		return catalogJSON(t, catalogItemSpec{name: name, confidence: 9000, evidence: []string{firstEvidence(t, request)}})
	}
}

func TestIT010PhotoCatalogingProducesReadyDraftAndInAppNotice(t *testing.T) {
	f := newFlow(t, "PHOTO")
	sala, cozinha := "env:new:"+identity.NewID().String(), "env:new:"+identity.NewID().String()
	f.requirements(inventorytest.EnvironmentRequirement(sala, "LIVING_ROOM", "Sala", true), inventorytest.EnvironmentRequirement(cozinha, "KITCHEN", "Cozinha", true))
	f.answer(sala, []identity.ID{f.photo(sala)}, "")
	f.answer(cozinha, []identity.ID{f.photo(cozinha)}, "")
	f.script(nil, oneItemCatalog(t, "Armário"))
	f.submitPhotos()
	f.submitPhotos()
	if jobs := f.jobs(); len(jobs) != 2 {
		t.Fatalf("CATALOG jobs = %d, want 2", len(jobs))
	}
	f.drain()
	var draft database.OriginVersion
	var items int64
	f.within(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND source_inspection_id=?", f.seed.TenantID, f.seed.InspectionID).Take(&draft).Error; err != nil {
			return err
		}
		return tx.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=? AND origin='AI' AND decision='PENDING'", f.seed.TenantID, draft.ID).Count(&items).Error
	})
	if draft.DraftState == nil || *draft.DraftState != "READY" || items != 2 {
		t.Fatalf("draft = %v items = %d, want READY with 2 suggestions", draft.DraftState, items)
	}
	ready := f.outbox("inventory.draft_ready.v1")
	if len(ready) != 1 {
		t.Fatalf("draft_ready events = %d, want 1", len(ready))
	}
	envelope, err := events.DefaultRegistry().Validate(ready[0].Payload)
	if err != nil {
		t.Fatalf("draft_ready violates its contract: %v", err)
	}
	f.within(func(tx *gorm.DB) error { return record_in_app.Setup()(f.ctx, tx, envelope) })
	var notices int64
	f.within(func(tx *gorm.DB) error {
		return tx.Table("notifications.recipient_notifications").Where("tenant_id=? AND kind='INVENTORY_DRAFT_READY' AND resource_id=?", f.seed.TenantID, draft.ID).Count(&notices).Error
	})
	if notices == 0 {
		t.Fatal("no in-app notice recorded for the asset's internal recipients")
	}
}

func TestIT020LegacyPhotoOriginCatalogingBuildsDraftFromOriginEvidence(t *testing.T) {
	f := newFlow(t, "PHOTO")
	legacy, now := identity.NewID(), f.now()
	photo := f.photo("origin")
	f.within(func(tx *gorm.DB) error {
		origin := database.Origin{ID: identity.NewID(), TenantID: f.seed.TenantID, AssetID: f.seed.AssetID, ActiveVersionID: &legacy, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&origin).Error; err != nil {
			return err
		}
		responsibility := f.seed.ResponsibilityID
		if err := tx.Create(&database.OriginVersion{ID: legacy, TenantID: f.seed.TenantID, OriginID: origin.ID, VersionNumber: 1, ResponsibilityID: &responsibility, Source: "CAPTURE", Status: "ACTIVE", IdempotencyKey: "it-legacy-" + legacy.String(), CreatedAt: now, ActivatedAt: &now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.OriginEvidence{ID: identity.NewID(), TenantID: f.seed.TenantID, OriginVersionID: legacy, MediaID: photo, Category: "GERAL", Description: "Sala", AttentionItems: json.RawMessage(`[]`), CreatedAt: now}).Error
	})
	service := inventorycore.Service{DB: f.h.DB, Within: f.h.WithinTenant, Now: f.now}
	draft, err := service.RequestOriginCataloging(f.ctx, f.seed.TenantID, f.seed.AssetID, f.seed.MembershipID)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Source != "LEGACY_ORIGIN" || draft.DraftState == nil || *draft.DraftState != "PREPARING" {
		t.Fatalf("draft = %+v, want a PREPARING LEGACY_ORIGIN draft", draft)
	}
	f.gateway.Respond = func(request llm.StructuredRequest) (string, error) {
		evidence := firstEvidence(t, request)
		item := map[string]any{"typeCode": "SOFA", "name": "Sofá", "material": nil, "color": nil, "quantity": 1, "condition": "GOOD", "observation": "OBSERVED", "limitation": nil, "confidenceBps": 9000, "evidence": []any{map[string]any{"mediaId": evidence, "frameId": nil}}}
		return mustJSON(t, map[string]any{"environments": []any{map[string]any{"typeCode": "LIVING_ROOM", "name": "Sala", "items": []any{item}}}}), nil
	}
	f.drain()
	var reloaded database.OriginVersion
	var environments []database.InventoryEnvironment
	f.within(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", f.seed.TenantID, draft.ID).Take(&reloaded).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND origin_version_id=?", f.seed.TenantID, draft.ID).Find(&environments).Error
	})
	if reloaded.DraftState == nil || *reloaded.DraftState != "READY" || len(environments) != 1 || environments[0].Name != "Sala" {
		t.Fatalf("legacy draft = %v environments = %+v", reloaded.DraftState, environments)
	}
	if calls := f.gateway.Calls("INVENTORY_CATALOG"); len(calls) != 1 || calls[0].Images[0].EvidenceID != photo.String() {
		t.Fatalf("legacy catalog request = %+v", calls)
	}
}

func baselineSpec() []inventorytest.BaselineEnvironment {
	return []inventorytest.BaselineEnvironment{
		{Name: "Sala", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}, {Name: "Mesa", TypeCode: "TABLE", Condition: "GOOD", Evidence: 1}}},
		{Name: "Garagem", TypeCode: "GARAGE", Items: []inventorytest.BaselineItem{{Name: "Portão", Condition: "GOOD"}}},
	}
}

func (f *flow) captureBaseline(p pinned, media func(key string) []identity.ID) {
	requirements := []capturecore.Requirement{}
	for _, env := range p.environments {
		requirements = append(requirements, inventorytest.EnvironmentRequirement(inventorytest.EnvironmentKey(env), env.TypeCode, env.Name, false))
	}
	f.requirements(requirements...)
	f.answer(inventorytest.EnvironmentKey(p.environments[0]), media(inventorytest.EnvironmentKey(p.environments[0])), "")
	f.answer(inventorytest.EnvironmentKey(p.environments[1]), nil, "LOCKED")
}

func TestIT023PhotoComparisonRecordsResultsPairsAndFindings(t *testing.T) {
	f := newFlow(t, "PHOTO")
	p := f.pin(baselineSpec()...)
	f.captureBaseline(p, func(key string) []identity.ID { return []identity.ID{f.photo(key)} })
	f.script(func(llm.StructuredRequest) string {
		return compareJSON(t, claim(p.items["Sofá"], "MISSING"), claim(p.items["Mesa"], "PRESENT_UNCHANGED"))
	}, nil)
	f.submitPhotos()
	if jobs := f.jobs(); len(jobs) != 2 {
		t.Fatalf("COMPARE jobs = %d, want one per baseline environment", len(jobs))
	}
	f.drain()
	results := f.results()
	expect := map[string]string{"Sofá": "MISSING", "Mesa": "PRESENT_UNCHANGED", "Portão": "NOT_OBSERVED"}
	for name, status := range expect {
		if got := results[p.items[name].IdentityID.String()]; got.Status != status {
			t.Fatalf("%s = %+v, want %s", name, got, status)
		}
	}
	if reason := value(results[p.items["Portão"].IdentityID.String()].ReasonCode); reason != "ENVIRONMENT_NOT_CAPTURED" {
		t.Fatalf("impossible environment reason = %s", reason)
	}
	var pairs []database.InventoryEvidencePair
	var findings []database.FindingRecord
	f.within(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND confirmed", f.seed.TenantID).Find(&pairs).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND inventory_job_id IS NOT NULL", f.seed.TenantID).Find(&findings).Error
	})
	if len(pairs) != 2 {
		t.Fatalf("confirmed pairs = %d, want 2", len(pairs))
	}
	if len(findings) != 1 || findings[0].ItemIdentityID == nil || *findings[0].ItemIdentityID != p.items["Sofá"].IdentityID || findings[0].Severity != "CRITICAL" {
		t.Fatalf("findings = %+v", findings)
	}
	if completed := f.outbox("analysis.comparison_completed.v1"); len(completed) != 1 {
		t.Fatalf("comparison_completed events = %d, want 1 after the last job", len(completed))
	}
	var pinnedEnvironments int64
	f.within(func(tx *gorm.DB) error {
		return tx.Model(&database.InventoryEnvironment{}).Where("tenant_id=? AND origin_version_id=?", f.seed.TenantID, p.version).Count(&pinnedEnvironments).Error
	})
	if pinnedEnvironments != 2 {
		t.Fatalf("pinned version environments = %d, the reference must not change", pinnedEnvironments)
	}
}

func TestIT024PhotoBaselineAgainstVideoInspectionPairsFrames(t *testing.T) {
	f := newFlow(t, "VIDEO")
	p := f.pin(baselineSpec()...)
	var frames []identity.ID
	f.captureBaseline(p, func(key string) []identity.ID {
		clip, ids := f.clip(key, 2)
		frames = ids
		return []identity.ID{clip}
	})
	f.script(func(llm.StructuredRequest) string { return compareJSON(t) }, nil)
	f.deliver("capture.video_submitted.v2", events.CaptureVideoSubmittedV2Payload{InspectionID: f.seed.InspectionID, ResponsibilityID: f.seed.ResponsibilityID, SubmissionVersionID: identity.NewID()})
	if jobs := f.jobs(); len(jobs) != 2 {
		t.Fatalf("COMPARE jobs = %d", len(jobs))
	}
	f.drain()
	var pairs []database.InventoryEvidencePair
	f.within(func(tx *gorm.DB) error { return tx.Where("tenant_id=?", f.seed.TenantID).Find(&pairs).Error })
	if len(pairs) != 2 {
		t.Fatalf("pairs = %d, want 2", len(pairs))
	}
	for _, pair := range pairs {
		if pair.OriginKind != "PHOTO" || pair.CurrentKind != "VIDEO_FRAME" || pair.CurrentFrameID == nil || *pair.CurrentFrameID != frames[0] || !pair.Confirmed {
			t.Fatalf("pair = %+v, want PHOTO ↔ VIDEO_FRAME", pair)
		}
	}
}

// takeover lets a second executor reclaim the job while the first one works.
type takeover struct {
	inner     *Processor
	second    func()
	triggered atomic.Bool
}

func (p *takeover) Process(ctx context.Context, job database.InventoryJob, fenced func(func(*gorm.DB) error) error) error {
	if p.triggered.CompareAndSwap(false, true) {
		p.second()
	}
	return p.inner.Process(ctx, job, fenced)
}
func (p *takeover) Failed(ctx context.Context, tx *gorm.DB, job database.InventoryJob, code string) error {
	return p.inner.Failed(ctx, tx, job, code)
}
func (p *takeover) Succeeded(ctx context.Context, tx *gorm.DB, job database.InventoryJob) error {
	return p.inner.Succeeded(ctx, tx, job)
}

func TestIT030ExpiredLeaseIsReclaimedAndStaleWriterRejected(t *testing.T) {
	f := newFlow(t, "PHOTO")
	p := f.pin(baselineSpec()[0])
	f.requirements(inventorytest.EnvironmentRequirement(inventorytest.EnvironmentKey(p.environments[0]), "LIVING_ROOM", "Sala", false))
	f.answer(inventorytest.EnvironmentKey(p.environments[0]), []identity.ID{f.photo("current")}, "")
	f.script(func(llm.StructuredRequest) string {
		return compareJSON(t, claim(p.items["Sofá"], "PRESENT_UNCHANGED"), claim(p.items["Mesa"], "PRESENT_UNCHANGED"))
	}, nil)
	f.submitPhotos()
	wrapper := &takeover{inner: f.processor}
	first := &inventoryexecutor.Executor{DB: f.h.DB, ClaimDB: f.h.AdminDB, Processor: wrapper, Now: f.now, HeartbeatInterval: time.Hour}
	second := &inventoryexecutor.Executor{DB: f.h.DB, ClaimDB: f.h.AdminDB, Processor: wrapper, Now: f.now, HeartbeatInterval: time.Hour}
	wrapper.second = func() {
		// The first owner stalls past its 120s lease; another executor reclaims.
		f.clock.Add(int64(200 * time.Second))
		if worked, err := second.RunDue(f.ctx); err != nil || !worked {
			t.Errorf("second executor RunDue = %v, %v", worked, err)
		}
	}
	if _, err := first.RunDue(f.ctx); err != nil {
		t.Fatalf("stale owner must end quietly: %v", err)
	}
	job := f.jobs()[0]
	if job.Status != "SUCCEEDED" || job.Attempts != 2 {
		t.Fatalf("job = %+v, want SUCCEEDED by the second owner", job)
	}
	var runs int64
	f.within(func(tx *gorm.DB) error {
		return tx.Model(&database.AnalysisRun{}).Where("tenant_id=? AND job_id=?", f.seed.TenantID, job.ID).Count(&runs).Error
	})
	if results := f.results(); len(results) != 2 || runs != 1 {
		t.Fatalf("results = %d runs = %d, want one result set", len(results), runs)
	}
}

// counting records how many times each job is processed.
type counting struct {
	mu    sync.Mutex
	inner *Processor
	seen  map[identity.ID]int
}

func (p *counting) Process(ctx context.Context, job database.InventoryJob, fenced func(func(*gorm.DB) error) error) error {
	p.mu.Lock()
	p.seen[job.ID]++
	p.mu.Unlock()
	return p.inner.Process(ctx, job, fenced)
}
func (p *counting) Failed(ctx context.Context, tx *gorm.DB, job database.InventoryJob, code string) error {
	return p.inner.Failed(ctx, tx, job, code)
}
func (p *counting) Succeeded(ctx context.Context, tx *gorm.DB, job database.InventoryJob) error {
	return p.inner.Succeeded(ctx, tx, job)
}

func TestIT031ParallelExecutorsProcessEachJobOnce(t *testing.T) {
	f := newFlow(t, "PHOTO")
	specs := make([]inventorytest.BaselineEnvironment, 0, 10)
	for i := 0; i < 10; i++ {
		specs = append(specs, inventorytest.BaselineEnvironment{Name: "Quarto " + string(rune('A'+i)), TypeCode: "BEDROOM", Items: []inventorytest.BaselineItem{{Name: "Cama " + string(rune('A'+i)), Condition: "GOOD"}}})
	}
	p := f.pin(specs...)
	requirements := []capturecore.Requirement{}
	for _, env := range p.environments {
		requirements = append(requirements, inventorytest.EnvironmentRequirement(inventorytest.EnvironmentKey(env), env.TypeCode, env.Name, false))
		f.answer(inventorytest.EnvironmentKey(env), nil, "LOCKED")
	}
	f.requirements(requirements...)
	f.submitPhotos()
	if jobs := f.jobs(); len(jobs) != 10 {
		t.Fatalf("jobs = %d, want 10", len(jobs))
	}
	counter := &counting{inner: f.processor, seen: map[identity.ID]int{}}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		executor := &inventoryexecutor.Executor{DB: f.h.DB, ClaimDB: f.h.AdminDB, Processor: counter, Now: f.now, HeartbeatInterval: time.Hour}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				worked, err := executor.RunDue(f.ctx)
				if err != nil {
					t.Error(err)
					return
				}
				if !worked {
					return
				}
			}
		}()
	}
	wg.Wait()
	if len(counter.seen) != 10 {
		t.Fatalf("processed %d jobs, want 10", len(counter.seen))
	}
	for id, count := range counter.seen {
		if count != 1 {
			t.Fatalf("job %s processed %d times", id, count)
		}
	}
	for _, job := range f.jobs() {
		if job.Status != "SUCCEEDED" {
			t.Fatalf("job %s = %s", job.RequirementKey, job.Status)
		}
	}
}

func TestIT032FailuresFailCompareItemsAndRetryRecoversCatalog(t *testing.T) {
	t.Run("compare", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		p := f.pin(baselineSpec()[0])
		f.requirements(inventorytest.EnvironmentRequirement(inventorytest.EnvironmentKey(p.environments[0]), "LIVING_ROOM", "Sala", false))
		f.answer(inventorytest.EnvironmentKey(p.environments[0]), []identity.ID{f.photo("current")}, "")
		f.gateway.Respond = func(llm.StructuredRequest) (string, error) { return "", errGateway }
		f.submitPhotos()
		f.drain()
		job := f.jobs()[0]
		if job.Status != "FAILED" || job.Attempts != 4 {
			t.Fatalf("job = %+v, want FAILED after 4 attempts", job)
		}
		for _, item := range p.items {
			if row := f.results()[item.IdentityID.String()]; row.Status != "NOT_OBSERVED" || value(row.ReasonCode) != "ANALYSIS_FAILED" {
				t.Fatalf("%s = %+v", item.Name, row)
			}
		}
	})
	t.Run("catalog", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		key := "env:new:" + identity.NewID().String()
		f.requirements(inventorytest.EnvironmentRequirement(key, "BEDROOM", "Quarto", true))
		f.answer(key, []identity.ID{f.photo(key)}, "")
		f.gateway.Respond = func(llm.StructuredRequest) (string, error) { return "", errGateway }
		f.submitPhotos()
		f.drain()
		draftState := func() string {
			var draft database.OriginVersion
			f.within(func(tx *gorm.DB) error {
				return tx.Where("tenant_id=? AND source_inspection_id=?", f.seed.TenantID, f.seed.InspectionID).Take(&draft).Error
			})
			return value(draft.DraftState) + ":" + draft.ID.String()
		}
		state := draftState()
		if !strings.HasPrefix(state, "FAILED:") {
			t.Fatalf("draft = %s, want FAILED", state)
		}
		versionID, _ := identity.ParseID(strings.TrimPrefix(state, "FAILED:"))
		service := inventorycore.Service{DB: f.h.DB, Within: f.h.WithinTenant, Now: f.now}
		if _, err := service.RetryDraft(f.ctx, f.seed.TenantID, versionID, f.seed.MembershipID); err != nil {
			t.Fatal(err)
		}
		f.script(nil, oneItemCatalog(t, "Cama"))
		f.drain()
		if state := draftState(); !strings.HasPrefix(state, "READY:") {
			t.Fatalf("draft after retry = %s, want READY", state)
		}
	})
}

func TestIT025VideoBaselineAgainstPhotoInspectionPairsPhotos(t *testing.T) {
	f := newFlow(t, "PHOTO")
	f.originFrames = true
	p := f.pin(baselineSpec()...)
	f.captureBaseline(p, func(key string) []identity.ID { return []identity.ID{f.photo(key)} })
	f.script(func(llm.StructuredRequest) string { return compareJSON(t) }, nil)
	f.submitPhotos()
	f.drain()
	var pairs []database.InventoryEvidencePair
	f.within(func(tx *gorm.DB) error { return tx.Where("tenant_id=?", f.seed.TenantID).Find(&pairs).Error })
	if len(pairs) != 2 {
		t.Fatalf("pairs = %d, want 2", len(pairs))
	}
	for _, pair := range pairs {
		if pair.OriginKind != "VIDEO_FRAME" || pair.OriginFrameID == nil || pair.CurrentKind != "PHOTO" || !pair.Confirmed {
			t.Fatalf("pair = %+v, want VIDEO_FRAME ↔ PHOTO", pair)
		}
	}
}

func TestIT028ActivationDuringAnalysisKeepsThePinnedVersion(t *testing.T) {
	f := newFlow(t, "PHOTO")
	p := f.pin(baselineSpec()...)
	f.captureBaseline(p, func(key string) []identity.ID { return []identity.ID{f.photo(key)} })
	f.submitPhotos()
	newer, now, purpose := identity.NewID(), f.now(), "INVENTORY_UPDATE"
	f.within(func(tx *gorm.DB) error {
		var version database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", f.seed.TenantID, p.version).Take(&version).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", f.seed.TenantID, p.version).Update("status", "SUPERSEDED").Error; err != nil {
			return err
		}
		if err := tx.Create(&database.OriginVersion{ID: newer, TenantID: f.seed.TenantID, OriginID: version.OriginID, VersionNumber: 2, Purpose: &purpose, Source: "CAPTURE", HasInventory: true, Status: "ACTIVE", IdempotencyKey: "it-newer-" + newer.String(), CreatedAt: now, ActivatedAt: &now}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Origin{}).Where("tenant_id=? AND id=?", f.seed.TenantID, version.OriginID).Update("active_version_id", newer).Error
	})
	f.script(func(request llm.StructuredRequest) string {
		return compareJSON(t, claim(p.items["Sofá"], "PRESENT_UNCHANGED"), claim(p.items["Mesa"], "PRESENT_UNCHANGED"))
	}, nil)
	f.drain()
	for _, job := range f.jobs() {
		if job.OriginVersionID != p.version || job.Status != "SUCCEEDED" {
			t.Fatalf("job = %+v, want the pinned version", job)
		}
	}
	if calls := f.gateway.Calls("INVENTORY_COMPARE"); len(calls) != 1 || len(userData(t, calls[0])["items"].([]any)) != 2 {
		t.Fatalf("compare request must use the pinned version's items: %+v", calls)
	}
	if row := f.results()[p.items["Sofá"].IdentityID.String()]; row.Status != "PRESENT_UNCHANGED" {
		t.Fatalf("Sofá = %+v", row)
	}
}

type silentNotifications struct{}

func (silentNotifications) Send(context.Context, notificationcore.Notification) (notificationcore.NotificationResult, error) {
	return notificationcore.NotificationResult{ID: identity.NewID()}, nil
}

// completeFindingsComparison records the legacy FINDINGS comparison of the
// inspection as completed without findings, as process_comparison would.
func (f *flow) completeFindingsComparison() {
	f.t.Helper()
	now := f.now()
	job := database.ComparisonJob{ID: identity.NewID(), TenantID: f.seed.TenantID, InspectionID: f.seed.InspectionID, PromptSnapshotID: f.seed.PromptSnapshotID, RequirementKey: "photo:overview", ModelAlias: "inspection-vision", InputDigest: strings.Repeat("e", 64), PromptDigest: "it-findings", Status: "COMPLETED", Attempts: 1, CreatedAt: now, UpdatedAt: now}
	f.within(func(tx *gorm.DB) error {
		if err := tx.Create(&job).Error; err != nil {
			return err
		}
		return tx.Create(&database.AnalysisRun{ID: identity.NewID(), TenantID: f.seed.TenantID, JobID: job.ID, PromptSnapshotID: f.seed.PromptSnapshotID, Provider: "stub", Model: "stub/vision", GatewayRequestID: "it-findings", PromptDigest: "it-findings", InputDigest: job.InputDigest, Output: json.RawMessage(`{"noRelevantChange":true,"findings":[]}`), LatencyMS: 1, ValidationOutcome: "ACCEPTED", CreatedAt: now}).Error
	})
}

// classifyAndReport runs classification and the classified-event handler, the
// way the worker does after analysis.comparison_completed.v1.
func (f *flow) classifyAndReport() (database.ClassificationRun, database.ReportSnapshot) {
	f.t.Helper()
	f.clock.Add(int64(time.Second))
	classify, err := classifyinspection.Setup(classifyinspection.Dependencies{Now: f.now})
	if err != nil {
		f.t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"inspectionId": f.seed.InspectionID})
	f.within(func(tx *gorm.DB) error {
		return classify(f.ctx, tx, events.RawEnvelope{ID: identity.NewID(), Type: "analysis.comparison_completed.v1", TenantID: f.seed.TenantID, CorrelationID: f.seed.InspectionID.String(), Payload: payload})
	})
	var run database.ClassificationRun
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, f.seed.InspectionID).Order("created_at DESC").First(&run).Error
	})
	var classified []database.OutboxIntent
	if err := f.h.AdminDB.Where("tenant_id=? AND type='inspection.classified.v1'", f.seed.TenantID).Order("created_at DESC").Find(&classified).Error; err != nil {
		f.t.Fatal(err)
	}
	var envelope events.RawEnvelope
	for _, row := range classified {
		candidate, err := events.DefaultRegistry().Validate(row.Payload)
		if err != nil {
			f.t.Fatalf("inspection.classified.v1 violates its contract: %v", err)
		}
		var body events.InspectionClassifiedPayload
		_ = json.Unmarshal(candidate.Payload, &body)
		if body.ClassificationRunID == run.ID {
			envelope = candidate
		}
	}
	if envelope.ID == (identity.ID{}) {
		f.t.Fatalf("no inspection.classified.v1 for run %s", run.ID)
	}
	handler, err := handleclassified.Setup(handleclassified.Dependencies{Notifications: silentNotifications{}, CreateSnapshot: func(ctx context.Context, input generatesnapshot.Input) (generatesnapshot.Result, error) {
		return generatesnapshot.Create(ctx, f.h.AdminDB, input, f.now())
	}, Now: f.now})
	if err != nil {
		f.t.Fatal(err)
	}
	f.within(func(tx *gorm.DB) error { return handler(f.ctx, tx, envelope) })
	var snapshot database.ReportSnapshot
	if err := f.h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, f.seed.InspectionID).Order("version_number DESC").First(&snapshot).Error; err != nil {
		f.t.Fatal(err)
	}
	return run, snapshot
}

func (f *flow) triage() (database.TriageCase, bool) {
	f.t.Helper()
	var triage database.TriageCase
	err := f.h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, f.seed.InspectionID).Take(&triage).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return triage, false
	}
	if err != nil {
		f.t.Fatal(err)
	}
	return triage, true
}

func snapshotInventory(t *testing.T, snapshot database.ReportSnapshot) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(snapshot.CanonicalJSON, &document); err != nil {
		t.Fatal(err)
	}
	inventory, _ := document["inventory"].(map[string]any)
	return inventory
}

func TestIT021LegacyVideoOriginWithExpiredSourceCatalogsRetainedFrames(t *testing.T) {
	f := newFlow(t, "PHOTO")
	clip, frames := f.clip("video:main", 2)
	legacy, now := identity.NewID(), f.now()
	f.within(func(tx *gorm.DB) error {
		var prep database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND media_id=?", f.seed.TenantID, clip).Take(&prep).Error; err != nil {
			return err
		}
		expired := now.Add(-time.Hour)
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: f.seed.TenantID, MediaID: clip, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 2_000_000, Width: 1, Height: 1, FrameRate: 1, ScreeningStatus: "APPROVED", SourceExpiryAt: &expired, OriginalDeletedAt: &expired, RetentionAnchor: "FIXED", Status: "EXPIRED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		origin := database.Origin{ID: identity.NewID(), TenantID: f.seed.TenantID, AssetID: f.seed.AssetID, ActiveVersionID: &legacy, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&origin).Error; err != nil {
			return err
		}
		responsibility := f.seed.ResponsibilityID
		return tx.Create(&database.OriginVersion{ID: legacy, TenantID: f.seed.TenantID, OriginID: origin.ID, VersionNumber: 1, ResponsibilityID: &responsibility, Source: "CAPTURE", Status: "ACTIVE", MediaKind: "VIDEO", VideoPreparationID: &prep.ID, IdempotencyKey: "it-legacy-video-" + legacy.String(), CreatedAt: now, ActivatedAt: &now}).Error
	})
	bus := mediator.New()
	if err := request_cataloging.Setup(bus, inventorycore.Service{DB: f.h.DB, Within: f.h.WithinTenant, Now: f.now}); err != nil {
		t.Fatal(err)
	}
	raw, err := bus.Send(f.ctx, request_cataloging.Command{TenantID: f.seed.TenantID, AssetID: f.seed.AssetID, ActorID: f.seed.MembershipID})
	if err != nil {
		t.Fatal(err)
	}
	result := raw.(request_cataloging.Result)
	if !result.SourcesExpired || result.Version.Source != "LEGACY_ORIGIN" {
		t.Fatalf("cataloging result = %+v, want sourcesExpired for the expired legacy video", result)
	}
	f.gateway.Respond = func(request llm.StructuredRequest) (string, error) {
		evidence := userData(t, request)["evidence"].([]any)[0].(map[string]any)
		item := map[string]any{"typeCode": "WINDOW", "name": "Janela", "material": nil, "color": nil, "quantity": 1, "condition": "GOOD", "observation": "OBSERVED", "limitation": nil, "confidenceBps": 9000, "evidence": []any{evidence}}
		return mustJSON(t, map[string]any{"environments": []any{map[string]any{"typeCode": "LIVING_ROOM", "name": "Sala", "items": []any{item}}}}), nil
	}
	f.drain()
	calls := f.gateway.Calls("INVENTORY_CATALOG")
	if len(calls) != 1 || len(calls[0].Images) != len(frames) || calls[0].Images[0].Source != "VIDEO_FRAME" {
		t.Fatalf("legacy video cataloging must use the retained frames: %+v", calls)
	}
	var evidence []database.InventoryItemEvidence
	f.within(func(tx *gorm.DB) error {
		return tx.Table("origins.inventory_item_evidence AS e").Select("e.*").Joins("JOIN origins.inventory_items i ON i.tenant_id=e.tenant_id AND i.id=e.item_id").Where("e.tenant_id=? AND i.origin_version_id=?", f.seed.TenantID, result.Version.ID).Find(&evidence).Error
	})
	if len(evidence) != 1 || evidence[0].Kind != "VIDEO_FRAME" || evidence[0].FrameID == nil {
		t.Fatalf("catalog evidence = %+v, want a retained frame", evidence)
	}
}

func unchangedCompare(t *testing.T, p pinned) func(llm.StructuredRequest) string {
	return func(llm.StructuredRequest) string {
		return compareJSON(t, claim(p.items["Sofá"], "PRESENT_UNCHANGED"), claim(p.items["Mesa"], "PRESENT_UNCHANGED"))
	}
}

func TestIT026AllUnchangedIsNormalWithoutTriageAndSummaryOnly(t *testing.T) {
	f := newFlow(t, "PHOTO")
	p := f.pin(baselineSpec()[0])
	key := inventorytest.EnvironmentKey(p.environments[0])
	f.requirements(inventorytest.EnvironmentRequirement(key, "LIVING_ROOM", "Sala", false))
	f.answer(key, []identity.ID{f.photo(key)}, "")
	f.completeFindingsComparison()
	f.script(unchangedCompare(t, p), nil)
	f.submitPhotos()
	f.drain()
	run, snapshot := f.classifyAndReport()
	if run.Classification != "NORMAL" {
		t.Fatalf("classification = %s %s, want NORMAL", run.Classification, run.ReasonCodes)
	}
	if _, opened := f.triage(); opened {
		t.Fatal("NORMAL inspection opened a triage case")
	}
	if snapshot.SchemaVersion != 2 || !strings.Contains(string(snapshot.HTML), "inventory-summary") || strings.Contains(string(snapshot.HTML), "inventory-problems") {
		t.Fatalf("snapshot v%d must render the inventory summary only", snapshot.SchemaVersion)
	}
}

func TestIT027LegacyMediaOnlyReferenceKeepsSnapshotV1(t *testing.T) {
	f := newFlow(t, "PHOTO")
	legacy, now := identity.NewID(), f.now()
	f.within(func(tx *gorm.DB) error {
		origin := database.Origin{ID: identity.NewID(), TenantID: f.seed.TenantID, AssetID: f.seed.AssetID, ActiveVersionID: &legacy, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&origin).Error; err != nil {
			return err
		}
		return tx.Create(&database.OriginVersion{ID: legacy, TenantID: f.seed.TenantID, OriginID: origin.ID, VersionNumber: 1, Source: "CAPTURE", Status: "ACTIVE", IdempotencyKey: "it027-" + legacy.String(), CreatedAt: now, ActivatedAt: &now}).Error
	})
	f.replaceReference(legacy)
	key := "env:new:" + identity.NewID().String()
	f.requirements(inventorytest.EnvironmentRequirement(key, "LIVING_ROOM", "Sala", true))
	f.answer(key, []identity.ID{f.photo(key)}, "")
	f.completeFindingsComparison()
	f.submitPhotos()
	if jobs := f.jobs(); len(jobs) != 0 {
		t.Fatalf("legacy reference produced %d inventory jobs", len(jobs))
	}
	run, snapshot := f.classifyAndReport()
	if run.Classification != "NORMAL" || snapshot.SchemaVersion != 1 || snapshotInventory(t, snapshot) != nil {
		t.Fatalf("legacy comparison = %s, snapshot v%d with inventory %v", run.Classification, snapshot.SchemaVersion, snapshotInventory(t, snapshot))
	}
}

func TestIT029ReanalysisChangesPairAndStatusInANewSnapshotVersion(t *testing.T) {
	f := newFlow(t, "PHOTO")
	p := f.pin(baselineSpec()[0])
	key := inventorytest.EnvironmentKey(p.environments[0])
	first, second := f.photo(key), f.photo(key)
	f.requirements(inventorytest.EnvironmentRequirement(key, "LIVING_ROOM", "Sala", false))
	f.answer(key, []identity.ID{first, second}, "")
	f.completeFindingsComparison()
	pick, status := 0, "PRESENT_UNCHANGED"
	f.gateway.Respond = func(request llm.StructuredRequest) (string, error) {
		switch request.Mode {
		case "INVENTORY_MATCH":
			var in matchRequest
			if err := json.Unmarshal([]byte(request.UserPrompt), &in); err != nil {
				return "", err
			}
			if in.Stage == "verify" {
				return mustJSON(t, matchOutput{Pairs: in.ProposedPairs}), nil
			}
			return mustJSON(t, matchOutput{Pairs: []matchPair{{OriginEvidenceID: in.OriginEvidenceID, CurrentEvidenceID: in.CurrentEvidenceIDs[pick], Decision: "SAME_VIEW", Reason: "vista"}}}), nil
		default:
			return compareJSON(t, claim(p.items["Sofá"], status), claim(p.items["Mesa"], "PRESENT_UNCHANGED")), nil
		}
	}
	f.submitPhotos()
	f.drain()
	run1, snapshot1 := f.classifyAndReport()
	if run1.Classification != "NORMAL" {
		t.Fatalf("first classification = %s %s", run1.Classification, run1.ReasonCodes)
	}
	firstJSON := string(snapshot1.CanonicalJSON)
	pick, status = 1, "MISSING"
	var scheduled int
	f.within(func(tx *gorm.DB) error {
		var err error
		scheduled, err = request_inventory_jobs.Reanalyze(f.ctx, tx, f.seed.TenantID, f.seed.InspectionID, f.now())
		return err
	})
	if scheduled != 1 {
		t.Fatalf("re-analysis scheduled %d jobs, want 1", scheduled)
	}
	f.clock.Add(int64(time.Second))
	f.drain()
	run2, snapshot2 := f.classifyAndReport()
	if run2.Classification != "CRITICAL" || snapshot2.VersionNumber != snapshot1.VersionNumber+1 {
		t.Fatalf("after re-analysis: classification %s, snapshot v%d (previous v%d)", run2.Classification, snapshot2.VersionNumber, snapshot1.VersionNumber)
	}
	var previous database.ReportSnapshot
	if err := f.h.AdminDB.Where("id=?", snapshot1.ID).Take(&previous).Error; err != nil || string(previous.CanonicalJSON) != firstJSON {
		t.Fatalf("previous snapshot changed: %v", err)
	}
	problems, _ := snapshotInventory(t, snapshot2)["problems"].([]any)
	if len(problems) != 1 {
		t.Fatalf("new snapshot problems = %v", problems)
	}
	missing := problems[0].(map[string]any)
	pairs, _ := missing["pairs"].([]any)
	if missing["status"] != "MISSING" || len(pairs) != 1 || pairs[0].(map[string]any)["current"] != second.String() {
		t.Fatalf("missing problem = %v, want the re-analysis pair with the second photo", missing)
	}
	triage, opened := f.triage()
	if !opened || triage.Classification != "CRITICAL" || triage.ReportVersion != snapshot2.VersionNumber {
		t.Fatalf("triage = %+v, want the latest CRITICAL classification", triage)
	}
}
