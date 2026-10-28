//go:build integration

package complete

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"inspection/libs/identity"
	segmentresolve "inspection/services/inspection/internal/features/segments/resolve_definition"
	publishtemplate "inspection/services/inspection/internal/features/templates/publish_template"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestOnboardingCompletePublishesAndActivatesEnvironmentTemplateIT(t *testing.T) {
	url := os.Getenv("INSPECTION_TEST_ONBOARDING_DATABASE_URL")
	runtimeURL := os.Getenv("INSPECTION_TEST_ONBOARDING_RUNTIME_DATABASE_URL")
	if url == "" || runtimeURL == "" {
		t.Skip("fresh disposable onboarding admin and runtime database URLs are required")
	}
	db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	runtimeDB, err := gorm.Open(postgres.Open(runtimeURL), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var exists *string
	if err := db.Raw("SELECT to_regclass('platform.schema_migrations')").Scan(&exists).Error; err != nil {
		t.Fatal(err)
	}
	if exists != nil {
		t.Fatal("onboarding integration database must be fresh")
	}
	if err := (database.Migrator{DB: db}).Migrate(context.Background()); err != nil {
		t.Fatalf("migrate disposable onboarding database: %v", err)
	}
	tenantID, actorID := identity.NewID(), identity.NewID()
	if err := db.Create(&database.Tenant{ID: tenantID, TenantID: tenantID, Name: "Onboarding test", Language: "pt-BR", DefaultTimezone: "America/Sao_Paulo", Status: "ACTIVE"}).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := bus.RegisterQuery(segmentresolve.Query{}, func(context.Context, any) (any, error) { return segmentresolve.Result{}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := publishtemplate.Setup(publishtemplate.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: actorID, TenantID: tenantID, Roles: []string{auth.TenantAdmin, auth.InspectionConfigAdmin, auth.ParticipationAdmin}, ProductEntitlements: []string{auth.AdminProduct}, Internal: true}})
	_, _, template, err := (Service{DB: runtimeDB, Bus: bus, Stage: "production"}).ensureCatalog(ctx, tenantID)
	if err != nil {
		t.Fatalf("ensure onboarding catalog: %v", err)
	}
	if template.ActiveVersionID == nil {
		t.Fatal("onboarding template has no active version")
	}
	var active database.TemplateVersion
	if err := db.First(&active, "id=?", *template.ActiveVersionID).Error; err != nil {
		t.Fatal(err)
	}
	if active.SchemaVersion != 2 || active.Status != "ACTIVE" {
		t.Fatalf("onboarding flow active template version schema=%d status=%s", active.SchemaVersion, active.Status)
	}
	var document struct {
		CaptureLayout         string          `json:"captureLayout"`
		DefaultComparisonMode string          `json:"defaultComparisonMode"`
		EnvironmentCapture    json.RawMessage `json:"environmentCapture"`
	}
	if err := json.Unmarshal(active.DefinitionJSON, &document); err != nil {
		t.Fatal(err)
	}
	if document.CaptureLayout != "ENVIRONMENTS" || document.DefaultComparisonMode != "CHECKLIST_ONLY" || len(document.EnvironmentCapture) == 0 {
		t.Fatalf("active onboarding template = %+v", document)
	}
	var onboarding database.Template
	if err := db.Where("tenant_id=? AND key='real-estate-checklist'", tenantID).First(&onboarding).Error; err != nil {
		t.Fatal(err)
	}
	var versions []database.TemplateVersion
	if err := db.Where("tenant_id=? AND template_id=?", tenantID, onboarding.ID).Find(&versions).Error; err != nil {
		t.Fatal(err)
	}
	for _, version := range versions {
		if version.ID != *template.ActiveVersionID && version.SchemaVersion == 2 {
			t.Fatalf("unexpected duplicate schemaVersion 2: %+v", versions)
		}
	}
	if _, _, _, err := (Service{DB: runtimeDB, Bus: bus, Stage: "production"}).ensureCatalog(ctx, tenantID); err != nil {
		t.Fatalf("onboarding catalog replay: %v", err)
	}
	var replayed int64
	if err := db.Model(&database.TemplateVersion{}).Where("tenant_id=? AND template_id=?", tenantID, onboarding.ID).Count(&replayed).Error; err != nil {
		t.Fatal(err)
	}
	if replayed != int64(len(versions)) {
		t.Fatalf("catalog replay versions=%d, want %d", replayed, len(versions))
	}
}
