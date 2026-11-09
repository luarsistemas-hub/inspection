//go:build integration

package process_inventory_job

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	"inspection/services/inspection/internal/features/analysis/prompt"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
)

func TestTask03IT008AndIT039PinnedFurnishedScopeSurvivesVoicePresence(t *testing.T) {
	f := newFlow(t, "PHOTO")
	key, photo := f.voiceRoom("Cozinha")
	v2 := prompt.DefinitionForTaskVersion(string(prompt.InventoryCatalog), prompt.CurrentCatalogSchemaVersion)
	payload, digest, err := prompt.CanonicalizeTask(string(prompt.InventoryCatalog), v2)
	if err != nil {
		t.Fatal(err)
	}
	// These task-scoped v2 snapshots are pinned explicitly below. Keep them
	// older than the active seed so legacy fixtures selecting the latest prompt
	// do not accidentally pick up an unreleased schema.
	snapshot := database.AnalysisPromptSnapshot{ID: identity.NewID(), AnalysisType: prompt.RealEstate, Task: string(prompt.InventoryCatalog), DefinitionJSON: payload, CanonicalDigest: digest, SchemaVersion: prompt.CurrentCatalogSchemaVersion, CreatedAt: f.now().AddDate(-100, 0, 0)}
	var existing database.AnalysisPromptSnapshot
	if err := f.h.AdminDB.Where("analysis_type=? AND task=? AND canonical_digest=?", snapshot.AnalysisType, snapshot.Task, snapshot.CanonicalDigest).Take(&existing).Error; err == nil {
		snapshot = existing
	} else if err := f.h.AdminDB.Create(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	scopeDoc := json.RawMessage(`{"rooms":[{"key":"cozinha","name":"Cozinha","typeCode":"LIVING_ROOM","components":[{"key":"piso","typeCode":"FLOOR","label":"Piso"}]}]}`)
	scope := database.InspectionScopeSnapshot{ID: identity.NewID(), TenantID: f.seed.TenantID, InspectionID: f.seed.InspectionID, TemplateVersionID: f.seed.TemplateVersionID, Profile: "FURNISHED_PROPERTY", Document: scopeDoc, Digest: "fixture-scope", Version: 1, CreatedAt: f.now()}
	f.within(func(tx *gorm.DB) error { return tx.Create(&scope).Error })
	f.within(func(tx *gorm.DB) error {
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", f.seed.TenantID, f.seed.InspectionID).Update("inventory_prompt_snapshot_id", snapshot.ID).Error
	})
	f.gateway.Respond = func(request llm.StructuredRequest) (string, error) {
		return `{"environments":[{"typeCode":"KITCHEN","name":"Cozinha","items":[{"typeCode":"OTHER","name":"Piso","description":"Piso cerâmico claro visível","assessedScope":"área central visível","notAssessed":["área sob os móveis"],"componentKey":"cozinha.piso","material":null,"color":null,"quantity":null,"condition":"GOOD","observation":"OBSERVED","limitation":null,"confidenceBps":9000,"evidence":[{"mediaId":"` + photo.String() + `","frameId":null}],"voice":[]}]}]}`, nil
	}
	// No voice note: the pinned furnished profile still reaches the model.
	f.submitPhotos()
	job := f.catalogJob(key)
	if err := f.processor.Process(f.ctx, job, func(write func(*gorm.DB) error) error { return f.h.WithinTenant(f.ctx, f.seed.TenantID, write) }); err != nil {
		t.Fatalf("IT-008 catalog processing: %v", err)
	}
	calls := f.catalogRequests()
	if len(calls) != 1 {
		t.Fatalf("requests=%d, want 1", len(calls))
	}
	data := userData(t, calls[0])
	if data["assetProfile"] != "FURNISHED_PROPERTY" || len(data["expectedComponents"].([]any)) != 1 || len(calls[0].Images) != 1 {
		t.Fatalf("IT-008 request lost the pinned furnished visual scope: %v", data)
	}
	items := f.catalogItems()
	if len(items) != 1 || items[0].ComponentKey == nil || *items[0].ComponentKey != "cozinha.piso" {
		t.Fatalf("catalog items=%+v", items)
	}
	_ = key
}

func TestTask03RegenerationInheritsAndProcessesPinnedCatalogV2IT(t *testing.T) {
	f := newFlow(t, "PHOTO")
	key, photo := f.voiceRoom("Cozinha")
	_, revisionA := f.note(itNote{Key: key, Photo: photo, Text: "Piso declarado"})
	v2 := prompt.DefinitionForTaskVersion(string(prompt.InventoryCatalog), prompt.CurrentCatalogSchemaVersion)
	payload, digest, err := prompt.CanonicalizeTask(string(prompt.InventoryCatalog), v2)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := database.AnalysisPromptSnapshot{ID: identity.NewID(), AnalysisType: prompt.RealEstate, Task: string(prompt.InventoryCatalog), DefinitionJSON: payload, CanonicalDigest: digest, SchemaVersion: prompt.CurrentCatalogSchemaVersion, CreatedAt: f.now().AddDate(-100, 0, 0)}
	var existingSnapshot database.AnalysisPromptSnapshot
	if err := f.h.AdminDB.Where("analysis_type=? AND task=? AND canonical_digest=?", snapshot.AnalysisType, snapshot.Task, snapshot.CanonicalDigest).Take(&existingSnapshot).Error; err == nil {
		snapshot = existingSnapshot
	} else {
		if err := f.h.AdminDB.Create(&snapshot).Error; err != nil {
			t.Fatal(err)
		}
	}
	f.within(func(tx *gorm.DB) error {
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", f.seed.TenantID, f.seed.InspectionID).Update("inventory_prompt_snapshot_id", snapshot.ID).Error
	})
	f.gateway.Respond = func(request llm.StructuredRequest) (string, error) {
		data := userData(f.t, request)
		citations := []map[string]any{}
		voiceNotes, _ := data["voiceNotes"].([]any)
		for _, raw := range voiceNotes {
			note := raw.(map[string]any)
			citations = append(citations, map[string]any{"noteId": note["noteId"], "revisionId": note["revisionId"], "attributes": []string{"name"}})
		}
		response, err := json.Marshal(map[string]any{"environments": []any{map[string]any{"typeCode": "KITCHEN", "name": "Cozinha", "items": []any{map[string]any{"typeCode": "FLOOR", "name": "Piso", "description": "Superfície clara visível", "assessedScope": "área central", "notAssessed": []string{}, "componentKey": nil, "material": nil, "color": nil, "quantity": nil, "condition": "GOOD", "observation": "OBSERVED", "limitation": nil, "confidenceBps": 9000, "evidence": []any{map[string]any{"mediaId": photo.String(), "frameId": nil}}, "voice": citations}}}}})
		return string(response), err
	}
	f.submitPhotos()
	f.drain()
	var first database.InventoryJob
	if err := f.h.AdminDB.Where("tenant_id=? AND inspection_id=? AND kind='CATALOG' AND requirement_key=?", f.seed.TenantID, f.seed.InspectionID, key).Take(&first).Error; err != nil {
		t.Fatal(err)
	}
	if first.Status != "SUCCEEDED" || first.SchemaVersion != prompt.CurrentCatalogSchemaVersion || first.SchemaDigest == nil || *first.SchemaDigest != digest {
		t.Fatalf("initial v2 catalog did not process: %+v", first)
	}
	_, revisionB := f.note(itNote{Key: key, Photo: photo, Text: "Uma segunda declaração"})
	f.mustRegenerate(key, "task03-v2-regen", revisionA, revisionB)
	f.drain()
	var next database.InventoryJob
	if err := f.h.AdminDB.Where("tenant_id=? AND inspection_id=? AND kind='CATALOG' AND requirement_key=?", f.seed.TenantID, f.seed.InspectionID, key).Order("generation DESC").Take(&next).Error; err != nil {
		t.Fatal(err)
	}
	if next.Generation != first.Generation+1 || next.Status != "SUCCEEDED" || next.SchemaVersion != prompt.CurrentCatalogSchemaVersion || next.SchemaDigest == nil || *next.SchemaDigest != digest || next.PromptSnapshotID == nil || *next.PromptSnapshotID != snapshot.ID {
		t.Fatalf("regenerated catalog did not inherit and process v2: %+v", next)
	}
}

func TestTask03CatalogComponentsStayInTheJobRoomIT(t *testing.T) {
	f := newFlow(t, "PHOTO")
	roomRequirements := []capturecore.Requirement{inventorytest.EnvironmentRequirement("env:sala", "LIVING_ROOM", "Sala", false), inventorytest.EnvironmentRequirement("env:quarto", "BEDROOM", "Quarto", false)}
	f.requirements(roomRequirements...)
	photos := map[string]identity.ID{"env:sala": f.photo("env:sala"), "env:quarto": f.photo("env:quarto")}
	for key, photo := range photos {
		f.answer(key, []identity.ID{photo}, "")
	}
	v2 := prompt.DefinitionForTaskVersion(string(prompt.InventoryCatalog), prompt.CurrentCatalogSchemaVersion)
	payload, digest, err := prompt.CanonicalizeTask(string(prompt.InventoryCatalog), v2)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := database.AnalysisPromptSnapshot{ID: identity.NewID(), AnalysisType: prompt.RealEstate, Task: string(prompt.InventoryCatalog), DefinitionJSON: payload, CanonicalDigest: digest, SchemaVersion: prompt.CurrentCatalogSchemaVersion, CreatedAt: f.now().AddDate(-100, 0, 0)}
	var existingSnapshot database.AnalysisPromptSnapshot
	if err := f.h.AdminDB.Where("analysis_type=? AND task=? AND canonical_digest=?", snapshot.AnalysisType, snapshot.Task, snapshot.CanonicalDigest).Take(&existingSnapshot).Error; err == nil {
		snapshot = existingSnapshot
	} else {
		if err := f.h.AdminDB.Create(&snapshot).Error; err != nil {
			t.Fatal(err)
		}
	}
	scope := database.InspectionScopeSnapshot{ID: identity.NewID(), TenantID: f.seed.TenantID, InspectionID: f.seed.InspectionID, TemplateVersionID: f.seed.TemplateVersionID, Profile: "PROPERTY_COMPONENTS", Document: json.RawMessage(`{"rooms":[{"key":"sala","name":"Sala","typeCode":"LIVING_ROOM","components":[{"key":"piso","typeCode":"FLOOR","label":"Piso"}]},{"key":"quarto","name":"Quarto","typeCode":"BEDROOM","components":[{"key":"piso","typeCode":"FLOOR","label":"Piso"}]}]}`), Digest: "task03-room-scope", Version: 1, CreatedAt: f.now()}
	f.within(func(tx *gorm.DB) error {
		if err := tx.Create(&scope).Error; err != nil {
			return err
		}
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", f.seed.TenantID, f.seed.InspectionID).Update("inventory_prompt_snapshot_id", snapshot.ID).Error
	})
	f.gateway.Respond = func(request llm.StructuredRequest) (string, error) {
		data := userData(f.t, request)
		environment := data["environment"].(map[string]any)
		name := environment["name"].(string)
		photoID := photos["env:"+strings.ToLower(name)]
		items := []any{}
		for _, room := range []string{"sala", "quarto"} {
			items = append(items, map[string]any{"typeCode": "FLOOR", "name": "Piso " + room, "description": "Superfície visível", "assessedScope": "área central", "notAssessed": []string{}, "componentKey": room + ".piso", "material": nil, "color": nil, "quantity": nil, "condition": "GOOD", "observation": "OBSERVED", "limitation": nil, "confidenceBps": 9000, "evidence": []any{map[string]any{"mediaId": photoID.String(), "frameId": nil}}})
		}
		body, err := json.Marshal(map[string]any{"environments": []any{map[string]any{"typeCode": environment["typeCode"], "name": name, "items": items}}})
		return string(body), err
	}
	f.submitPhotos()
	f.drain()
	requests := f.catalogRequests()
	if len(requests) != 2 {
		t.Fatalf("catalog requests=%d, want one per room", len(requests))
	}
	for _, request := range requests {
		data := userData(f.t, request)
		components := data["expectedComponents"].([]any)
		if len(components) != 1 {
			t.Fatalf("request scope leaked other rooms: %v", components)
		}
		key := components[0].(map[string]any)["key"].(string)
		if !strings.HasSuffix(key, ".piso") || key != "sala.piso" && key != "quarto.piso" {
			t.Fatalf("unexpected room component key %q", key)
		}
	}
	for _, key := range []string{"env:sala", "env:quarto"} {
		var environment database.InventoryEnvironment
		if err := f.h.AdminDB.Where("tenant_id=? AND origin_version_id=(SELECT id FROM origins.origin_versions WHERE source_inspection_id=? AND source='CAPTURE') AND requirement_key=?", f.seed.TenantID, f.seed.InspectionID, key).Take(&environment).Error; err != nil {
			t.Fatal(err)
		}
		var items []database.InventoryItem
		if err := f.h.AdminDB.Where("tenant_id=? AND environment_id=?", f.seed.TenantID, environment.ID).Find(&items).Error; err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ComponentKey != nil && !strings.HasPrefix(*item.ComponentKey, strings.TrimPrefix(key, "env:")+".") {
				t.Fatalf("%s accepted another room's component %q", key, *item.ComponentKey)
			}
		}
	}
}

func TestTask03ExecutorKeepsCombinedTruncationDiagnosticsAndBlocksPromotionIT(t *testing.T) {
	f := newFlow(t, "PHOTO")
	key := "env:new:" + identity.NewID().String()
	f.requirements(inventorytest.EnvironmentRequirement(key, "LIVING_ROOM", "Sala", true))
	mediaIDs := make([]identity.ID, 0, maxRequestImages+1)
	for len(mediaIDs) < maxRequestImages+1 {
		mediaIDs = append(mediaIDs, f.photo(key))
	}
	f.answer(key, mediaIDs, "")
	items := make([]catalogItemSpec, maxItemsPerEnvironment+1)
	for i := range items {
		items[i] = catalogItemSpec{name: "Item " + strconv.Itoa(i), confidence: 9000, evidence: []string{mediaIDs[0].String()}}
	}
	f.gateway.Respond = func(llm.StructuredRequest) (string, error) { return catalogJSON(t, items...), nil }
	f.submitPhotos()
	f.drain()
	job := f.catalogJob(key)
	if job.Status != "SUCCEEDED" || job.ErrorCode == nil || *job.ErrorCode != "TRUNCATED_ITEMS" || job.CandidateTruncated != 1 || job.CandidateImageTruncated != 1 {
		t.Fatalf("executor lost combined truncation facts: %+v", job)
	}
}

func TestTask03IT009ForeignEvidenceDiagnosticAndIT052PinnedSchemaFailure(t *testing.T) {
	f := newFlow(t, "PHOTO")
	key, photo := f.voiceRoom("Sala")
	v2 := prompt.DefinitionForTaskVersion(string(prompt.InventoryCatalog), prompt.CurrentCatalogSchemaVersion)
	payload, digest, err := prompt.CanonicalizeTask(string(prompt.InventoryCatalog), v2)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := database.AnalysisPromptSnapshot{ID: identity.NewID(), AnalysisType: prompt.RealEstate, Task: string(prompt.InventoryCatalog), DefinitionJSON: payload, CanonicalDigest: digest, SchemaVersion: prompt.CurrentCatalogSchemaVersion, CreatedAt: f.now().AddDate(-100, 0, 0)}
	var existing database.AnalysisPromptSnapshot
	if err := f.h.AdminDB.Where("analysis_type=? AND task=? AND canonical_digest=?", snapshot.AnalysisType, snapshot.Task, snapshot.CanonicalDigest).Take(&existing).Error; err == nil {
		snapshot = existing
	} else if err := f.h.AdminDB.Create(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	f.within(func(tx *gorm.DB) error {
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", f.seed.TenantID, f.seed.InspectionID).Update("inventory_prompt_snapshot_id", snapshot.ID).Error
	})
	f.gateway.Respond = func(llm.StructuredRequest) (string, error) {
		return `{"environments":[{"typeCode":"LIVING_ROOM","name":"Sala","items":[{"typeCode":"OTHER","name":"Inventado","description":"Objeto visível","assessedScope":"área frontal","notAssessed":[],"componentKey":null,"material":null,"color":null,"quantity":null,"condition":"GOOD","observation":"OBSERVED","limitation":null,"confidenceBps":9100,"evidence":[{"mediaId":"` + identity.NewID().String() + `","frameId":null}]},{"typeCode":"OTHER","name":"Sofá","description":"Sofá visível","assessedScope":"parte frontal","notAssessed":[],"componentKey":null,"material":null,"color":null,"quantity":null,"condition":"GOOD","observation":"OBSERVED","limitation":null,"confidenceBps":9000,"evidence":[{"mediaId":"` + photo.String() + `","frameId":null},{"mediaId":"` + identity.NewID().String() + `","frameId":null}]}]}]}`, nil
	}
	f.submitPhotos()
	job := f.catalogJob(key)
	if err := f.processor.Process(f.ctx, job, func(write func(*gorm.DB) error) error { return f.h.WithinTenant(f.ctx, f.seed.TenantID, write) }); err != nil {
		t.Fatalf("IT-009 catalog processing: %v", err)
	}
	if err := f.h.AdminDB.Where("id=?", job.ID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.CandidateRejectedEvidence != 2 || job.CandidateReturned != 2 {
		t.Fatalf("IT-009 diagnostics=%+v", job)
	}
	if job.SchemaVersion != prompt.CurrentCatalogSchemaVersion || job.SchemaDigest == nil || *job.SchemaDigest != digest || job.PromptSnapshotID == nil || *job.PromptSnapshotID != snapshot.ID {
		t.Fatalf("analysis provenance was not pinned to the catalog generation: %+v", job)
	}
	if job.ModelRevision != "stub/vision" || job.MediaTransformDigest == nil || *job.MediaTransformDigest == "" {
		t.Fatalf("analysis execution provenance is incomplete: modelRevision=%q transformDigest=%v", job.ModelRevision, job.MediaTransformDigest)
	}

	f2 := newFlow(t, "PHOTO")
	key2, _ := f2.voiceRoom("Quarto")
	f2.submitPhotos()
	job2 := f2.catalogJob(key2)
	invalidDefinition := json.RawMessage(`{"incompatible":true}`)
	digestBytes := sha256.Sum256(invalidDefinition)
	digest = hex.EncodeToString(digestBytes[:])
	// Keep this deliberately invalid historical fixture out of "latest prompt"
	// selection performed by other packages sharing the integration database.
	invalidSnapshot := database.AnalysisPromptSnapshot{ID: identity.NewID(), AnalysisType: prompt.RealEstate, Task: string(prompt.InventoryCatalog), DefinitionJSON: invalidDefinition, CanonicalDigest: digest, SchemaVersion: 1, CreatedAt: f2.now().AddDate(-100, 0, 0)}
	var existingInvalid database.AnalysisPromptSnapshot
	if err := f2.h.AdminDB.Where("analysis_type=? AND task=? AND canonical_digest=?", invalidSnapshot.AnalysisType, invalidSnapshot.Task, invalidSnapshot.CanonicalDigest).Take(&existingInvalid).Error; err == nil {
		invalidSnapshot = existingInvalid
	} else if err := f2.h.AdminDB.Create(&invalidSnapshot).Error; err != nil {
		t.Fatal(err)
	}
	if err := f2.h.AdminDB.Model(&database.InventoryJob{}).Where("id=?", job2.ID).Updates(map[string]any{"prompt_snapshot_id": invalidSnapshot.ID, "schema_version": 1, "schema_digest": digest}).Error; err != nil {
		t.Fatal(err)
	}
	f2.drain()
	var failed database.InventoryJob
	if err := f2.h.AdminDB.Where("id=?", job2.ID).Take(&failed).Error; err != nil {
		t.Fatal(err)
	}
	if failed.Status != "FAILED" {
		t.Fatalf("IT-052 incompatible pinned schema status=%s", failed.Status)
	}
	if items := f2.catalogItems(); len(items) != 0 {
		t.Fatalf("IT-052 rendered fields from invalid schema: %+v", items)
	}
}

func TestTask03IT024LegacyPromptReaderRemainsAvailable(t *testing.T) {
	legacy := prompt.DefaultDefinitionForTask(string(prompt.InventoryCatalog))
	payload, digest, err := prompt.CanonicalizeTask(string(prompt.InventoryCatalog), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prompt.ParsePinnedTask(string(prompt.InventoryCatalog), payload, digest, 1); err != nil {
		t.Fatalf("IT-024 legacy reader rejected: %v", err)
	}
	fixture, err := os.ReadFile("testdata/checklist-v2/report-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var report reportcore.Snapshot
	if err := json.Unmarshal(fixture, &report); err != nil {
		t.Fatalf("IT-024 v2 report fixture failed to decode: %v", err)
	}
	if err := reportcore.Validate(report); err != nil {
		t.Fatalf("IT-024 v2 report fixture failed validation: %v", err)
	}
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL URL is required")
	}
}

func TestTask03IT039VoiceNotesDoNotReplacePinnedFurnishedVisualScope(t *testing.T) {
	f := newFlow(t, "PHOTO")
	key, photo := f.voiceRoom("Cozinha")
	noteID, revisionID := f.note(itNote{Key: key, Photo: photo, Text: "Piso declarado como madeira"})
	v2 := prompt.DefinitionForTaskVersion(string(prompt.InventoryCatalog), prompt.CurrentCatalogSchemaVersion)
	_, digest, err := prompt.CanonicalizeTask(string(prompt.InventoryCatalog), v2)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot database.AnalysisPromptSnapshot
	if err := f.h.AdminDB.Where("analysis_type=? AND task=? AND canonical_digest=?", prompt.RealEstate, prompt.InventoryCatalog, digest).Take(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	scope := database.InspectionScopeSnapshot{ID: identity.NewID(), TenantID: f.seed.TenantID, InspectionID: f.seed.InspectionID, TemplateVersionID: f.seed.TemplateVersionID, Profile: "FURNISHED_PROPERTY", Document: json.RawMessage(`{"rooms":[{"key":"cozinha","name":"Cozinha","typeCode":"LIVING_ROOM","components":[{"key":"piso","typeCode":"FLOOR","label":"Piso"}]}]}`), Digest: "fixture-scope", Version: 1, CreatedAt: f.now()}
	f.within(func(tx *gorm.DB) error { return tx.Create(&scope).Error })
	f.within(func(tx *gorm.DB) error {
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", f.seed.TenantID, f.seed.InspectionID).Update("inventory_prompt_snapshot_id", snapshot.ID).Error
	})
	f.gateway.Respond = func(llm.StructuredRequest) (string, error) {
		return `{"environments":[{"typeCode":"KITCHEN","name":"Cozinha","items":[]}]}`, nil
	}
	f.submitPhotos()
	job := f.catalogJob(key)
	if err := f.processor.Process(f.ctx, job, func(write func(*gorm.DB) error) error { return f.h.WithinTenant(f.ctx, f.seed.TenantID, write) }); err != nil {
		t.Fatal(err)
	}
	calls := f.catalogRequests()
	if len(calls) != 1 {
		t.Fatalf("requests=%d, want 1", len(calls))
	}
	data := userData(t, calls[0])
	if data["assetProfile"] != "FURNISHED_PROPERTY" || data["scope"] != prompt.FurnishedCatalogScope || len(data["expectedComponents"].([]any)) != 1 {
		t.Fatalf("voice changed the pinned visual scope: %v", data)
	}
	notes := data["voiceNotes"].([]any)
	if len(notes) != 1 {
		t.Fatalf("voice notes=%v, want one declaration", notes)
	}
	entry := notes[0].(map[string]any)
	if entry["noteId"] != noteID.String() || entry["revisionId"] != revisionID.String() || entry["sourceMediaId"] != photo.String() {
		t.Fatalf("voice citation=%v", entry)
	}
}
