// Package inventorytest provides an in-memory database, seeds and small fakes
// for inventory analysis tests. Only test files import it.
package inventorytest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/prompt"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open returns an isolated in-memory SQLite database with the tables touched
// by inventory jobs, classification and reports.
func Open(t testing.TB) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, schema := range []string{"inspections", "templates", "capture", "origins", "analysis", "media", "messaging", "assets", "reports"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	models := []any{&database.Inspection{}, &database.TemplateVersion{}, &database.CaptureDraft{}, &database.RequirementAnswer{}, &database.Responsibility{}, &database.ReferenceSnapshot{}, &database.Origin{}, &database.OriginVersion{}, &database.OriginEvidence{}, &database.InventoryEnvironment{}, &database.InventoryItem{}, &database.InventoryItemEvidence{}, &database.InventoryJob{}, &database.InventoryEvidencePair{}, &database.InventoryItemResult{}, &database.AnalysisPromptSnapshot{}, &database.MediaObject{}, &database.MediaDerivative{}, &database.VideoPreparationJob{}, &database.VideoFrame{}, &database.VideoSource{}, &database.OutboxIntent{}, &database.AnalysisRun{}, &database.FindingRecord{}, &database.ComparisonJob{}, &database.ClassificationRun{}}
	for _, model := range models {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatalf("migrate %T: %v", model, err)
		}
	}
	// AutoMigrate cannot create indexes inside attached schemas; the uniqueness
	// that makes the handlers idempotent mirrors the PostgreSQL migrations.
	for _, statement := range []string{
		"CREATE UNIQUE INDEX analysis.uq_inventory_job_input ON inventory_jobs(tenant_id, inspection_id, kind, requirement_key, generation)",
		"CREATE UNIQUE INDEX analysis.uq_inventory_pair_identity ON inventory_evidence_pairs(tenant_id, job_id, item_identity_id, origin_media_id, origin_frame_id)",
		"CREATE UNIQUE INDEX analysis.uq_inventory_result_item ON inventory_item_results(tenant_id, job_id, item_identity_id)",
		"CREATE UNIQUE INDEX origins.uq_inventory_environment_identity ON inventory_environments(tenant_id, origin_version_id, identity_id)",
		"CREATE UNIQUE INDEX origins.uq_inventory_item_identity ON inventory_items(tenant_id, origin_version_id, identity_id)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	return db
}

// Seed is one tenant with one v2 inspection and its submitted capture.
type Seed struct {
	DB                                                  *gorm.DB
	Store                                               *MemoryStore
	Now                                                 time.Time
	Tenant, Asset, Inspection, Responsibility, Template identity.ID
	CaptureDraft, CatalogPrompt, ComparePrompt          identity.ID
}

// InspectionOptions shapes the seeded inspection.
type InspectionOptions struct {
	MediaKind         string // PHOTO by default
	SchemaVersion     int    // 2 by default
	CaptureStatus     string // SUBMITTED by default
	NoInventoryPrompt bool
	Requirements      []capturecore.Requirement
}

// New seeds prompts, a template version, an inspection and its capture draft.
func New(t testing.TB, options InspectionOptions) *Seed {
	t.Helper()
	db := Open(t)
	s := &Seed{DB: db, Store: NewMemoryStore(), Now: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC), Tenant: identity.NewID(), Asset: identity.NewID(), Inspection: identity.NewID(), Responsibility: identity.NewID(), Template: identity.NewID(), CaptureDraft: identity.NewID()}
	if options.MediaKind == "" {
		options.MediaKind = "PHOTO"
	}
	if options.SchemaVersion == 0 {
		options.SchemaVersion = 2
	}
	if options.CaptureStatus == "" {
		options.CaptureStatus = "SUBMITTED"
	}
	s.CatalogPrompt = s.prompt(t, "INVENTORY_CATALOG")
	s.ComparePrompt = s.prompt(t, "INVENTORY_COMPARE")
	findings := s.prompt(t, "FINDINGS")
	s.create(t, &database.TemplateVersion{ID: s.Template, TenantID: s.Tenant, TemplateID: identity.NewID(), VersionNumber: 1, SchemaVersion: options.SchemaVersion, DefinitionJSON: json.RawMessage(`{}`), CanonicalDigest: "digest", Status: "ACTIVE", IdempotencyKey: "inventory-test-" + s.Template.String(), PublishedAt: s.Now, CreatedBy: identity.NewID()})
	var inventoryPrompt *identity.ID
	if !options.NoInventoryPrompt {
		inventoryPrompt = &s.CatalogPrompt
	}
	s.create(t, &database.Inspection{ID: s.Inspection, TenantID: s.Tenant, BusinessUnitID: identity.NewID(), AssetID: s.Asset, ParticipantID: identity.NewID(), TemplateID: identity.NewID(), TemplateVersionID: s.Template, AnalysisPromptSnapshotID: findings, InventoryPromptSnapshotID: inventoryPrompt, Source: "MANUAL", SourceKey: "inventory-test-" + s.Inspection.String(), Status: "SUBMITTED", MediaKind: options.MediaKind, DueAt: s.Now, DeadlineAt: s.Now.Add(time.Hour), ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), Version: 1, CreatedAt: s.Now, UpdatedAt: s.Now})
	s.create(t, &database.Responsibility{ID: s.Responsibility, TenantID: s.Tenant, InspectionID: s.Inspection, ParticipantID: identity.NewID(), Status: "SUBMITTED", Version: 1, CreatedAt: s.Now, UpdatedAt: s.Now})
	requirements, err := json.Marshal(options.Requirements)
	if err != nil {
		t.Fatal(err)
	}
	s.create(t, &database.CaptureDraft{ID: s.CaptureDraft, TenantID: s.Tenant, ResponsibilityID: s.Responsibility, Kind: "INSPECTION", MediaKind: options.MediaKind, TemplateVersionID: s.Template, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: requirements, Status: options.CaptureStatus, Version: 1, CreatedAt: s.Now, UpdatedAt: s.Now})
	s.create(t, &database.ReferenceSnapshot{ID: identity.NewID(), TenantID: s.Tenant, InspectionID: s.Inspection, ComparisonMode: "CURRENT_ONLY", Payload: json.RawMessage(`{}`), CreatedAt: s.Now})
	return s
}

func (s *Seed) prompt(t testing.TB, task string) identity.ID {
	t.Helper()
	payload, digest, err := prompt.CanonicalizeTask(task, prompt.DefaultDefinitionForTask(task))
	if err != nil {
		t.Fatal(err)
	}
	id := identity.NewID()
	s.create(t, &database.AnalysisPromptSnapshot{ID: id, AnalysisType: "REAL_ESTATE", Task: task, DefinitionJSON: payload, CanonicalDigest: digest, CreatedAt: s.Now})
	return id
}

func (s *Seed) create(t testing.TB, row any) {
	t.Helper()
	if err := s.DB.Create(row).Error; err != nil {
		t.Fatalf("seed %T: %v", row, err)
	}
}

// EnvironmentRequirement is a v2 capture requirement for one environment.
func EnvironmentRequirement(key, typeCode, name string, added bool) capturecore.Requirement {
	requirement := capturecore.Requirement{Key: key, Section: typeCode, Label: name, EvidenceKind: "PHOTO", Required: true, ImpossibilityAllowed: true, MinimumMedia: 1, MaximumMedia: 20, EnvironmentTypeCode: typeCode, EnvironmentName: name, AddedDuringCapture: added, Expected: !added}
	if strings.HasPrefix(key, "env:") && !strings.HasPrefix(key, "env:new:") {
		requirement.EnvironmentIdentityID = strings.TrimPrefix(key, "env:")
	}
	if strings.HasPrefix(key, "env:new:") {
		requirement.EnvironmentIdentityID = strings.TrimPrefix(key, "env:new:")
	}
	return requirement
}

// Answer records the submitted answer of one requirement.
func (s *Seed) Answer(t testing.TB, key string, media []identity.ID, impossibility string) {
	t.Helper()
	if media == nil {
		media = []identity.ID{}
	}
	ids, _ := json.Marshal(media)
	s.create(t, &database.RequirementAnswer{ID: identity.NewID(), TenantID: s.Tenant, DraftID: s.CaptureDraft, RequirementKey: key, MediaIDs: ids, Flags: json.RawMessage(`[]`), ImpossibilityReason: impossibility, Version: 1, CreatedAt: s.Now, UpdatedAt: s.Now})
}

// Photo stores a READY photo with its display derivative.
func (s *Seed) Photo(t testing.TB, key string) identity.ID {
	t.Helper()
	id := identity.NewID()
	object := "display/" + id.String() + ".jpg"
	s.Store.Put(object, []byte("jpeg-"+id.String()))
	s.create(t, &database.MediaObject{ID: id, TenantID: s.Tenant, ResponsibilityID: s.Responsibility, ObjectKey: "original/" + id.String(), ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 10, Status: "READY", MediaKind: "PHOTO", IdempotencyKey: "photo-" + id.String(), RequirementKey: key, AttentionItems: json.RawMessage(`[]`), DeviceContext: json.RawMessage(`{}`), Flags: json.RawMessage(`[]`), CreatedAt: s.Now})
	s.create(t, &database.MediaDerivative{ID: identity.NewID(), TenantID: s.Tenant, MediaID: id, ObjectKey: object, Kind: "DISPLAY", SHA256: strings.Repeat("b", 64), ContentType: "image/jpeg", Width: 10, Height: 10, SizeBytes: 10, Profile: "display", CreatedAt: s.Now})
	return id
}

// Clip stores a READY video whose latest preparation has the given status and
// representative frames.
func (s *Seed) Clip(t testing.TB, key, preparationStatus string, frames int) (identity.ID, identity.ID, []identity.ID) {
	t.Helper()
	id, prep := identity.NewID(), identity.NewID()
	s.create(t, &database.MediaObject{ID: id, TenantID: s.Tenant, ResponsibilityID: s.Responsibility, ObjectKey: "original/" + id.String(), ContentType: "video/mp4", SHA256: strings.Repeat("c", 64), SizeBytes: 10, Status: "READY", MediaKind: "VIDEO", IdempotencyKey: "clip-" + id.String(), RequirementKey: key, AttentionItems: json.RawMessage(`[]`), DeviceContext: json.RawMessage(`{}`), Flags: json.RawMessage(`[]`), CreatedAt: s.Now})
	requirementKey := key
	s.create(t, &database.VideoPreparationJob{ID: prep, TenantID: s.Tenant, MediaID: id, RequirementKey: &requirementKey, Status: preparationStatus, Phase: "DONE", PreparationVersion: 2, Generation: 1, NextAttemptAt: s.Now, PendingObjectKeys: []byte(`[]`), CreatedAt: s.Now})
	frameIDs := make([]identity.ID, 0, frames)
	for i := 0; i < frames; i++ {
		frameID := identity.NewID()
		object := "frames/" + frameID.String() + ".jpg"
		s.Store.Put(object, []byte("frame-"+frameID.String()))
		s.create(t, &database.VideoFrame{ID: frameID, TenantID: s.Tenant, SourceMediaID: id, PreparationID: prep, PTSMicros: int64(i+1) * 1_000_000, SHA256: fmt.Sprintf("%064d", i), ObjectKey: object, Width: 10, Height: 10, Role: "CORE", Representative: true, Quality: "GOOD", SizeBytes: 10, CreatedAt: s.Now})
		frameIDs = append(frameIDs, frameID)
	}
	return id, prep, frameIDs
}

// BaselineItem describes an included item of a pinned version.
type BaselineItem struct {
	Name, TypeCode, Condition, Decision string
	Declared                            bool
	// Evidence is the number of origin photos citing the item.
	Evidence int
}

// BaselineEnvironment describes an environment of a pinned version.
type BaselineEnvironment struct {
	Name, TypeCode string
	Items          []BaselineItem
}

// Baseline is a seeded, activated inventory version.
type Baseline struct {
	Version      identity.ID
	Environments []database.InventoryEnvironment
	Items        []database.InventoryItem
	Evidence     []database.InventoryItemEvidence
}

// PinBaseline seeds an ACTIVE inventory version and pins the inspection to it.
func (s *Seed) PinBaseline(t testing.TB, environments ...BaselineEnvironment) Baseline {
	t.Helper()
	origin := database.Origin{ID: identity.NewID(), TenantID: s.Tenant, AssetID: s.Asset, Version: 1, CreatedAt: s.Now, UpdatedAt: s.Now}
	baseline := Baseline{Version: identity.NewID()}
	origin.ActiveVersionID = &baseline.Version
	s.create(t, &origin)
	purpose, activated := "MOVE_IN", s.Now.Add(-24*time.Hour)
	s.create(t, &database.OriginVersion{ID: baseline.Version, TenantID: s.Tenant, OriginID: origin.ID, VersionNumber: 1, Purpose: &purpose, Source: "CAPTURE", HasInventory: true, Status: "ACTIVE", IdempotencyKey: "baseline-" + baseline.Version.String(), CreatedAt: s.Now, ActivatedAt: &activated})
	for position, spec := range environments {
		env := database.InventoryEnvironment{ID: identity.NewID(), TenantID: s.Tenant, OriginVersionID: baseline.Version, IdentityID: identity.NewID(), TypeCode: spec.TypeCode, Name: spec.Name, Expected: true, Decision: "ACCEPTED", Position: position, Version: 1, CreatedAt: s.Now, UpdatedAt: s.Now}
		s.create(t, &env)
		baseline.Environments = append(baseline.Environments, env)
		for _, itemSpec := range spec.Items {
			decision := itemSpec.Decision
			if decision == "" {
				decision = "ACCEPTED"
			}
			typeCode := itemSpec.TypeCode
			if typeCode == "" {
				typeCode = "OTHER"
			}
			item := database.InventoryItem{ID: identity.NewID(), TenantID: s.Tenant, OriginVersionID: baseline.Version, EnvironmentID: env.ID, IdentityID: identity.NewID(), TypeCode: typeCode, Name: itemSpec.Name, Condition: itemSpec.Condition, Observation: "OBSERVED", Origin: "AI", Declared: itemSpec.Declared, Decision: decision, Version: 1, CreatedAt: s.Now, UpdatedAt: s.Now}
			s.create(t, &item)
			baseline.Items = append(baseline.Items, item)
			for i := 0; i < itemSpec.Evidence; i++ {
				photo := s.Photo(t, "origin")
				evidence := database.InventoryItemEvidence{ID: identity.NewID(), TenantID: s.Tenant, ItemID: item.ID, Kind: "PHOTO", MediaID: photo, Position: i, CreatedAt: s.Now}
				s.create(t, &evidence)
				baseline.Evidence = append(baseline.Evidence, evidence)
			}
		}
	}
	if err := s.DB.Model(&database.ReferenceSnapshot{}).Where("tenant_id=? AND inspection_id=?", s.Tenant, s.Inspection).Updates(map[string]any{"reference_version_id": baseline.Version, "comparison_mode": "FIXED_ORIGIN"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Model(&database.Inspection{}).Where("id=?", s.Inspection).Update("inventory_prompt_snapshot_id", s.ComparePrompt).Error; err != nil {
		t.Fatal(err)
	}
	return baseline
}

// EnvironmentKey is the capture key of a pinned environment.
func EnvironmentKey(env database.InventoryEnvironment) string {
	return "env:" + env.IdentityID.String()
}

// Job inserts a PENDING inventory job.
func (s *Seed) Job(t testing.TB, kind, key string, versionID identity.ID, environment *identity.ID) database.InventoryJob {
	t.Helper()
	snapshot := s.CatalogPrompt
	if kind == "COMPARE" {
		snapshot = s.ComparePrompt
	}
	job := database.InventoryJob{ID: identity.NewID(), TenantID: s.Tenant, InspectionID: s.Inspection, OriginVersionID: versionID, Kind: kind, RequirementKey: key, EnvironmentIdentityID: environment, Generation: 1, Status: "PENDING", NextAttemptAt: s.Now, PromptSnapshotID: &snapshot, CreatedAt: s.Now, UpdatedAt: s.Now}
	s.create(t, &job)
	return job
}

// Outbox returns the outbox event types recorded so far.
func (s *Seed) Outbox(t testing.TB) []database.OutboxIntent {
	t.Helper()
	var rows []database.OutboxIntent
	if err := s.DB.Order("created_at ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

// Within runs fn in the seed database, standing in for the tenant runner.
func (s *Seed) Within(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
	return s.DB.Transaction(fn)
}

// Gateway is a scripted llm.Gateway that records every request.
type Gateway struct {
	mu       sync.Mutex
	Requests []llm.StructuredRequest
	Respond  func(llm.StructuredRequest) (string, error)
}

// CompleteStructured records the request and returns the scripted response.
func (g *Gateway) CompleteStructured(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
	g.mu.Lock()
	g.Requests = append(g.Requests, request)
	respond := g.Respond
	g.mu.Unlock()
	if respond == nil {
		return llm.StructuredResult{}, fmt.Errorf("unexpected gateway call %s", request.Mode)
	}
	body, err := respond(request)
	if err != nil {
		return llm.StructuredResult{}, err
	}
	return llm.StructuredResult{JSON: []byte(body), Provider: "stub", Model: "stub/vision", GatewayRequestID: "stub-" + request.Mode}, nil
}

// Calls returns the recorded requests of one mode.
func (g *Gateway) Calls(mode string) []llm.StructuredRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []llm.StructuredRequest
	for _, request := range g.Requests {
		if request.Mode == mode {
			out = append(out, request)
		}
	}
	return out
}

// MemoryStore is an in-memory object store reader.
type MemoryStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{objects: map[string][]byte{}} }

// Store wraps the memory store as an objectstore.Store.
func (m *MemoryStore) Store() objectstore.Store {
	return objectstore.Store{Bucket: "test", Client: memoryClient{m}}
}

// Put stores an object.
func (m *MemoryStore) Put(key string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = data
}

type memoryClient struct{ m *MemoryStore }

func (c memoryClient) Get(_ context.Context, _ string, key string) (io.ReadCloser, error) {
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	data, ok := c.m.objects[key]
	if !ok {
		return nil, fmt.Errorf("object %s not found", key)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (memoryClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "", fmt.Errorf("not supported")
}
func (memoryClient) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "", fmt.Errorf("not supported")
}
func (memoryClient) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return fmt.Errorf("not supported")
}
func (memoryClient) AbortMultipart(context.Context, string, string, string) error {
	return fmt.Errorf("not supported")
}
func (memoryClient) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{}, fmt.Errorf("not supported")
}
func (memoryClient) Put(context.Context, string, string, io.Reader, int64, string) error {
	return fmt.Errorf("not supported")
}
