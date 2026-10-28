//go:build integration

package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/prompt"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	capturebeginvideo "inspection/services/inspection/internal/features/capture/begin_video_capture"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	originresolve "inspection/services/inspection/internal/features/origins/resolve_reference"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	"inspection/services/inspection/internal/features/templates/catalog"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestUT100UT101UT103UT104UT174InspectionCreatePinsRequirementsAndPromptSnapshots(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })

	for _, task := range []prompt.Task{prompt.InventoryCatalog, prompt.InventoryCompare} {
		definition, digest, err := prompt.CanonicalizeTask(string(task), prompt.DefaultDefinitionForTask(string(task)))
		if err != nil {
			t.Fatal(err)
		}
		row := database.AnalysisPrompt{ID: identity.NewID(), AnalysisType: prompt.RealEstate, Task: string(task), DefinitionJSON: definition, CanonicalDigest: digest, Revision: 1, UpdatedBy: identity.NewID(), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if err := h.AdminDB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "analysis_type"}, {Name: "task"}}, DoNothing: true}).Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := h.AdminDB.Exec("INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, TRUE, 1, ?, ?, ?, '') ON CONFLICT (id) DO UPDATE SET enabled=TRUE", time.Now().UTC(), "Task 3 isolated integration fixture", "task3-create-video").Error; err != nil {
		t.Fatal(err)
	}

	t.Run("UT-100 four pinned photo environments and COMPARE snapshot", func(t *testing.T) {
		fx := newTask3CreateFixture(t, h, "PHOTO", 2, true)
		view := fx.create(t, catalog.FixedOrigin, &fx.referenceID, "ut100")
		assertRequirements(t, h, fx.seed.TenantID, view.Responsibility.ID, "PHOTO", fx.requirements)
		assertPromptAndReference(t, h, fx.seed.TenantID, view.Inspection.ID, string(prompt.InventoryCompare), &fx.referenceID)
	})
	t.Run("UT-101 v2 video environments", func(t *testing.T) {
		fx := newTask3CreateFixture(t, h, "VIDEO", 2, true)
		view := fx.create(t, catalog.FixedOrigin, &fx.referenceID, "ut101")
		requirements := loadRequirements(t, h, fx.seed.TenantID, view.Responsibility.ID)
		if len(requirements) != 4 {
			t.Fatalf("requirements=%+v, want four", requirements)
		}
		for _, requirement := range requirements {
			if requirement.EvidenceKind != "VIDEO" || requirement.MaximumMedia != 3 || requirement.Key == "video:main" {
				t.Fatalf("v2 video requirement=%+v", requirement)
			}
		}
	})
	t.Run("UT-103 no inventory keeps requirements empty and v2 schema", func(t *testing.T) {
		fx := newTask3CreateFixture(t, h, "PHOTO", 2, false)
		view := fx.create(t, catalog.ChecklistOnly, nil, "ut103")
		if got := loadRequirements(t, h, fx.seed.TenantID, view.Responsibility.ID); len(got) != 0 {
			t.Fatalf("requirements=%+v; want none before declarations", got)
		}
		var draft database.CaptureDraft
		if err := h.WithinTenant(ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND responsibility_id=?", fx.seed.TenantID, view.Responsibility.ID).Take(&draft).Error
		}); err != nil {
			t.Fatal(err)
		}
		if draft.TemplateVersionID != fx.templateVersionID || view.Inspection.InventoryPromptSnapshotID == nil {
			t.Fatalf("v2 draft/inspection snapshot mismatch: draft=%+v inspection=%+v", draft, view.Inspection)
		}
	})
	t.Run("UT-174 CATALOG and COMPARE pins", func(t *testing.T) {
		noReference := newTask3CreateFixture(t, h, "PHOTO", 2, false)
		without := noReference.create(t, catalog.ChecklistOnly, nil, "ut174-catalog")
		assertPromptAndReference(t, h, noReference.seed.TenantID, without.Inspection.ID, string(prompt.InventoryCatalog), nil)
		withReference := newTask3CreateFixture(t, h, "PHOTO", 2, true)
		with := withReference.create(t, catalog.FixedOrigin, &withReference.referenceID, "ut174-compare")
		assertPromptAndReference(t, h, withReference.seed.TenantID, with.Inspection.ID, string(prompt.InventoryCompare), &withReference.referenceID)
	})
	t.Run("v1 template rejects inventory reference before creating draft", func(t *testing.T) {
		fx := newTask3CreateFixture(t, h, "PHOTO", 1, true)
		_, err := fx.createErr(catalog.FixedOrigin, &fx.referenceID, "v1-inventory")
		if !apperror.Is(err, apperror.InvalidState) || err.Error() != "CATALOG_ORIGIN_FIRST" {
			t.Fatalf("v1 template with inventory returned %v; want InvalidState CATALOG_ORIGIN_FIRST", err)
		}
	})
}

type task3CreateFixture struct {
	h                 *harness.Harness
	seed              harness.Task06Fixture
	bus               *mediator.Bus
	version           templateresolve.Result
	resolvedVersion   *templateresolve.Result
	reference         originresolve.Result
	templateVersionID identity.ID
	referenceID       identity.ID
	requirements      []capturecore.Requirement
	service           Service
}

func newTask3CreateFixture(t *testing.T, h *harness.Harness, mediaKind string, schemaVersion int, inventory bool) *task3CreateFixture {
	t.Helper()
	seed, err := h.SeedTask06Fixture(context.Background(), harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	templateID, templateVersionID, referenceID := identity.NewID(), identity.NewID(), identity.NewID()
	document, err := json.Marshal(catalog.TemplateDocument{SchemaVersion: schemaVersion, IsVideo: mediaKind == "VIDEO", SegmentVersionID: seed.SegmentVersionID.String(), ParticipantRoles: []string{"TENANT_PARTICIPANT"}, DefaultComparisonMode: catalog.ChecklistOnly, AnalysisType: "REAL_ESTATE"})
	if err != nil {
		t.Fatal(err)
	}
	asset := database.Asset{ID: seed.AssetID, TenantID: seed.TenantID, BusinessUnitID: seed.BusinessUnitID, TemplateID: &templateID, Status: "ACTIVE", Version: 1, PolicyOverrides: json.RawMessage(`{}`)}
	participant := database.Participant{ID: seed.ParticipantID, TenantID: seed.TenantID, BusinessUnitID: seed.BusinessUnitID, Status: "ACTIVE", Version: 1}
	version := templateresolve.Result{Template: database.Template{ID: templateID, TenantID: seed.TenantID}, Version: database.TemplateVersion{ID: templateVersionID, TenantID: seed.TenantID, TemplateID: templateID, SchemaVersion: schemaVersion, DefinitionJSON: document}}
	requirements := make([]capturecore.Requirement, 4)
	for i := range requirements {
		id := identity.NewID()
		requirements[i] = capturecore.Requirement{Key: "env:" + id.String(), EvidenceKind: mediaKind, EnvironmentIdentityID: id.String(), EnvironmentTypeCode: "BEDROOM", EnvironmentName: "Quarto " + string(rune('1'+i)), Required: true, Expected: true, ImpossibilityAllowed: true, MinimumMedia: 1, MaximumMedia: map[string]int{"PHOTO": 20, "VIDEO": 3}[mediaKind]}
	}
	reference := originresolve.Result{Version: database.OriginVersion{ID: referenceID, OriginID: identity.NewID(), TenantID: seed.TenantID, Status: "ACTIVE", MediaKind: "PHOTO", HasInventory: inventory}, Requirements: requirements}
	bus := mediator.New()
	register := func(message any, handler func(context.Context, any) (any, error)) {
		t.Helper()
		if err := bus.RegisterQuery(message, handler); err != nil {
			t.Fatal(err)
		}
	}
	register(assetget.Query{}, func(_ context.Context, raw any) (any, error) { return assetcore.View{Asset: asset}, nil })
	register(participantget.Query{}, func(_ context.Context, raw any) (any, error) {
		return participantcore.ParticipantView{Participant: participant, Selected: []identity.ID{seed.ContactID}}, nil
	})
	resolvedVersion := &version
	register(templateresolve.Query{}, func(_ context.Context, raw any) (any, error) { return *resolvedVersion, nil })
	register(originresolve.Query{}, func(_ context.Context, raw any) (any, error) { return reference, nil })
	return &task3CreateFixture{h: h, seed: seed, bus: bus, version: version, resolvedVersion: resolvedVersion, reference: reference, templateVersionID: templateVersionID, referenceID: referenceID, requirements: requirements, service: Service{DB: h.DB, Bus: bus}}
}

func (f *task3CreateFixture) setRequirements(requirements []capturecore.Requirement) {
	f.requirements = requirements
	f.reference.Requirements = requirements
}

func (f *task3CreateFixture) create(t *testing.T, mode catalog.ComparisonMode, referenceID *identity.ID, key string) View {
	t.Helper()
	view, err := f.createErr(mode, referenceID, key)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func (f *task3CreateFixture) createErr(mode catalog.ComparisonMode, referenceID *identity.ID, key string) (View, error) {
	now := time.Now().UTC()
	return f.service.Create(context.Background(), CreateInput{TenantID: f.seed.TenantID, AssetID: f.seed.AssetID, ParticipantID: f.seed.ParticipantID, TemplateID: &f.version.Template.ID, TemplateVersionID: &f.templateVersionID, ComparisonMode: mode, ReferenceVersionID: referenceID, Source: SourceScheduled, SourceKey: "task3-create-" + key, DueAt: now.Add(time.Hour), DeadlineAt: now.Add(24 * time.Hour)})
}

func loadRequirements(t *testing.T, h *harness.Harness, tenantID, responsibilityID identity.ID) []capturecore.Requirement {
	t.Helper()
	var draft database.CaptureDraft
	if err := h.WithinTenant(context.Background(), tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).Take(&draft).Error
	}); err != nil {
		t.Fatal(err)
	}
	var requirements []capturecore.Requirement
	if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
		t.Fatal(err)
	}
	return requirements
}

func assertRequirements(t *testing.T, h *harness.Harness, tenantID, responsibilityID identity.ID, kind string, expected []capturecore.Requirement) {
	t.Helper()
	got := loadRequirements(t, h, tenantID, responsibilityID)
	if len(got) != 4 {
		t.Fatalf("requirements=%+v; want 4", got)
	}
	for _, requirement := range got {
		if requirement.EvidenceKind != kind || !requirement.Expected || requirement.MinimumMedia != 1 || requirement.MaximumMedia != 20 || kind != "PHOTO" || requirement.Key[:4] != "env:" {
			t.Fatalf("v2 photo requirement=%+v", requirement)
		}
	}
	if !requirementsEqual(got, expected) {
		t.Fatalf("requirements=%+v; want %+v", got, expected)
	}
}

func assertPromptAndReference(t *testing.T, h *harness.Harness, tenantID, inspectionID identity.ID, task string, referenceID *identity.ID) {
	t.Helper()
	if err := h.WithinTenant(context.Background(), tenantID, func(tx *gorm.DB) error {
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", tenantID, inspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		if inspection.InventoryPromptSnapshotID == nil {
			return apperror.New(apperror.InvalidState, "inventoryPromptSnapshotId", "inventory prompt snapshot missing")
		}
		var snapshot database.AnalysisPromptSnapshot
		if err := tx.Where("id=? AND task=?", *inspection.InventoryPromptSnapshotID, task).Take(&snapshot).Error; err != nil {
			return err
		}
		var reference database.ReferenceSnapshot
		if err := tx.Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Take(&reference).Error; err != nil {
			return err
		}
		if (referenceID == nil && reference.ReferenceVersionID != nil) || (referenceID != nil && (reference.ReferenceVersionID == nil || *reference.ReferenceVersionID != *referenceID)) {
			return apperror.New(apperror.InvalidState, "referenceVersionId", "reference snapshot did not pin selected version")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func requirementsEqual(a, b []capturecore.Requirement) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func TestIT012V1PhotoCreateKeepsLegacyDraftAndNoInventoryWork(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	h, err := harness.NewFromEnv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	fx := newTask3CreateFixture(t, h, "PHOTO", 1, false)
	definition, err := json.Marshal(catalog.TemplateDocument{
		SchemaVersion: 1, SegmentVersionID: fx.seed.SegmentVersionID.String(),
		ParticipantRoles: []string{"TENANT_PARTICIPANT"}, DefaultComparisonMode: catalog.ChecklistOnly,
		AnalysisType: "REAL_ESTATE",
		Requirements: []catalog.CaptureRequirement{
			{Key: "overview", Section: "General", Label: "Overview", EvidenceKind: "PHOTO", MinimumCount: 1, MaximumCount: 2, Required: true, CaptureSourcePolicy: "CAMERA_DEFAULT"},
			{Key: "detail", Section: "Details", Label: "Detail", EvidenceKind: "PHOTO", MinimumCount: 2, MaximumCount: 5, Required: true, CaptureSourcePolicy: "CAMERA_DEFAULT"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fx.resolvedVersion.Version.DefinitionJSON = definition
	view := fx.create(t, catalog.ChecklistOnly, nil, "it012-v1-photo")
	var createdEvent database.OutboxIntent
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, view.EventID).Take(&createdEvent).Error; err != nil {
		t.Fatal(err)
	}
	var createdEnvelope struct {
		Payload map[string]json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(createdEvent.Payload, &createdEnvelope); err != nil {
		t.Fatal(err)
	}
	if _, exists := createdEnvelope.Payload["reviewerMembershipId"]; exists {
		t.Fatal("v1 inspection.created.v1 added a null reviewerMembershipId")
	}
	if err := h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
		var draft database.CaptureDraft
		if err := tx.Where("tenant_id=? AND responsibility_id=?", fx.seed.TenantID, view.Responsibility.ID).Take(&draft).Error; err != nil {
			return err
		}
		var requirements []capturecore.Requirement
		if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
			return err
		}
		if len(requirements) != 2 {
			t.Errorf("v1 photo requirements=%+v; want two template requirements", requirements)
		} else if requirements[0].Key != "overview" || requirements[0].MinimumMedia != 1 || requirements[0].MaximumMedia != 2 || requirements[1].Key != "detail" || requirements[1].MinimumMedia != 2 || requirements[1].MaximumMedia != 5 {
			t.Errorf("v1 photo requirements=%+v; want original keys and limits", requirements)
		}
		var inventoryJobs int64
		if err := tx.Model(&database.OutboxIntent{}).Where("tenant_id=? AND (type LIKE '%inventory%' OR type LIKE '%INVENTORY%')", fx.seed.TenantID).Count(&inventoryJobs).Error; err != nil {
			return err
		}
		if inventoryJobs != 0 {
			t.Errorf("v1 photo create enqueued %d inventory jobs", inventoryJobs)
		}
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", fx.seed.TenantID, view.Inspection.ID).Take(&inspection).Error; err != nil {
			return err
		}
		if inspection.InventoryPromptSnapshotID != nil {
			t.Errorf("v1 photo create pinned inventory prompt %s", inspection.InventoryPromptSnapshotID.String())
		}
		var reference database.ReferenceSnapshot
		if err := tx.Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, view.Inspection.ID).Take(&reference).Error; err != nil {
			return err
		}
		if reference.ReferenceVersionID != nil || reference.ComparisonMode != string(catalog.ChecklistOnly) {
			t.Errorf("v1 CHECKLIST_ONLY reference snapshot=%+v; want no pinned version", reference)
		}
		var policy database.PolicySnapshot
		if err := tx.Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, view.Inspection.ID).Take(&policy).Error; err != nil {
			return err
		}
		if policy.SchemaVersion != 1 {
			t.Errorf("v1 policy snapshot schemaVersion=%d; want 1", policy.SchemaVersion)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestIT013V1VideoCreateUsesLegacyMainAttempt(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	h, err := harness.NewFromEnv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	fx := newTask3CreateFixture(t, h, "VIDEO", 1, false)
	view := fx.create(t, catalog.ChecklistOnly, nil, "it013-v1-video")
	if got := loadRequirements(t, h, fx.seed.TenantID, view.Responsibility.ID); len(got) != 1 || got[0].Key != "video:main" {
		t.Fatalf("v1 video draft requirements=%+v; want exactly video:main", got)
	}
	now := time.Now().UTC()
	tokenDigest := sha256.Sum256([]byte("it013:" + view.Responsibility.ID.String()))
	invitation := database.Invitation{ID: identity.NewID(), TenantID: fx.seed.TenantID, ResponsibilityID: view.Responsibility.ID, TokenHash: tokenDigest[:], DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	acceptance := database.ProcessingAcceptance{ID: identity.NewID(), TenantID: fx.seed.TenantID, ResponsibilityID: view.Responsibility.ID, DisclosureVersion: "pt-BR-1", PhotoProcessing: true, VideoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}
	if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&invitation).Error; err != nil {
			return err
		}
		return tx.Create(&acceptance).Error
	}); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := capturebeginvideo.Setup(capturebeginvideo.Dependencies{DB: fx.h.DB, Bus: bus, Within: fx.h.WithinTenant}); err != nil {
		t.Fatal(err)
	}
	raw, err := bus.Send(context.Background(), capturebeginvideo.Command{TenantID: fx.seed.TenantID, ResponsibilityID: view.Responsibility.ID, AcquisitionSource: "CAMERA", MutationID: "it013-v1-begin"})
	if err != nil {
		t.Fatal(err)
	}
	attempt := raw.(database.VideoAttempt)
	if attempt.RequirementKey != nil {
		t.Fatalf("v1 video attempt requirement key=%q; want NULL legacy main slot", *attempt.RequirementKey)
	}
}
