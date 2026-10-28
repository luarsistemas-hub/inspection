//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	"inspection/services/inspection/internal/features/origins/resolve_reference"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	"inspection/services/inspection/internal/features/templates/catalog"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

func TestCreateInspectionFixedOriginWithoutPreparedOriginGraphQLIT079(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	templateID, templateVersionID, err := createVideoInspectionTemplate(ctx, h, fixture)
	if err != nil {
		t.Fatal(err)
	}

	bus := mediator.New()
	if err := bus.RegisterQuery(assetget.Query{}, func(_ context.Context, raw any) (any, error) {
		query := raw.(assetget.Query)
		if query.TenantID != fixture.TenantID || query.AssetID != fixture.AssetID {
			t.Fatalf("unexpected asset lookup: %+v", query)
		}
		return assetcore.View{Asset: database.Asset{ID: fixture.AssetID, TenantID: fixture.TenantID, BusinessUnitID: fixture.BusinessUnitID, TemplateID: &templateID, Status: "ACTIVE", Version: 1}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(participantget.Query{}, func(_ context.Context, raw any) (any, error) {
		query := raw.(participantget.Query)
		if query.TenantID != fixture.TenantID || query.ParticipantID != fixture.ParticipantID {
			t.Fatalf("unexpected participant lookup: %+v", query)
		}
		return participantcore.ParticipantView{Participant: database.Participant{ID: fixture.ParticipantID, TenantID: fixture.TenantID, BusinessUnitID: fixture.BusinessUnitID, Status: "ACTIVE", Version: 1}, Selected: []identity.ID{identity.NewID()}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	document, err := json.Marshal(catalog.TemplateDocument{SchemaVersion: catalog.SchemaVersion, IsVideo: true, DefaultComparisonMode: catalog.FixedOrigin, AnalysisType: "REAL_ESTATE"})
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(templateresolve.Query{}, func(_ context.Context, raw any) (any, error) {
		query := raw.(templateresolve.Query)
		if query.TenantID != fixture.TenantID || query.TemplateID != templateID {
			t.Fatalf("unexpected template lookup: %+v", query)
		}
		return templateresolve.Result{Template: database.Template{ID: templateID, TenantID: fixture.TenantID}, Version: database.TemplateVersion{ID: templateVersionID, TenantID: fixture.TenantID, TemplateID: templateID, DefinitionJSON: document}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := resolve_reference.Setup(resolve_reference.Dependencies{DB: h.DB, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{
		DB: h.DB, Bus: bus,
		InspectionService: inspectioncore.Service{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}},
	}}))
	server.SetErrorPresenter(graph.PresentError)
	meta := requestctx.Metadata{TenantID: fixture.TenantID, Principal: requestctx.Principal{IdentityID: fixture.IdentityID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "it079"}
	requestContext := requestctx.WithMetadata(context.Background(), meta)
	query := `mutation { createInspection(input:{assetId:"` + fixture.AssetID.String() + `",participantId:"` + fixture.ParticipantID.String() + `",comparisonMode:FIXED_ORIGIN,dueAt:"2026-10-17T12:00:00Z",deadlineAt:"2026-10-18T12:00:00Z",reminderInstants:[],reason:"IT-079",clientMutationId:"IT-079"}) { inspection { id } userErrors { code field } clientMutationId } }`
	response := executeTask06GraphQL(t, server, query, requestContext)
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Extensions map[string]any `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(response), &envelope); err != nil {
		t.Fatalf("decode GraphQL response: %v; response=%s", err, response)
	}
	if len(envelope.Errors) != 1 || envelope.Errors[0].Extensions["code"] != "INVALID_INPUT" || envelope.Errors[0].Extensions["field"] != "referenceVersionId" {
		t.Fatalf("expected GraphQL INVALID_INPUT on referenceVersionId using the established error envelope, got %s", response)
	}
	if string(envelope.Data) != "null" {
		t.Fatalf("expected null mutation data, got %s", response)
	}
	assertIT079NoMutationSideEffects(t, ctx, h, fixture, "IT-079")
}

func createVideoInspectionTemplate(ctx context.Context, h *harness.Harness, fixture harness.Task06Fixture) (identity.ID, identity.ID, error) {
	return createVideoInspectionTemplateWithMode(ctx, h, fixture, catalog.FixedOrigin)
}

func createVideoInspectionTemplateWithMode(ctx context.Context, h *harness.Harness, fixture harness.Task06Fixture, mode catalog.ComparisonMode) (identity.ID, identity.ID, error) {
	document, err := json.Marshal(catalog.TemplateDocument{SchemaVersion: catalog.SchemaVersion, IsVideo: true, DefaultComparisonMode: mode, AnalysisType: "REAL_ESTATE"})
	if err != nil {
		return identity.ID{}, identity.ID{}, err
	}
	templateID, versionID := identity.NewID(), identity.NewID()
	err = h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		version := database.TemplateVersion{ID: versionID, TenantID: fixture.TenantID, TemplateID: templateID, VersionNumber: 1, SchemaVersion: catalog.SchemaVersion, DefinitionJSON: document, CanonicalDigest: strings.Repeat("0", 64), Status: "ACTIVE", IdempotencyKey: "it079-template-version", PublishedAt: now, CreatedBy: fixture.IdentityID}
		template := database.Template{ID: templateID, TenantID: fixture.TenantID, Key: "it079-video", Name: "IT-079 video template", SegmentVersionID: fixture.SegmentVersionID, ActiveVersionID: &versionID, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&template).Error; err != nil {
			return err
		}
		if err := tx.Create(&version).Error; err != nil {
			return err
		}
		return tx.Model(&database.Asset{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.AssetID).Update("template_id", templateID).Error
	})
	return templateID, versionID, err
}

func assertIT079NoMutationSideEffects(t *testing.T, ctx context.Context, h *harness.Harness, fixture harness.Task06Fixture, sourceKey string) {
	t.Helper()
	err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		checks := []struct {
			name  string
			model any
			query string
			args  []any
		}{
			{"inspection", &database.Inspection{}, "tenant_id=? AND source_key=?", []any{fixture.TenantID, sourceKey}},
			{"responsibility", &database.Responsibility{}, "tenant_id=? AND inspection_id IN (SELECT id FROM inspections.inspections WHERE tenant_id=? AND source_key=?)", []any{fixture.TenantID, fixture.TenantID, sourceKey}},
			{"policy snapshot", &database.PolicySnapshot{}, "tenant_id=? AND inspection_id IN (SELECT id FROM inspections.inspections WHERE tenant_id=? AND source_key=?)", []any{fixture.TenantID, fixture.TenantID, sourceKey}},
			{"reference snapshot", &database.ReferenceSnapshot{}, "tenant_id=? AND inspection_id IN (SELECT id FROM inspections.inspections WHERE tenant_id=? AND source_key=?)", []any{fixture.TenantID, fixture.TenantID, sourceKey}},
			{"capture draft", &database.CaptureDraft{}, "tenant_id=? AND responsibility_id IN (SELECT r.id FROM inspections.responsibilities r JOIN inspections.inspections i ON i.id=r.inspection_id WHERE i.tenant_id=? AND i.source_key=?)", []any{fixture.TenantID, fixture.TenantID, sourceKey}},
			{"invitation", &database.Invitation{}, "tenant_id=?", []any{fixture.TenantID}},
		}
		for _, check := range checks {
			var count int64
			if err := tx.Model(check.model).Where(check.query, check.args...).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return gorm.ErrRecordNotFound
			}
		}
		var fallbackCount int64
		if err := tx.Model(&database.ReferenceSnapshot{}).Where("tenant_id=? AND comparison_mode=? AND inspection_id IN (SELECT id FROM inspections.inspections WHERE tenant_id=? AND source_key=?)", fixture.TenantID, string(catalog.ChecklistOnly), fixture.TenantID, sourceKey).Count(&fallbackCount).Error; err != nil {
			return err
		}
		if fallbackCount != 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			t.Fatal("IT-079 created inspection-related rows or a CHECKLIST_ONLY fallback")
		}
		t.Fatalf("check IT-079 mutation side effects: %v", err)
	}
}
