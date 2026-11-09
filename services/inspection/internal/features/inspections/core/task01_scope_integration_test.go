//go:build integration

package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/request_inventory_jobs"
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

func TestTask01ScopePinAndRevisionIntegration(t *testing.T) {
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
	kitchenBaselineID, bathroomBaselineID := identity.NewID().String(), identity.NewID().String()
	f.setRequirements([]capturecore.Requirement{
		{Key: "env:" + kitchenBaselineID, EvidenceKind: "PHOTO", EnvironmentIdentityID: kitchenBaselineID, EnvironmentTypeCode: "KITCHEN", EnvironmentName: "Cozinha", Required: true, Expected: true, MinimumMedia: 1, MaximumMedia: 20},
		{Key: "env:" + bathroomBaselineID, EvidenceKind: "PHOTO", EnvironmentIdentityID: bathroomBaselineID, EnvironmentTypeCode: "BATHROOM", EnvironmentName: "Banheiro", Required: true, Expected: true, MinimumMedia: 1, MaximumMedia: 20},
	})
	if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
		for position, environment := range []struct {
			id   string
			kind string
			name string
		}{{kitchenBaselineID, "KITCHEN", "Cozinha"}, {bathroomBaselineID, "BATHROOM", "Banheiro"}} {
			id, err := identity.ParseID(environment.id)
			if err != nil {
				return err
			}
			row := database.InventoryEnvironment{ID: identity.NewID(), TenantID: f.seed.TenantID, OriginVersionID: f.referenceID, IdentityID: id, TypeCode: environment.kind, Name: environment.name, Expected: true, Decision: "PENDING", Position: position}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	document := catalog.TemplateDocument{SchemaVersion: catalog.DescriptiveSchemaVersion, SegmentVersionID: f.seed.SegmentVersionID.String(), ParticipantRoles: []string{"TENANT_PARTICIPANT"}, DefaultComparisonMode: catalog.ChecklistOnly, AssetProfile: scope.ProfilePropertyComponents, ExpectedRooms: []catalog.ExpectedRoom{{Key: "kitchen", TypeCode: "KITCHEN", Name: "Cozinha", Components: []catalog.ExpectedComponent{{Key: "floor", TypeCode: "FLOOR", Label: "Piso", Views: []string{"geral", "detalhe"}}}}}, HandoverRecords: []catalog.HandoverRecord{{Key: "keys", Kind: "KEY", Required: true}}, AnalysisType: "REAL_ESTATE"}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	f.version.Version.DefinitionJSON = encoded
	f.version.Version.SchemaVersion = catalog.DescriptiveSchemaVersion
	f.resolvedVersion.Version = f.version.Version
	if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
		template := database.Template{ID: f.version.Template.ID, TenantID: f.seed.TenantID, Key: "task01-scope", Name: "Escopo task 01", SegmentVersionID: f.seed.SegmentVersionID, ActiveVersionID: &f.templateVersionID, Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if err := tx.Create(&template).Error; err != nil {
			return err
		}
		version := database.TemplateVersion{ID: f.templateVersionID, TenantID: f.seed.TenantID, TemplateID: template.ID, VersionNumber: 1, SchemaVersion: catalog.DescriptiveSchemaVersion, DefinitionJSON: encoded, CanonicalDigest: "task01-scope-v1", Status: "ACTIVE", IdempotencyKey: "task01-scope-v1", PublishedAt: time.Now().UTC(), CreatedBy: f.seed.IdentityID}
		return tx.Create(&version).Error
	}); err != nil {
		t.Fatal(err)
	}
	f.service.Authorizer = auth.Authorizer{}
	now := time.Now().UTC()
	input := CreateInput{TenantID: f.seed.TenantID, AssetID: f.seed.AssetID, ParticipantID: f.seed.ParticipantID, TemplateID: &f.version.Template.ID, TemplateVersionID: &f.templateVersionID, ComparisonMode: catalog.ChecklistOnly, Source: SourceScheduled, SourceKey: "task01-scope-pin", DueAt: now.Add(time.Hour), DeadlineAt: now.Add(24 * time.Hour), ScopeOverride: &scope.Override{Profile: scope.ProfileSelectedItems, Selections: []scope.Selection{{RoomKey: "kitchen", ComponentKey: "floor"}}}}
	created, err := f.service.Create(ctx, input)
	if err != nil {
		t.Fatalf("IT-002 create with selected scope: %v", err)
	}
	var pinned database.InspectionScopeSnapshot
	if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, created.Inspection.ID).Take(&pinned).Error
	}); err != nil {
		t.Fatal(err)
	}
	var effective scope.Effective
	if err := json.Unmarshal(pinned.Document, &effective); err != nil {
		t.Fatal(err)
	}
	if pinned.Digest == "" || pinned.Digest != effective.Digest || pinned.Version != 1 || len(effective.Rooms) != 1 || len(effective.Rooms[0].Components) != 1 || effective.Rooms[0].Components[0].Key != "floor" {
		t.Fatalf("IT-002 scope snapshot is not the selected transactional scope: row=%+v document=%+v", pinned, effective)
	}
	updatedDocument := document
	updatedDocument.ExpectedRooms[0].Components = append(updatedDocument.ExpectedRooms[0].Components, catalog.ExpectedComponent{Key: "cabinet", TypeCode: "CABINET", Label: "Armários"})
	updatedDocument.ExpectedRooms = append(updatedDocument.ExpectedRooms, catalog.ExpectedRoom{Key: "bathroom", TypeCode: "BATHROOM", Name: "Banheiro", Components: []catalog.ExpectedComponent{{Key: "sink", TypeCode: "SINK", Label: "Pia"}}})
	updatedDefinition, _ := json.Marshal(updatedDocument)
	newTemplateVersionID := identity.NewID()
	f.version.Version.ID = newTemplateVersionID
	f.version.Version.VersionNumber = 2
	f.version.Version.DefinitionJSON = updatedDefinition
	f.version.Version.CanonicalDigest = "task01-scope-v2"
	f.version.Version.IdempotencyKey = "task01-scope-v2"
	f.resolvedVersion.Version = f.version.Version
	f.templateVersionID = newTemplateVersionID
	if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
		version := f.version.Version
		if err := tx.Create(&version).Error; err != nil {
			return err
		}
		return tx.Model(&database.Template{}).Where("tenant_id=? AND id=?", f.seed.TenantID, f.version.Template.ID).Updates(map[string]any{"active_version_id": newTemplateVersionID, "version": 2}).Error
	}); err != nil {
		t.Fatal(err)
	}
	newInspectionInput := input
	newInspectionInput.SourceKey = "task01-scope-after-template-change"
	newInspectionInput.ScopeOverride = nil
	newInspectionInput.ComparisonMode = catalog.FixedOrigin
	newInspectionInput.ReferenceVersionID = &f.referenceID
	newInspection, err := f.service.Create(ctx, newInspectionInput)
	if err != nil {
		t.Fatalf("UT-007 create after template edit: %v", err)
	}
	var newPinned database.InspectionScopeSnapshot
	if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, newInspection.Inspection.ID).Take(&newPinned).Error
	}); err != nil {
		t.Fatal(err)
	}
	var newEffective scope.Effective
	if err := json.Unmarshal(newPinned.Document, &newEffective); err != nil {
		t.Fatal(err)
	}
	if len(effective.Rooms[0].Components) != 1 || len(newEffective.Rooms) != 2 || len(newEffective.Rooms[0].Components) != 2 {
		t.Fatalf("UT-007 template edit changed a pinned scope or failed to affect new work: existing=%+v new=%+v", effective.Rooms, newEffective.Rooms)
	}

	manager := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.seed.TenantID, Principal: requestctx.Principal{IdentityID: f.seed.IdentityID, TenantID: f.seed.TenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: auth.ProductsForRole(auth.TenantAdmin)}})
	confirmed, err := f.service.ConfirmScope(manager, f.seed.TenantID, newInspection.Inspection.ID, scope.Override{Profile: scope.ProfileSelectedItems, Selections: []scope.Selection{{RoomKey: "kitchen", ComponentKey: "floor"}}}, 1)
	if err != nil || confirmed.Version != 2 {
		t.Fatalf("ConfirmScope after Create: version=%d err=%v", confirmed.Version, err)
	}
	var revisedDraft database.CaptureDraft
	if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
		var responsibility database.Responsibility
		if err := tx.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, newInspection.Inspection.ID).Take(&responsibility).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND responsibility_id=?", f.seed.TenantID, responsibility.ID).Take(&revisedDraft).Error
	}); err != nil {
		t.Fatal(err)
	}
	var revisedRequirements []capturecore.Requirement
	if err := json.Unmarshal(revisedDraft.Requirements, &revisedRequirements); err != nil {
		t.Fatal(err)
	}
	if len(revisedRequirements) != 1 || revisedRequirements[0].Key != "env:kitchen" || revisedRequirements[0].EnvironmentIdentityID != kitchenBaselineID {
		t.Fatalf("requirements after removing a room from confirmed move-out scope = %+v; want env:kitchen anchored to baseline environment %s", revisedRequirements, kitchenBaselineID)
	}
	noScopeManager := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.seed.TenantID, Principal: requestctx.Principal{IdentityID: f.seed.IdentityID, TenantID: f.seed.TenantID, Roles: []string{auth.Employee}, ProductEntitlements: auth.ProductsForRole(auth.Employee)}})
	if _, err := f.service.ConfirmScope(noScopeManager, f.seed.TenantID, created.Inspection.ID, scope.Override{Profile: scope.ProfileFurnishedProperty}, 1); !apperror.Is(err, apperror.NotFound) {
		t.Fatalf("UT-011 out-of-scope manager error=%v, want not found", err)
	}
	updated, err := f.service.ConfirmScope(manager, f.seed.TenantID, created.Inspection.ID, scope.Override{Profile: scope.ProfilePropertyComponents}, 1)
	if err != nil || updated.Version != 2 {
		t.Fatalf("IT-003 confirm scope version 1: version=%d err=%v", updated.Version, err)
	}
	retried, err := f.service.ConfirmScope(manager, f.seed.TenantID, created.Inspection.ID, scope.Override{Profile: scope.ProfilePropertyComponents}, 1)
	if err != nil || retried.Version != 2 || retried.Digest != updated.Digest {
		t.Fatalf("UT-013 confirmation retry version=%d digest=%s err=%v", retried.Version, retried.Digest, err)
	}
	secondManager := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.seed.TenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: f.seed.TenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: auth.ProductsForRole(auth.TenantAdmin)}})
	_, err = f.service.ConfirmScope(secondManager, f.seed.TenantID, created.Inspection.ID, scope.Override{Profile: scope.ProfileFurnishedProperty}, 1)
	if !apperror.Is(err, apperror.Conflict) {
		t.Fatalf("UT-012/IT-030 concurrent manager scope update err=%v, want conflict", err)
	}
	for _, activity := range []string{"consent", "upload", "answer", "submission"} {
		t.Run("IT-031/UT-014 freezes after real "+activity, func(t *testing.T) {
			input.SourceKey = "task01-scope-activity-" + activity
			activityInspection, err := f.service.Create(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			var responsibility database.Responsibility
			var draft database.CaptureDraft
			if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
				if err := tx.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, activityInspection.Inspection.ID).Take(&responsibility).Error; err != nil {
					return err
				}
				return tx.Where("tenant_id=? AND responsibility_id=?", f.seed.TenantID, responsibility.ID).Take(&draft).Error
			}); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
				switch activity {
				case "consent":
					return tx.Create(&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: f.seed.TenantID, ResponsibilityID: responsibility.ID, DisclosureVersion: "consent-v1", PhotoProcessing: true, AIAnalysis: true, GPSUse: false, AcceptedAt: now}).Error
				case "upload":
					return tx.Create(&database.MediaObject{ID: identity.NewID(), TenantID: f.seed.TenantID, ResponsibilityID: responsibility.ID, ObjectKey: "task01/" + identity.NewID().String(), ContentType: "image/jpeg", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 10, Status: "RECEIVED", MediaKind: "PHOTO", IdempotencyKey: "task01-" + identity.NewID().String(), AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error
				case "answer":
					return tx.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: f.seed.TenantID, DraftID: draft.ID, RequirementKey: "env:kitchen.floor", MediaIDs: []byte(`[]`), Flags: []byte(`[]`), Version: 1, CreatedAt: now, UpdatedAt: now}).Error
				case "submission":
					return tx.Create(&database.SubmissionVersion{ID: identity.NewID(), TenantID: f.seed.TenantID, DraftID: draft.ID, ResponsibilityID: responsibility.ID, VersionNumber: 1, Complete: true, Payload: []byte(`{}`), PayloadDigest: strings.Repeat("a", 64), SubmittedAt: now}).Error
				default:
					return nil
				}
			}); err != nil {
				t.Fatal(err)
			}
			before := activityInspection.Scope
			_, err = f.service.ConfirmScope(manager, f.seed.TenantID, activityInspection.Inspection.ID, scope.Override{Profile: scope.ProfileFurnishedProperty}, 1)
			if !apperror.Is(err, apperror.InvalidState) {
				t.Fatalf("scope change after %s err=%v, want invalid state", activity, err)
			}
			var after database.InspectionScopeSnapshot
			if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
				return tx.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, activityInspection.Inspection.ID).Take(&after).Error
			}); err != nil {
				t.Fatal(err)
			}
			if after.Digest != before.Digest || after.Version != 1 {
				t.Fatalf("IT-031 failed attempt changed the pinned scope: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestTask05ConfirmScopeRestoresUnmatchedPinnedEnvironment(t *testing.T) {
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
	xID, yID := identity.NewID(), identity.NewID()
	f.setRequirements([]capturecore.Requirement{
		{Key: "env:" + xID.String(), Section: "LIVING_ROOM", Label: "Sala", EvidenceKind: "PHOTO", EnvironmentIdentityID: xID.String(), EnvironmentTypeCode: "LIVING_ROOM", EnvironmentName: "Sala", Required: true, ImpossibilityAllowed: true, Expected: true, MinimumMedia: 1, MaximumMedia: 20, CaptureSourcePolicy: "CAMERA_DEFAULT", ComparisonTarget: "FIXED_ORIGIN"},
		{Key: "env:" + yID.String(), Section: "BALCONY", Label: "Varanda", EvidenceKind: "PHOTO", EnvironmentIdentityID: yID.String(), EnvironmentTypeCode: "BALCONY", EnvironmentName: "Varanda", Required: true, ImpossibilityAllowed: true, Expected: true, MinimumMedia: 1, MaximumMedia: 20, CaptureSourcePolicy: "CAMERA_DEFAULT", ComparisonTarget: "FIXED_ORIGIN"},
	})
	limits := catalog.DefaultEnvironmentCapture()
	limits.Photo.Minimum, limits.Photo.Maximum = 3, 10
	document := catalog.TemplateDocument{SchemaVersion: catalog.DescriptiveSchemaVersion, SegmentVersionID: f.seed.SegmentVersionID.String(), ParticipantRoles: []string{"TENANT_PARTICIPANT"}, DefaultComparisonMode: catalog.FixedOrigin, AssetProfile: scope.ProfilePropertyComponents, EnvironmentCapture: limits, ExpectedRooms: []catalog.ExpectedRoom{
		{Key: "sala", TypeCode: "LIVING_ROOM", Name: "Sala", Components: []catalog.ExpectedComponent{{Key: "piso", TypeCode: "FLOOR", Label: "Piso"}}},
		{Key: "varanda", TypeCode: "BALCONY", Name: "Varanda", Components: []catalog.ExpectedComponent{{Key: "piso", TypeCode: "FLOOR", Label: "Piso"}}},
		{Key: "quarto", TypeCode: "BEDROOM", Name: "Quarto", Components: []catalog.ExpectedComponent{{Key: "piso", TypeCode: "FLOOR", Label: "Piso"}}},
	}, AnalysisType: "REAL_ESTATE"}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	f.version.Version.DefinitionJSON = encoded
	f.version.Version.SchemaVersion = catalog.DescriptiveSchemaVersion
	f.resolvedVersion.Version = f.version.Version
	now := time.Now().UTC()
	if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
		template := database.Template{ID: f.version.Template.ID, TenantID: f.seed.TenantID, Key: "task05-confirm-scope", Name: "Escopo task 05", SegmentVersionID: f.seed.SegmentVersionID, ActiveVersionID: &f.templateVersionID, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&template).Error; err != nil {
			return err
		}
		version := database.TemplateVersion{ID: f.templateVersionID, TenantID: f.seed.TenantID, TemplateID: template.ID, VersionNumber: 1, SchemaVersion: catalog.DescriptiveSchemaVersion, DefinitionJSON: encoded, CanonicalDigest: "task05-confirm-scope-v1", Status: "ACTIVE", IdempotencyKey: "task05-confirm-scope-v1", PublishedAt: now, CreatedBy: f.seed.IdentityID}
		if err := tx.Create(&version).Error; err != nil {
			return err
		}
		for position, environment := range []struct {
			id   identity.ID
			kind string
			name string
		}{{xID, "LIVING_ROOM", "Sala"}, {yID, "BALCONY", "Varanda"}} {
			row := database.InventoryEnvironment{ID: identity.NewID(), TenantID: f.seed.TenantID, OriginVersionID: f.referenceID, IdentityID: environment.id, TypeCode: environment.kind, Name: environment.name, Expected: true, Decision: "PENDING", Position: position}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.service.Authorizer = auth.Authorizer{}
	create := func(key string, selections ...scope.Selection) View {
		t.Helper()
		view, err := f.service.Create(ctx, CreateInput{TenantID: f.seed.TenantID, AssetID: f.seed.AssetID, ParticipantID: f.seed.ParticipantID, TemplateID: &f.version.Template.ID, TemplateVersionID: &f.templateVersionID, ComparisonMode: catalog.FixedOrigin, ReferenceVersionID: &f.referenceID, Source: SourceScheduled, SourceKey: key, DueAt: now.Add(time.Hour), DeadlineAt: now.Add(24 * time.Hour), ScopeOverride: &scope.Override{Profile: scope.ProfileSelectedItems, Selections: selections}})
		if err != nil {
			t.Fatalf("Create %s: %v", key, err)
		}
		return view
	}
	manager := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.seed.TenantID, Principal: requestctx.Principal{IdentityID: f.seed.IdentityID, TenantID: f.seed.TenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: auth.ProductsForRole(auth.TenantAdmin)}})
	initial := create("task05-confirm-scope-unmatched", scope.Selection{RoomKey: "sala", ComponentKey: "piso"})
	load := func(inspectionID identity.ID) []capturecore.Requirement {
		t.Helper()
		var responsibility database.Responsibility
		var draft database.CaptureDraft
		if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
			if err := tx.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, inspectionID).Take(&responsibility).Error; err != nil {
				return err
			}
			return tx.Where("tenant_id=? AND responsibility_id=?", f.seed.TenantID, responsibility.ID).Take(&draft).Error
		}); err != nil {
			t.Fatal(err)
		}
		var requirements []capturecore.Requirement
		if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
			t.Fatal(err)
		}
		return requirements
	}
	allRooms := []scope.Selection{{RoomKey: "sala", ComponentKey: "piso"}, {RoomKey: "varanda", ComponentKey: "piso"}, {RoomKey: "quarto", ComponentKey: "piso"}}
	direct := create("task05-confirm-scope-same-result-direct", allRooms...)
	createdRequirements := load(direct.Inspection.ID)
	confirmedDirect, err := f.service.ConfirmScope(manager, f.seed.TenantID, direct.Inspection.ID, scope.Override{Profile: scope.ProfileSelectedItems, Selections: allRooms}, 1)
	if err != nil || confirmedDirect.Version != 2 {
		t.Fatalf("confirm unchanged scope: version=%d err=%v", confirmedDirect.Version, err)
	}
	confirmedRequirements := load(direct.Inspection.ID)
	if !reflect.DeepEqual(createdRequirements, confirmedRequirements) {
		t.Fatalf("Create and same-scope ConfirmScope requirements differ: created=%+v confirmed=%+v", createdRequirements, confirmedRequirements)
	}
	if len(confirmedRequirements) != 3 {
		t.Fatalf("confirmed requirements = %d; want two linked rooms and one unlinked room: %+v", len(confirmedRequirements), confirmedRequirements)
	}
	wantEnvironmentIDs := map[string]string{"env:sala": xID.String(), "env:varanda": yID.String(), "env:quarto": "quarto"}
	for _, requirement := range confirmedRequirements {
		if requirement.MinimumMedia != 3 || requirement.MaximumMedia != 10 {
			t.Fatalf("confirmed requirement %s limits=%d/%d; want 3/10", requirement.Key, requirement.MinimumMedia, requirement.MaximumMedia)
		}
		if requirement.EnvironmentIdentityID != wantEnvironmentIDs[requirement.Key] {
			t.Fatalf("confirmed requirement %s identity=%q; want %q", requirement.Key, requirement.EnvironmentIdentityID, wantEnvironmentIDs[requirement.Key])
		}
	}
	want := []scope.Selection{{RoomKey: "sala", ComponentKey: "piso"}, {RoomKey: "varanda", ComponentKey: "piso"}}
	confirmed, err := f.service.ConfirmScope(manager, f.seed.TenantID, initial.Inspection.ID, scope.Override{Profile: scope.ProfileSelectedItems, Selections: want}, 1)
	if err != nil || confirmed.Version != 2 {
		t.Fatalf("confirm expanded scope: version=%d err=%v", confirmed.Version, err)
	}
	checkY := func(label string, inspectionID identity.ID) {
		t.Helper()
		for _, requirement := range load(inspectionID) {
			if requirement.Key == "env:varanda" {
				if requirement.EnvironmentIdentityID != yID.String() {
					t.Fatalf("%s varanda requirement = %+v; want pinned environment %s", label, requirement, yID)
				}
				return
			}
		}
		t.Fatalf("%s requirements lost varanda: %+v", label, load(inspectionID))
	}
	checkY("expanded after confirmation", initial.Inspection.ID)
	removed, err := f.service.ConfirmScope(manager, f.seed.TenantID, initial.Inspection.ID, scope.Override{Profile: scope.ProfileSelectedItems, Selections: want[:1]}, 2)
	if err != nil || removed.Version != 3 {
		t.Fatalf("remove varanda: version=%d err=%v", removed.Version, err)
	}
	readmitted, err := f.service.ConfirmScope(manager, f.seed.TenantID, initial.Inspection.ID, scope.Override{Profile: scope.ProfileSelectedItems, Selections: want}, 3)
	if err != nil || readmitted.Version != 4 {
		t.Fatalf("readmit varanda: version=%d err=%v", readmitted.Version, err)
	}
	checkY("readmitted after two confirmations", initial.Inspection.ID)
	var comparisonJobs []database.InventoryJob
	if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?)", f.seed.TenantID, f.seed.TenantID, initial.Inspection.ID).Update("status", "SUBMITTED").Error; err != nil {
			return err
		}
		if err := request_inventory_jobs.Request(ctx, tx, f.seed.TenantID, initial.Inspection.ID, time.Now().UTC()); err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND inspection_id=? AND kind='COMPARE'", f.seed.TenantID, initial.Inspection.ID).Find(&comparisonJobs).Error
	}); err != nil {
		t.Fatalf("schedule comparison for confirmed scope: %v", err)
	}
	matchedVaranda := false
	for _, job := range comparisonJobs {
		if job.EnvironmentIdentityID != nil && *job.EnvironmentIdentityID == yID && job.RequirementKey == "env:varanda" {
			matchedVaranda = true
		}
	}
	if !matchedVaranda || len(comparisonJobs) != 2 {
		t.Fatalf("comparison jobs after scope confirmation = %+v; want Sala and Varanda linked to their pinned environments", comparisonJobs)
	}
	missingReference := create("task05-confirm-scope-missing-reference", scope.Selection{RoomKey: "sala", ComponentKey: "piso"})
	if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('app.retention_purge', 'on', true)").Error; err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"referenceVersionId": f.referenceID, "comparisonMode": catalog.FixedOrigin})
		if err := tx.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, missingReference.Inspection.ID).Delete(&database.ReferenceSnapshot{}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ReferenceSnapshot{ID: identity.NewID(), TenantID: f.seed.TenantID, InspectionID: missingReference.Inspection.ID, ReferenceVersionID: &f.referenceID, ComparisonMode: string(catalog.FixedOrigin), Payload: payload, CreatedAt: time.Now().UTC()}).Error
	}); err != nil {
		t.Fatal(err)
	}
	_, err = f.service.ConfirmScope(manager, f.seed.TenantID, missingReference.Inspection.ID, scope.Override{Profile: scope.ProfileSelectedItems, Selections: []scope.Selection{{RoomKey: "sala", ComponentKey: "piso"}}}, 1)
	var invalidState *apperror.Error
	if !errors.As(err, &invalidState) || invalidState.Code != apperror.InvalidState || invalidState.Field != "referenceVersionId" || invalidState.Safe != "A versão da origem fixada não está disponível. Atualize a vistoria e tente novamente." {
		t.Fatalf("ConfirmScope with missing fixed origin = %v; want safe INVALID_STATE referenceVersionId error", err)
	}
	directAfter := create("task05-confirm-scope-direct", want...)
	checkY("direct Create", directAfter.Inspection.ID)
	if got := load(initial.Inspection.ID); len(got) != 2 || got[1].EnvironmentIdentityID != yID.String() {
		t.Fatalf("confirmed requirements = %+v; want same two pinned environments as direct Create", got)
	}
	checklistDocument := document
	checklistDocument.DefaultComparisonMode = catalog.ChecklistOnly
	checklistDefinition, _ := json.Marshal(checklistDocument)
	f.resolvedVersion.Version.DefinitionJSON = checklistDefinition
	checklistInput := CreateInput{TenantID: f.seed.TenantID, AssetID: f.seed.AssetID, ParticipantID: f.seed.ParticipantID, TemplateID: &f.version.Template.ID, TemplateVersionID: &f.templateVersionID, ComparisonMode: catalog.ChecklistOnly, ReferenceVersionID: &f.referenceID, Source: SourceScheduled, SourceKey: "task05-checklist-version-does-not-link-origin", DueAt: now.Add(time.Hour), DeadlineAt: now.Add(24 * time.Hour), ScopeOverride: &scope.Override{Profile: scope.ProfileSelectedItems, Selections: []scope.Selection{{RoomKey: "sala", ComponentKey: "piso"}}}}
	checklistInspection, err := f.service.Create(ctx, checklistInput)
	if err != nil {
		t.Fatalf("Create CHECKLIST_ONLY with a reference version ID: %v", err)
	}
	var checklistResponsibility database.Responsibility
	if err := h.WithinTenant(ctx, f.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, checklistInspection.Inspection.ID).Take(&checklistResponsibility).Error; err != nil {
			return err
		}
		stale, _ := json.Marshal([]capturecore.Requirement{{Key: "env:sala", Section: "LIVING_ROOM", Label: "Sala", EvidenceKind: "PHOTO", Required: true, Expected: true, MinimumMedia: 1, MaximumMedia: 20, EnvironmentIdentityID: xID.String(), EnvironmentTypeCode: "LIVING_ROOM", EnvironmentName: "Sala"}})
		return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", f.seed.TenantID, checklistResponsibility.ID).Update("requirements", stale).Error
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ConfirmScope(manager, f.seed.TenantID, checklistInspection.Inspection.ID, *checklistInput.ScopeOverride, 1); err != nil {
		t.Fatalf("ConfirmScope CHECKLIST_ONLY: %v", err)
	}
	checklistRequirements := load(checklistInspection.Inspection.ID)
	if len(checklistRequirements) != 1 || checklistRequirements[0].EnvironmentIdentityID != "sala" {
		t.Fatalf("CHECKLIST_ONLY requirements reused a pinned origin environment: %+v; want scope identity sala", checklistRequirements)
	}
}
