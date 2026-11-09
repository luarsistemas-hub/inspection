//go:build integration

package core

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"inspection/services/inspection/internal/features/segments/resolve_definition"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestTask01PublishTemplateV3IT001IT028UT006(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	seed, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.SegmentDefinitionVersion{}).Where("tenant_id=? AND id=?", seed.TenantID, seed.SegmentVersionID).Update("status", "PUBLISHED").Error
	}); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := resolve_definition.Setup(resolve_definition.Dependencies{DB: h.DB, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	service := Service{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}
	request := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: seed.TenantID, Principal: requestctx.Principal{IdentityID: seed.IdentityID, TenantID: seed.TenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: auth.ProductsForRole(auth.TenantAdmin)}})
	document := catalog.TemplateDocument{SchemaVersion: catalog.DescriptiveSchemaVersion, SegmentVersionID: seed.SegmentVersionID.String(), ParticipantRoles: []string{"TENANT_PARTICIPANT"}, DefaultComparisonMode: catalog.ChecklistOnly, CaptureLayout: catalog.EnvironmentsLayout, AssetProfile: "PROPERTY_COMPONENTS", ExpectedRooms: []catalog.ExpectedRoom{{Key: "kitchen", TypeCode: "KITCHEN", Name: "Cozinha", Components: []catalog.ExpectedComponent{{Key: "floor", TypeCode: "FLOOR", Label: "Piso", Views: []string{"geral", "detalhe"}}}}, {Key: "bedroom", TypeCode: "BEDROOM", Name: "Quarto", Components: []catalog.ExpectedComponent{{Key: "floor", TypeCode: "FLOOR", Label: "Piso"}}}}, HandoverRecords: []catalog.HandoverRecord{{Key: "keys", Kind: "KEY", Required: true}}, AnalysisType: "REAL_ESTATE", Policy: catalog.Policy{GeofenceMeters: catalog.DefaultGeofence}}
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Publish(request, seed.TenantID, "task01-v3", "Escopo descritivo", "task01-idempotency", payload)
	if err != nil {
		t.Fatalf("IT-001 publish v3: %v", err)
	}
	var published catalog.TemplateDocument
	if err := json.Unmarshal(first.Version.DefinitionJSON, &published); err != nil || len(published.ExpectedRooms) != 2 || published.ExpectedRooms[0].Components[0].Key != published.ExpectedRooms[1].Components[0].Key {
		t.Fatalf("IT-028 equal component keys in different rooms should compile and persist: %+v err=%v", published.ExpectedRooms, err)
	}
	replay, err := service.Publish(request, seed.TenantID, "task01-v3", "Escopo descritivo", "task01-idempotency", payload)
	if err != nil || replay.Version.ID != first.Version.ID {
		t.Fatalf("UT-006 repeated save version=%s first=%s err=%v", replay.Version.ID, first.Version.ID, err)
	}
	if _, err := service.Activate(request, seed.TenantID, first.Version.ID, 99); !apperror.Is(err, apperror.Conflict) {
		t.Fatalf("UT-006 stale activation error=%v, want conflict", err)
	}
	duplicate := document
	duplicate.ExpectedRooms[0].Components = append(duplicate.ExpectedRooms[0].Components, catalog.ExpectedComponent{Key: "floor", TypeCode: "FLOOR", Label: "Outro piso"})
	duplicatePayload, _ := json.Marshal(duplicate)
	if _, err := service.Publish(request, seed.TenantID, "task01-v3-duplicate", "Duplicado", "task01-duplicate", duplicatePayload); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("IT-028 duplicate component key error=%v, want invalid input", err)
	}
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&database.Template{}).Where("tenant_id=? AND key IN ?", seed.TenantID, []string{"task01-v3", "task01-v3-duplicate"}).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			t.Fatalf("IT-028 invalid definition published %d templates, want one valid version only", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
