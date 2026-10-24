//go:build integration

package publish_seeds

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/segments/resolve_definition"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/features/templates/publish_template"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestUT095CuratedV2SeedsUseRealPublishServiceAndReplay(t *testing.T) {
	url := os.Getenv("INSPECTION_TEST_SEED_DATABASE_URL")
	runtimeURL := os.Getenv("INSPECTION_TEST_SEED_RUNTIME_DATABASE_URL")
	if url == "" || runtimeURL == "" {
		t.Skip("fresh disposable template seed admin and runtime database URLs are required")
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
		t.Fatal("template seed integration database must be fresh")
	}
	if err := (database.Migrator{DB: db}).Migrate(context.Background()); err != nil {
		t.Fatalf("migrate disposable seed database: %v", err)
	}
	tenantID, actorID := identity.NewID(), identity.NewID()
	if err := db.Create(&database.Tenant{ID: tenantID, TenantID: tenantID, Name: "Seed test", Language: "pt-BR", DefaultTimezone: "America/Sao_Paulo", Status: "ACTIVE"}).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := bus.RegisterQuery(resolve_definition.Query{}, func(context.Context, any) (any, error) { return resolve_definition.Result{}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := publish_template.Setup(publish_template.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	if err := Setup(Dependencies{Bus: bus}); err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: actorID, TenantID: tenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: []string{auth.AdminProduct}, Internal: true}})
	segmentIDs := []identity.ID{identity.NewID(), identity.NewID(), identity.NewID()}
	segments := []string{segmentIDs[0].String(), segmentIDs[1].String(), segmentIDs[2].String()}
	legacy := catalog.TemplateDocument{SchemaVersion: catalog.LegacySchemaVersion, ParticipantRoles: []string{"PROPERTY_OWNER"}, DefaultComparisonMode: catalog.ChecklistOnly, Requirements: []catalog.CaptureRequirement{{Key: "overview", Section: "property", Label: "Visão geral", EvidenceKind: "PHOTO", MinimumCount: 1, MaximumCount: 10, Required: true, CaptureSourcePolicy: "CAMERA_DEFAULT"}}, AnalysisType: "REAL_ESTATE", Policy: catalog.Policy{GPSRequired: true, GeofenceMeters: catalog.DefaultGeofence}}
	for i, seed := range catalog.CuratedSeeds("REAL_ESTATE", segments[0], segments[1], segments[2]) {
		legacy.SegmentVersionID = segments[i]
		payload, _ := json.Marshal(legacy)
		if _, err := bus.Send(ctx, publish_template.Command{TenantID: tenantID, Key: seed.TemplateKey, Name: seed.TemplateKey, IdempotencyKey: "curated-seed:" + seed.TemplateKey + ":" + segments[i], DefinitionJSON: payload}); err != nil {
			t.Fatalf("publish v1 %s: %v", seed.TemplateKey, err)
		}
	}
	command := Command{TenantID: tenantID, AnalysisType: "REAL_ESTATE", PropertySegmentVersionID: segmentIDs[0], ConstructionSegmentVersionID: segmentIDs[1], CleaningSegmentVersionID: segmentIDs[2]}
	raw, err := bus.Send(ctx, command)
	if err != nil {
		t.Fatalf("publish curated seeds: %v", err)
	}
	first := raw.(Result)
	for key, id := range first.Versions {
		var version database.TemplateVersion
		if err := db.First(&version, "id=?", id).Error; err != nil {
			t.Fatal(err)
		}
		if version.VersionNumber != 2 || version.SchemaVersion != 2 {
			t.Fatalf("%s published version=%d schema=%d, want v2/schema2", key, version.VersionNumber, version.SchemaVersion)
		}
	}
	replayedRaw, err := bus.Send(ctx, command)
	if err != nil {
		t.Fatalf("replay curated seeds: %v", err)
	}
	replayed := replayedRaw.(Result)
	for key, id := range first.Versions {
		if replayed.Versions[key] != id {
			t.Fatalf("%s replay returned version %s, first publication returned %s", key, replayed.Versions[key], id)
		}
		var template database.Template
		if err := db.Where("tenant_id=? AND key=?", tenantID, key).First(&template).Error; err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := db.Model(&database.TemplateVersion{}).Where("tenant_id=? AND template_id=?", tenantID, template.ID).Count(&count).Error; err != nil || count != 2 {
			t.Fatalf("%s replay template version count=%d, want 2, err=%v", key, count, err)
		}
	}
}
