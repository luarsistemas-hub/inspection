//go:build integration

package core

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/features/inspections/scope"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestTask01MoveOutRequiresApprovedSameAssetBaseline(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })

	t.Run("IT-017/UT-064 pins the selected approved version", func(t *testing.T) {
		f := newTask3CreateFixture(t, h, "PHOTO", catalog.EnvironmentSchemaVersion, true)
		view, err := f.createErr(catalog.FixedOrigin, &f.referenceID, "task01-it017")
		if err != nil {
			t.Fatal(err)
		}
		if view.Reference.ReferenceVersionID == nil || *view.Reference.ReferenceVersionID != f.referenceID {
			t.Fatalf("approved baseline was not pinned: %+v", view.Reference)
		}
		var approved database.OriginVersion
		if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=?", f.seed.TenantID, f.referenceID).Take(&approved).Error
		}); err != nil || !approved.HasInventory {
			t.Fatalf("IT-017 selected baseline is not approved: %+v err=%v", approved, err)
		}
		retry, err := f.createErr(catalog.FixedOrigin, &f.referenceID, "task01-it017")
		if err != nil || retry.Inspection.ID != view.Inspection.ID {
			t.Fatalf("UT-069 repeated creation inspection=%s original=%s err=%v", retry.Inspection.ID, view.Inspection.ID, err)
		}
		purpose := "INVENTORY_UPDATE"
		var nextVersion int
		if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
			if err := tx.Raw("SELECT COALESCE(MAX(version_number),0)+1 FROM origins.origin_versions WHERE tenant_id=? AND origin_id=?", f.seed.TenantID, f.reference.Version.OriginID).Scan(&nextVersion).Error; err != nil {
				return err
			}
			rotated := database.OriginVersion{ID: identity.NewID(), TenantID: f.seed.TenantID, OriginID: f.reference.Version.OriginID, VersionNumber: nextVersion, Purpose: &purpose, Status: "ACTIVE", MediaKind: "PHOTO", HasInventory: true, IdempotencyKey: "task01-rotation-" + identity.NewID().String(), ScopeManifestDigest: "rotated"}
			if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", f.seed.TenantID, f.referenceID).Update("status", "SUPERSEDED").Error; err != nil {
				return err
			}
			if err := tx.Create(&rotated).Error; err != nil {
				return err
			}
			return tx.Model(&database.Origin{}).Where("tenant_id=? AND id=?", f.seed.TenantID, f.reference.Version.OriginID).Update("active_version_id", rotated.ID).Error
		}); err != nil {
			t.Fatal(err)
		}
		explicitlySelected, err := f.createErr(catalog.FixedOrigin, &f.referenceID, "task01-explicit-superseded")
		if err != nil || explicitlySelected.Reference.ReferenceVersionID == nil || *explicitlySelected.Reference.ReferenceVersionID != f.referenceID {
			t.Fatalf("D-5 explicitly selected SUPERSEDED approved version was not eligible: reference=%+v err=%v", explicitlySelected.Reference, err)
		}
		manager := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.seed.TenantID, Principal: requestctx.Principal{IdentityID: f.seed.IdentityID, TenantID: f.seed.TenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: auth.ProductsForRole(auth.TenantAdmin)}})
		reloaded, err := f.service.Get(manager, f.seed.TenantID, view.Inspection.ID)
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Reference.ReferenceVersionID == nil || *reloaded.Reference.ReferenceVersionID != f.referenceID {
			t.Fatalf("UT-070 active-baseline rotation changed the reloaded pinned reference: %+v", reloaded.Reference)
		}
	})

	t.Run("IT-045/UT-066 and IT-048 block missing and media-only baselines", func(t *testing.T) {
		for _, id := range []string{"it045-no-baseline", "it048-media-only"} {
			f := newTask3CreateFixture(t, h, "PHOTO", catalog.EnvironmentSchemaVersion, false)
			_, err := f.createErr(catalog.FixedOrigin, nil, id)
			if !apperror.Is(err, apperror.InvalidState) || !strings.Contains(err.Error(), "catalogada e aprovada") {
				t.Errorf("%s error=%v, want safe Portuguese baseline-required error", id, err)
			}
			assertNoInspectionForTask01(t, h, f.seed.TenantID, "task3-create-"+id)
		}
	})

	t.Run("IT-057 planned and onboarding creation share the baseline guard", func(t *testing.T) {
		for _, source := range []string{SourceMilestone, SourceManual} {
			f := newTask3CreateFixture(t, h, "PHOTO", catalog.EnvironmentSchemaVersion, false)
			f.service.Authorizer = auth.Authorizer{}
			now := time.Now().UTC()
			input := CreateInput{TenantID: f.seed.TenantID, AssetID: f.seed.AssetID, ParticipantID: f.seed.ParticipantID, TemplateID: &f.version.Template.ID, TemplateVersionID: &f.templateVersionID, ComparisonMode: catalog.FixedOrigin, Source: source, SourceKey: "task01-it057-" + strings.ToLower(source), Reason: "vistoria de saída", DueAt: now.Add(time.Hour), DeadlineAt: now.Add(24 * time.Hour)}
			if source == SourceMilestone {
				input.ProjectID = &f.seed.ProjectID
				input.StageID = &f.seed.StageID
			}
			request := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.seed.TenantID, Principal: requestctx.Principal{IdentityID: f.seed.IdentityID, TenantID: f.seed.TenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: auth.ProductsForRole(auth.TenantAdmin)}})
			if _, err := f.service.Create(request, input); !apperror.Is(err, apperror.InvalidState) {
				t.Errorf("IT-057 source=%s error=%v, want baseline-required invalid state", source, err)
			}
			assertNoInspectionForTask01(t, h, f.seed.TenantID, input.SourceKey)
		}
	})

	t.Run("IT-046 blocks a draft-only baseline", func(t *testing.T) {
		f := newTask3CreateFixture(t, h, "PHOTO", catalog.EnvironmentSchemaVersion, true)
		if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", f.seed.TenantID, f.referenceID).Update("status", "DRAFT").Error
		}); err != nil {
			t.Fatal(err)
		}
		_, err := f.createErr(catalog.FixedOrigin, &f.referenceID, "it046-draft")
		if !apperror.Is(err, apperror.InvalidState) || !strings.Contains(err.Error(), "catalogada e aprovada") {
			t.Fatalf("IT-046 real draft baseline error=%v, want specific Portuguese baseline-required invalid state", err)
		}
	})

	t.Run("IT-047/UT-065 rejects a baseline belonging to another asset", func(t *testing.T) {
		f := newTask3CreateFixture(t, h, "PHOTO", catalog.EnvironmentSchemaVersion, true)
		otherAssetID := identity.NewID()
		if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.Origin{}).Where("tenant_id=? AND id=?", f.seed.TenantID, f.reference.Version.OriginID).Update("asset_id", otherAssetID).Error
		}); err != nil {
			t.Fatal(err)
		}
		_, err := f.createErr(catalog.FixedOrigin, &f.referenceID, "it047-other-asset")
		if !apperror.Is(err, apperror.InvalidInput) {
			t.Fatalf("foreign asset baseline error=%v, want invalid input", err)
		}
		assertNoInspectionForTask01(t, h, f.seed.TenantID, "task3-create-it047-other-asset")
	})

	t.Run("IT-068 baseline rotation during creation conflicts", func(t *testing.T) {
		f := newTask3CreateFixture(t, h, "PHOTO", catalog.EnvironmentSchemaVersion, true)
		if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", f.seed.TenantID, f.referenceID).Update("status", "SUPERSEDED").Error
		}); err != nil {
			t.Fatal(err)
		}
		_, err := f.createErr(catalog.FixedOrigin, nil, "it068-race")
		if !apperror.Is(err, apperror.Conflict) {
			t.Fatalf("IT-068 active baseline rotation error=%v, want conflict", err)
		}
	})

	t.Run("UT-067 manager without asset scope cannot use the baseline", func(t *testing.T) {
		f := newTask3CreateFixture(t, h, "PHOTO", catalog.EnvironmentSchemaVersion, true)
		f.service.Authorizer = auth.Authorizer{}
		input := CreateInput{TenantID: f.seed.TenantID, AssetID: f.seed.AssetID, ParticipantID: f.seed.ParticipantID, TemplateID: &f.version.Template.ID, TemplateVersionID: &f.templateVersionID, ComparisonMode: catalog.FixedOrigin, ReferenceVersionID: &f.referenceID, Source: SourceManual, SourceKey: "task01-ut067", Reason: "vistoria de saída", DueAt: time.Now().UTC().Add(time.Hour), DeadlineAt: time.Now().UTC().Add(24 * time.Hour)}
		manager := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.seed.TenantID, Principal: requestctx.Principal{IdentityID: f.seed.IdentityID, TenantID: f.seed.TenantID, Roles: []string{auth.Employee}, ProductEntitlements: auth.ProductsForRole(auth.Employee)}})
		if _, err := f.service.Create(manager, input); !apperror.Is(err, apperror.NotFound) {
			t.Fatalf("UT-067 unauthorized baseline use error=%v, want not found", err)
		}
	})

	t.Run("IT-029 rejects a component identity outside the template", func(t *testing.T) {
		f := newTask3CreateFixture(t, h, "PHOTO", catalog.DescriptiveSchemaVersion, false)
		doc := catalog.TemplateDocument{SchemaVersion: catalog.DescriptiveSchemaVersion, AssetProfile: scope.ProfileSelectedItems, ExpectedRooms: []catalog.ExpectedRoom{{Key: "bedroom-1", TypeCode: "BEDROOM", Name: "Quarto 1", Components: []catalog.ExpectedComponent{{Key: "floor", TypeCode: "FLOOR", Label: "Piso"}}}}, AnalysisType: "REAL_ESTATE"}
		payload, _ := json.Marshal(doc)
		f.version.Version.DefinitionJSON, f.version.Version.SchemaVersion = payload, catalog.DescriptiveSchemaVersion
		f.resolvedVersion.Version = f.version.Version
		input := CreateInput{TenantID: f.seed.TenantID, AssetID: f.seed.AssetID, ParticipantID: f.seed.ParticipantID, TemplateID: &f.version.Template.ID, TemplateVersionID: &f.templateVersionID, ComparisonMode: catalog.ChecklistOnly, Source: SourceScheduled, SourceKey: "task01-it029", DueAt: time.Now().UTC().Add(time.Hour), DeadlineAt: time.Now().UTC().Add(24 * time.Hour), ScopeOverride: &scope.Override{Profile: scope.ProfileSelectedItems, Selections: []scope.Selection{{RoomKey: "foreign", ComponentKey: "floor"}}}}
		_, err := f.service.Create(ctx, input)
		if !apperror.Is(err, apperror.InvalidInput) {
			t.Fatalf("foreign component identity error=%v, want invalid input", err)
		}
		assertNoInspectionForTask01(t, h, f.seed.TenantID, input.SourceKey)
	})
}

func TestTask02MoveOutV3MapsConfirmedRoomToBaselineRequirement(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f := newTask3CreateFixture(t, h, "PHOTO", catalog.DescriptiveSchemaVersion, true)
	doc := catalog.TemplateDocument{SchemaVersion: catalog.DescriptiveSchemaVersion, AssetProfile: scope.ProfilePropertyComponents, ExpectedRooms: []catalog.ExpectedRoom{{Key: "bedroom-1", TypeCode: "BEDROOM", Name: "Quarto 1", Components: []catalog.ExpectedComponent{{Key: "floor", TypeCode: "FLOOR", Label: "Piso", Views: []string{"geral"}}}}}, AnalysisType: "REAL_ESTATE"}
	encoded, _ := json.Marshal(doc)
	f.version.Version.DefinitionJSON = encoded
	f.version.Version.SchemaVersion = catalog.DescriptiveSchemaVersion
	f.resolvedVersion.Version = f.version.Version
	requirement := capturecore.Requirement{Key: "env:" + f.requirements[0].EnvironmentIdentityID, Section: "BEDROOM", Label: "Quarto 1", EvidenceKind: "PHOTO", Required: true, Expected: true, EnvironmentIdentityID: f.requirements[0].EnvironmentIdentityID, EnvironmentTypeCode: "BEDROOM", EnvironmentName: "Quarto 1", MinimumMedia: 1, MaximumMedia: 20}
	f.setRequirements([]capturecore.Requirement{requirement})
	view, err := f.createErr(catalog.FixedOrigin, &f.referenceID, "task02-moveout-v3-scope")
	if err != nil {
		t.Fatal(err)
	}
	var requirements []capturecore.Requirement
	if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
		var draft database.CaptureDraft
		if err := tx.Where("tenant_id=? AND responsibility_id=?", f.seed.TenantID, view.Responsibility.ID).Take(&draft).Error; err != nil {
			return err
		}
		return json.Unmarshal(draft.Requirements, &requirements)
	}); err != nil {
		t.Fatal(err)
	}
	if len(requirements) != 1 || requirements[0].Key != "env:bedroom-1" || !requirements[0].AllowConfirmedGap {
		t.Fatalf("move-out scope was not mapped to baseline requirement with confirmed-gap policy: %+v", requirements)
	}
	if _, err := capturecore.Evaluate(requirements, nil, false); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("unconfirmed move-out gap error=%v", err)
	}
	result, err := capturecore.Evaluate(requirements, nil, true)
	if err != nil || result.Complete || !result.RequiresAttention {
		t.Fatalf("confirmed move-out gap result=%+v err=%v", result, err)
	}
}

func assertNoInspectionForTask01(t *testing.T, h *harness.Harness, tenantID identity.ID, key string) {
	t.Helper()
	if err := h.WithinTenant(context.Background(), tenantID, func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND source_key=?", tenantID, key).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	}); err != nil {
		t.Fatalf("unexpected inspection side effect for %s: %v", key, err)
	}
}
