//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	"inspection/services/inspection/internal/features/templates/catalog"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

// newPhotoTemplateGraphQLFixture resolves templates through the real
// resolve_template slice; asset and participant reads stay stubbed and the
// asset reads its template from the database on every call.
func newPhotoTemplateGraphQLFixture(t *testing.T) fixedOriginGraphQLFixture {
	t.Helper()
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Skipf("integration dependencies unavailable: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	seed, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := bus.RegisterQuery(assetget.Query{}, func(ctx context.Context, raw any) (any, error) {
		query := raw.(assetget.Query)
		if query.TenantID != seed.TenantID || query.AssetID != seed.AssetID {
			return nil, apperror.New(apperror.NotFound, "", "resource not found")
		}
		var asset database.Asset
		if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=?", seed.TenantID, seed.AssetID).Take(&asset).Error
		}); err != nil {
			return nil, err
		}
		return assetcore.View{Asset: database.Asset{ID: seed.AssetID, TenantID: seed.TenantID, BusinessUnitID: seed.BusinessUnitID, TemplateID: asset.TemplateID, Status: "ACTIVE", Version: 1}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(participantget.Query{}, func(_ context.Context, raw any) (any, error) {
		query := raw.(participantget.Query)
		if query.TenantID != seed.TenantID || query.ParticipantID != seed.ParticipantID {
			return nil, apperror.New(apperror.NotFound, "", "resource not found")
		}
		return participantcore.ParticipantView{Participant: database.Participant{ID: seed.ParticipantID, TenantID: seed.TenantID, BusinessUnitID: seed.BusinessUnitID, Status: "ACTIVE", Version: 1}, Selected: []identity.ID{identity.NewID()}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := templateresolve.Setup(templateresolve.Dependencies{DB: h.DB, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Bus: bus, InspectionService: inspectioncore.Service{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}}}))
	server.SetErrorPresenter(graph.PresentError)
	meta := requestctx.Metadata{TenantID: seed.TenantID, Principal: requestctx.Principal{IdentityID: seed.IdentityID, TenantID: seed.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "vv-photo-template"}
	return fixedOriginGraphQLFixture{ctx: requestctx.WithMetadata(ctx, meta), h: h, seed: seed, server: server, bus: bus}
}

type photoTemplateRefs struct{ segment string }

func (r photoTemplateRefs) SegmentExists(value string) bool    { return value == r.segment }
func (photoTemplateRefs) AnalysisTypeExists(value string) bool { return value == "REAL_ESTATE" }

// publishPhotoTemplateWithoutVideoFlag compiles a schema-1 document that omits
// isVideo and makes it the asset's active template.
func publishPhotoTemplateWithoutVideoFlag(t *testing.T, fx fixedOriginGraphQLFixture) (identity.ID, identity.ID) {
	t.Helper()
	segment := fx.seed.SegmentVersionID.String()
	raw := `{"schemaVersion":1,"segmentVersionId":"` + segment + `","participantRoles":["TENANT_PARTICIPANT"],"defaultComparisonMode":"CHECKLIST_ONLY","requirements":[{"key":"front","section":"outside","label":"Front","evidenceKind":"PHOTO","minimumCount":1,"maximumCount":2,"required":true,"captureSourcePolicy":"CAMERA_DEFAULT"}],"analysisType":"REAL_ESTATE","policy":{"geofenceMeters":150,"allowGallery":true}}`
	if strings.Contains(raw, "isVideo") {
		t.Fatal("fixture must omit isVideo")
	}
	compiled, err := catalog.Compile([]byte(raw), photoTemplateRefs{segment: segment})
	if err != nil {
		t.Fatalf("compile schema-1 photo template: %v", err)
	}
	if compiled.Document.MediaKind() != "PHOTO" || compiled.Document.IsVideo {
		t.Fatalf("omitted isVideo compiled as %s", compiled.Document.MediaKind())
	}
	templateID, versionID := identity.NewID(), identity.NewID()
	if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Create(&database.Template{ID: templateID, TenantID: fx.seed.TenantID, Key: "photo-" + templateID.String(), Name: "Schema-1 photo", SegmentVersionID: fx.seed.SegmentVersionID, ActiveVersionID: &versionID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.TemplateVersion{ID: versionID, TenantID: fx.seed.TenantID, TemplateID: templateID, VersionNumber: 1, SchemaVersion: catalog.SchemaVersion, DefinitionJSON: compiled.Canonical, CanonicalDigest: compiled.Digest, Status: "ACTIVE", IdempotencyKey: "photo-v1-" + versionID.String(), PublishedAt: now, CreatedBy: fx.seed.IdentityID}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Asset{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.AssetID).Update("template_id", templateID).Error
	}); err != nil {
		t.Fatal(err)
	}
	return templateID, versionID
}

func createPhotoTemplateInspection(t *testing.T, fx fixedOriginGraphQLFixture, key string) identity.ID {
	t.Helper()
	now := time.Now().UTC()
	create := `mutation { createInspection(input:{assetId:"` + fx.seed.AssetID.String() + `",participantId:"` + fx.seed.ParticipantID.String() + `",comparisonMode:CHECKLIST_ONLY,dueAt:"` + now.Add(time.Hour).Format(time.RFC3339) + `",deadlineAt:"` + now.Add(24*time.Hour).Format(time.RFC3339) + `",reminderInstants:[],reason:"` + key + `",clientMutationId:"` + key + `"}) { inspection { id } } }`
	created := executeTask06GraphQL(t, fx.server, create, fx.ctx)
	var payload struct {
		Data struct {
			Create struct {
				Inspection struct {
					ID string `json:"id"`
				} `json:"inspection"`
			} `json:"createInspection"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(created), &payload); err != nil || payload.Data.Create.Inspection.ID == "" || strings.Contains(created, `"errors"`) {
		t.Fatalf("createInspection from photo template: %s (%v)", created, err)
	}
	id, err := identity.ParseID(payload.Data.Create.Inspection.ID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func assertPhotoInspection(t *testing.T, fx fixedOriginGraphQLFixture, inspectionID, versionID identity.ID, stage string) {
	t.Helper()
	if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", fx.seed.TenantID, inspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		var drafts []database.CaptureDraft
		if err := tx.Where("tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?)", fx.seed.TenantID, fx.seed.TenantID, inspectionID).Find(&drafts).Error; err != nil {
			return err
		}
		if inspection.MediaKind != "PHOTO" || inspection.TemplateVersionID != versionID || len(drafts) == 0 {
			t.Errorf("%s: inspection media=%s templateVersion=%s drafts=%d; want PHOTO pinned to %s", stage, inspection.MediaKind, inspection.TemplateVersionID, len(drafts), versionID)
		}
		for _, draft := range drafts {
			if draft.MediaKind == "VIDEO" || draft.TemplateVersionID != versionID {
				t.Errorf("%s: capture draft media=%s templateVersion=%s; want PHOTO pinned to %s", stage, draft.MediaKind, draft.TemplateVersionID, versionID)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestVV_IT_358SchemaOnePhotoTemplateWithoutVideoFlagCapturesPhoto(t *testing.T) {
	fx := newPhotoTemplateGraphQLFixture(t)
	_, versionID := publishPhotoTemplateWithoutVideoFlag(t, fx)
	inspectionID := createPhotoTemplateInspection(t, fx, "vv-it-358")
	assertPhotoInspection(t, fx, inspectionID, versionID, "new schema-1 template")
}

func TestVV_IT_361TemplateVersionChangedToVideoKeepsCapturingP1AsPhoto(t *testing.T) {
	fx := newPhotoTemplateGraphQLFixture(t)
	templateID, v1 := publishPhotoTemplateWithoutVideoFlag(t, fx)
	p1 := createPhotoTemplateInspection(t, fx, "vv-it-361")
	// While P1 captures, a new template version switches the template to VIDEO.
	v2 := identity.NewID()
	document, err := json.Marshal(catalog.TemplateDocument{SchemaVersion: catalog.SchemaVersion, IsVideo: true, SegmentVersionID: fx.seed.SegmentVersionID.String(), DefaultComparisonMode: catalog.ChecklistOnly, AnalysisType: "REAL_ESTATE"})
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Model(&database.TemplateVersion{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, v1).Update("status", "SUPERSEDED").Error; err != nil {
			return err
		}
		if err := tx.Create(&database.TemplateVersion{ID: v2, TenantID: fx.seed.TenantID, TemplateID: templateID, VersionNumber: 2, SchemaVersion: catalog.SchemaVersion, DefinitionJSON: document, CanonicalDigest: strings.Repeat("1", 64), Status: "ACTIVE", IdempotencyKey: "photo-v2-" + v2.String(), PublishedAt: now, CreatedBy: fx.seed.IdentityID}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Template{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, templateID).Updates(map[string]any{"active_version_id": v2, "version": 2}).Error
	}); err != nil {
		t.Fatal(err)
	}
	resolved, err := fx.bus.Ask(fx.ctx, templateresolve.Query{TenantID: fx.seed.TenantID, TemplateID: templateID})
	if err != nil {
		t.Fatal(err)
	}
	var active catalog.TemplateDocument
	if err := json.Unmarshal(resolved.(templateresolve.Result).Version.DefinitionJSON, &active); err != nil || resolved.(templateresolve.Result).Version.ID != v2 || active.MediaKind() != "VIDEO" {
		t.Fatalf("active template version=%s media=%s err=%v; want VIDEO v2", resolved.(templateresolve.Result).Version.ID, active.MediaKind(), err)
	}
	assertPhotoInspection(t, fx, p1, v1, "after VIDEO template version")
}
