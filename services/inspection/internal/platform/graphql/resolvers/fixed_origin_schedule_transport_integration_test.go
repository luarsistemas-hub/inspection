//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	originactivate "inspection/services/inspection/internal/features/origins/activate_version"
	origincore "inspection/services/inspection/internal/features/origins/core"
	origininvalidate "inspection/services/inspection/internal/features/origins/invalidate_version"
	originpromotevideo "inspection/services/inspection/internal/features/origins/promote_inspection_video"
	"inspection/services/inspection/internal/features/origins/resolve_reference"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	"inspection/services/inspection/internal/features/schedules/core"
	"inspection/services/inspection/internal/features/templates/catalog"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/testfailure"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

func TestFixedOriginMissingReferenceGraphQLIT455(t *testing.T) {
	for _, entryPoint := range []string{"createInspection", "createSchedule", "planInspection"} {
		t.Run(entryPoint, func(t *testing.T) {
			fx := newFixedOriginGraphQLFixture(t, false)
			clientID := "IT-455-" + entryPoint
			response := executeFixedOriginEntry(t, fx, entryPoint, clientID)
			assertFixedOriginInvalidInput(t, response)
			assertFixedOriginNoSideEffects(t, fx, clientID)
		})
	}
}

func TestFixedOriginPhotoReferenceForVideoGraphQLIT454(t *testing.T) {
	for _, entryPoint := range []string{"createInspection", "createSchedule", "planInspection"} {
		t.Run(entryPoint, func(t *testing.T) {
			fx := newFixedOriginGraphQLFixture(t, true)
			clientID := "IT-454-" + entryPoint
			response := executeFixedOriginEntry(t, fx, entryPoint, clientID)
			assertFixedOriginInvalidInput(t, response)
			assertFixedOriginNoSideEffects(t, fx, clientID)
		})
	}
}

func TestVV_IT_449ProtectedCreationOperationsRequireAuthentication(t *testing.T) {
	for _, entryPoint := range []string{"createInspection", "createSchedule", "planInspection"} {
		t.Run(entryPoint, func(t *testing.T) {
			fx := newFixedOriginGraphQLFixture(t, false)
			setFixedVideoAdmission(t, fx)
			versionID := seedFixedVideoOrigin(t, fx)
			query := fixedOriginEntryQuery(fx, entryPoint, "VV-IT-449-"+entryPoint, versionID.String())
			response := executeFixedOriginRequest(t, fx, query, context.Background(), "", "")
			assertFixedOriginTransportError(t, response, "UNAUTHENTICATED")
		})
	}
}

func TestVV_IT_450CreationOperationsRequireManagementCapability(t *testing.T) {
	for _, entryPoint := range []string{"createInspection", "createSchedule", "planInspection"} {
		t.Run(entryPoint, func(t *testing.T) {
			fx := newFixedOriginGraphQLFixture(t, false)
			setFixedVideoAdmission(t, fx)
			versionID := seedFixedVideoOrigin(t, fx)
			viewer := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: requestctx.Principal{IdentityID: fx.seed.IdentityID, TenantID: fx.seed.TenantID, Roles: []string{auth.Viewer}}, CorrelationID: "vv-it-450"})
			query := fixedOriginEntryQuery(fx, entryPoint, "VV-IT-450-"+entryPoint, versionID.String())
			response := executeFixedOriginRequest(t, fx, query, viewer, "", "")
			assertFixedOriginTransportError(t, response, "FORBIDDEN")
		})
	}
}

func TestVV_IT_067OriginMutationsForbidViewer(t *testing.T) {
	fx := newFixedOriginGraphQLFixture(t, false)
	setFixedVideoAdmission(t, fx)
	versionID := seedFixedVideoOrigin(t, fx)
	viewer := requestctx.WithMetadata(context.Background(), requestctx.Metadata{
		TenantID: fx.seed.TenantID,
		Principal: requestctx.Principal{
			IdentityID: fx.seed.IdentityID,
			TenantID:   fx.seed.TenantID,
			Roles:      []string{auth.Viewer},
		},
		CorrelationID: "vv-it-067",
	})
	mutations := []struct {
		name  string
		query string
	}{
		{
			name:  "activateOriginVersion",
			query: `mutation { activateOriginVersion(input:{versionId:"` + versionID.String() + `",clientMutationId:"vv-it-067-activate"}) { version { id } userErrors { code } clientMutationId } }`,
		},
		{
			name:  "invalidateOriginVersion",
			query: `mutation { invalidateOriginVersion(input:{versionId:"` + versionID.String() + `",clientMutationId:"vv-it-067-invalidate"}) { version { id } userErrors { code } clientMutationId } }`,
		},
		{
			name:  "promoteInspectionVideo",
			query: `mutation { promoteInspectionVideo(input:{inspectionId:"` + fx.seed.InspectionID.String() + `",clientMutationId:"vv-it-067"}) { version { id } userErrors { code } clientMutationId } }`,
		},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			response := executeFixedOriginRequest(t, fx, mutation.query, viewer, "", "")
			assertFixedOriginTransportError(t, response, "FORBIDDEN")
		})
	}
}

func TestVV_IT_457SuspendedAdmissionRejectsNewVideoResponsibilities(t *testing.T) {
	fx := newFixedOriginGraphQLFixture(t, false)
	if err := fx.h.AdminDB.Exec("INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, FALSE, 2, ?, ?, ?, '') ON CONFLICT (id) DO UPDATE SET enabled=FALSE, version=2", time.Now().UTC(), "IT-457 suspended admission", "vv-it-457").Error; err != nil {
		t.Fatal(err)
	}
	versionID := seedFixedVideoOrigin(t, fx)
	for _, entryPoint := range []string{"createInspection", "createSchedule", "planInspection"} {
		t.Run(entryPoint, func(t *testing.T) {
			clientID := "VV-IT-457-" + entryPoint
			query := fixedOriginEntryQuery(fx, entryPoint, clientID, versionID.String())
			response := executeFixedOriginRequest(t, fx, query, fx.ctx, "", "")
			assertFixedOriginTransportError(t, response, "INTEGRATION_DISABLED")
			assertFixedOriginNoSideEffects(t, fx, clientID)
		})
	}
}

func TestVV_IT_452CreationAdapterUnavailableReturnsDependencyError(t *testing.T) {
	for _, entryPoint := range []string{"createInspection", "createSchedule", "planInspection"} {
		t.Run(entryPoint, func(t *testing.T) {
			fx := newInspectionModeGraphQLFixture(t, false, catalog.ChecklistOnly)
			if err := testfailure.Install(fx.h.DB); err != nil {
				t.Fatal(err)
			}
			query := strings.Replace(fixedOriginEntryQuery(fx, entryPoint, "VV-IT-452-"+entryPoint, ""), "comparisonMode:FIXED_ORIGIN", "comparisonMode:CHECKLIST_ONLY", 1)
			response := executeFixedOriginRequest(t, fx, query, fx.ctx, entryPoint, "dependency")
			assertFixedOriginTransportError(t, response, "DEPENDENCY_UNAVAILABLE")
		})
	}
}

func TestVV_IT_453CreationInternalErrorHidesSecret(t *testing.T) {
	for _, entryPoint := range []string{"createInspection", "createSchedule", "planInspection"} {
		t.Run(entryPoint, func(t *testing.T) {
			fx := newInspectionModeGraphQLFixture(t, false, catalog.ChecklistOnly)
			if err := testfailure.Install(fx.h.DB); err != nil {
				t.Fatal(err)
			}
			query := strings.Replace(fixedOriginEntryQuery(fx, entryPoint, "VV-IT-453-"+entryPoint, ""), "comparisonMode:FIXED_ORIGIN", "comparisonMode:CHECKLIST_ONLY", 1)
			response := executeFixedOriginRequest(t, fx, query, fx.ctx, entryPoint, "internal")
			assertFixedOriginTransportError(t, response, "INTERNAL")
			if strings.Contains(response, testfailure.Secret) {
				t.Fatalf("internal adapter error leaked secret: %s", response)
			}
		})
	}
}

func TestVV_IT_456InvalidatedOriginRejectsAllCreationOperations(t *testing.T) {
	for _, entryPoint := range []string{"createInspection", "createSchedule", "planInspection"} {
		t.Run(entryPoint, func(t *testing.T) {
			fx := newFixedOriginGraphQLFixture(t, true)
			var invalidated database.OriginVersion
			if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
				var origin database.Origin
				if err := tx.Where("tenant_id=? AND asset_id=?", fx.seed.TenantID, fx.seed.AssetID).Take(&origin).Error; err != nil {
					return err
				}
				if err := tx.Where("tenant_id=? AND origin_id=?", fx.seed.TenantID, origin.ID).Order("version_number DESC").Take(&invalidated).Error; err != nil {
					return err
				}
				return tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, invalidated.ID).Updates(map[string]any{"status": "INVALIDATED", "media_kind": "VIDEO"}).Error
			}); err != nil {
				t.Fatal(err)
			}
			clientID := "VV-IT-456-" + entryPoint
			query := fixedOriginEntryQuery(fx, entryPoint, clientID, invalidated.ID.String())
			response := executeFixedOriginRequest(t, fx, query, fx.ctx, "", "")
			assertFixedOriginTransportError(t, response, "INVALID_STATE")
			assertFixedOriginNoSideEffects(t, fx, clientID)
		})
	}
}

func TestVV_IT_451ForeignTenantResourcesAreNotFound(t *testing.T) {
	for _, entryPoint := range []string{"createInspection", "createSchedule", "planInspection"} {
		for _, resource := range []string{"asset", "participant", "template", "reference"} {
			t.Run(entryPoint+"/"+resource, func(t *testing.T) {
				fx := newFixedOriginGraphQLFixture(t, resource == "reference")
				foreign, err := fx.h.SeedVideoOriginFixture(context.Background(), harness.VideoOriginFixtureOptions{Active: true})
				if err != nil {
					t.Fatal(err)
				}
				foreignBase, err := fx.h.SeedTask06Fixture(context.Background(), harness.Task06FixtureOptions{})
				if err != nil {
					t.Fatal(err)
				}
				query := fixedOriginEntryQuery(fx, entryPoint, "VV-IT-451-"+entryPoint+"-"+resource, "")
				switch resource {
				case "asset":
					query = strings.Replace(query, fx.seed.AssetID.String(), foreign.AssetID.String(), 1)
				case "participant":
					query = strings.Replace(query, fx.seed.ParticipantID.String(), foreignBase.ParticipantID.String(), 1)
				case "template":
					query = strings.Replace(query, fx.templateID.String(), foreignBase.TemplateID.String(), 1)
				case "reference":
					var local database.OriginVersion
					if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
						var origin database.Origin
						if err := tx.Where("tenant_id=? AND asset_id=?", fx.seed.TenantID, fx.seed.AssetID).Take(&origin).Error; err != nil {
							return err
						}
						return tx.Where("tenant_id=? AND origin_id=?", fx.seed.TenantID, origin.ID).Take(&local).Error
					}); err != nil {
						t.Fatal(err)
					}
					if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
						return tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, local.ID).Update("media_kind", "VIDEO").Error
					}); err != nil {
						t.Fatal(err)
					}
					query = strings.Replace(query, `comparisonMode:FIXED_ORIGIN`, `comparisonMode:FIXED_ORIGIN,referenceVersionId:"`+foreign.OriginVersionID.String()+`"`, 1)
				}
				response := executeFixedOriginRequest(t, fx, query, fx.ctx, "", "")
				assertFixedOriginTransportError(t, response, "NOT_FOUND")
				if strings.Contains(response, foreign.TenantID.String()) || strings.Contains(response, foreign.AssetID.String()) || strings.Contains(response, foreign.OriginVersionID.String()) {
					t.Fatalf("foreign tenant metadata leaked: %s", response)
				}
			})
		}
	}
}

func TestVV_IT_448VideoFixedOriginIsPinnedByEveryCreationOperation(t *testing.T) {
	for _, entryPoint := range []string{"createInspection", "createSchedule", "planInspection"} {
		t.Run(entryPoint, func(t *testing.T) {
			fx := newFixedOriginGraphQLFixture(t, false)
			setFixedVideoAdmission(t, fx)
			originVersionID := seedFixedVideoOrigin(t, fx)
			clientID := "VV-IT-448-" + entryPoint
			query := fixedOriginEntryQuery(fx, entryPoint, clientID, originVersionID.String())
			response := executeFixedOriginRequest(t, fx, query, fx.ctx, "", "")
			if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"data"`) {
				t.Fatalf("%s failed to create with FIXED_ORIGIN VIDEO: %s", entryPoint, response)
			}
			var envelope map[string]any
			if err := json.Unmarshal([]byte(response), &envelope); err != nil {
				t.Fatal(err)
			}
			data := envelope["data"].(map[string]any)[entryPoint].(map[string]any)
			createdID := ""
			if entryPoint == "createInspection" {
				createdID = data["inspection"].(map[string]any)["id"].(string)
			} else {
				createdID = data["schedule"].(map[string]any)["id"].(string)
			}
			if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
				var schedule database.Schedule
				if entryPoint != "createInspection" {
					scheduleID, err := identity.ParseID(createdID)
					if err != nil {
						return err
					}
					if err := tx.Where("tenant_id=? AND id=?", fx.seed.TenantID, scheduleID).Take(&schedule).Error; err != nil {
						return fmt.Errorf("load persisted schedule %s: %w", scheduleID, err)
					}
					if schedule.ComparisonMode != string(catalog.FixedOrigin) || schedule.ReferenceVersionID == nil || *schedule.ReferenceVersionID != originVersionID {
						t.Fatalf("%s schedule did not pin O1 as FIXED_ORIGIN: %+v", entryPoint, schedule)
					}
				}
				if entryPoint == "createInspection" {
					var inspection database.Inspection
					inspectionID, err := identity.ParseID(createdID)
					if err != nil {
						return err
					}
					if err := tx.Where("tenant_id=? AND id=?", fx.seed.TenantID, inspectionID).Take(&inspection).Error; err != nil {
						return err
					}
					var snapshot database.ReferenceSnapshot
					var snapshots int64
					if err := tx.Model(&database.ReferenceSnapshot{}).Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, inspection.ID).Count(&snapshots).Error; err != nil {
						return err
					}
					if snapshots != 1 {
						t.Fatalf("%s inspection has %d pinned reference snapshots, want exactly one", entryPoint, snapshots)
					}
					if err := tx.Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, inspection.ID).Take(&snapshot).Error; err != nil {
						return err
					}
					if snapshot.ComparisonMode != string(catalog.FixedOrigin) || snapshot.ReferenceVersionID == nil || *snapshot.ReferenceVersionID != originVersionID {
						t.Fatalf("%s inspection did not pin O1 as FIXED_ORIGIN: %+v", entryPoint, snapshot)
					}
				}
				var template database.Template
				if err := tx.Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.templateID).Take(&template).Error; err != nil {
					return err
				}
				var version database.TemplateVersion
				if template.ActiveVersionID == nil {
					return gorm.ErrRecordNotFound
				}
				if err := tx.Where("tenant_id=? AND id=?", fx.seed.TenantID, *template.ActiveVersionID).Take(&version).Error; err != nil {
					return fmt.Errorf("load persisted video template version: %w", err)
				}
				var document catalog.TemplateDocument
				if err := json.Unmarshal(version.DefinitionJSON, &document); err != nil {
					return err
				}
				if !document.IsVideo {
					t.Fatalf("persisted template version is not VIDEO: %+v", document)
				}
				return nil
			}); err != nil {
				t.Fatalf("verify persisted %s creation: %v", entryPoint, err)
			}
		})
	}
}

func seedFixedVideoOrigin(t *testing.T, fx fixedOriginGraphQLFixture) identity.ID {
	t.Helper()
	now := time.Now().UTC()
	originID, versionID, prepID, mediaID, frameID, scopeID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	digest := strings.Repeat("a", 64)
	err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fx.seed.TenantID, ResponsibilityID: fx.seed.ResponsibilityID, ObjectKey: "vv-it-448/source/" + mediaID.String(), ContentType: "video/mp4", SHA256: digest, SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: prepID, TenantID: fx.seed.TenantID, MediaID: mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.OriginVersion{ID: versionID, TenantID: fx.seed.TenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: fx.seed.ResponsibilityID, Status: "ACTIVE", MediaKind: "VIDEO", VideoPreparationID: &prepID, ScopeManifestDigest: digest, IdempotencyKey: "vv-it-448-" + versionID.String(), CreatedAt: now, ActivatedAt: &now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Origin{ID: originID, TenantID: fx.seed.TenantID, AssetID: fx.seed.AssetID, ActiveVersionID: &versionID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fx.seed.TenantID, SourceMediaID: mediaID, PreparationID: prepID, PTSMicros: 0, SHA256: digest, ObjectKey: "vv-it-448/" + frameID.String(), Width: 1, Height: 1, Role: "BASELINE", Quality: "GOOD", SizeBytes: 1, CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoScopeItem{ID: scopeID, TenantID: fx.seed.TenantID, PreparationID: prepID, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now}).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	return versionID
}

func setFixedVideoAdmission(t *testing.T, fx fixedOriginGraphQLFixture) {
	t.Helper()
	if err := fx.h.AdminDB.Exec("INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, TRUE, 1, ?, ?, ?, '') ON CONFLICT (id) DO UPDATE SET enabled=TRUE", time.Now().UTC(), "isolated VIDEO creation fixture", "vv-video-creation").Error; err != nil {
		t.Fatal(err)
	}
}

type fixedOriginGraphQLFixture struct {
	ctx        context.Context
	h          *harness.Harness
	seed       harness.Task06Fixture
	server     *handler.Server
	bus        *mediator.Bus
	templateID identity.ID
}

func newFixedOriginGraphQLFixture(t *testing.T, photoOrigin bool) fixedOriginGraphQLFixture {
	return newInspectionModeGraphQLFixture(t, photoOrigin, catalog.FixedOrigin)
}

func newInspectionModeGraphQLFixture(t *testing.T, photoOrigin bool, mode catalog.ComparisonMode) fixedOriginGraphQLFixture {
	t.Helper()
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
	templateID, templateVersionID, err := createVideoInspectionTemplateWithMode(ctx, h, seed, mode)
	if err != nil {
		t.Fatal(err)
	}
	if photoOrigin {
		if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
			versionID, originID, now := identity.NewID(), identity.NewID(), time.Now().UTC()
			if err := tx.Create(&database.Origin{ID: originID, TenantID: seed.TenantID, AssetID: seed.AssetID, ActiveVersionID: &versionID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.OriginVersion{ID: versionID, TenantID: seed.TenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: seed.ResponsibilityID, Status: "ACTIVE", MediaKind: "PHOTO", IdempotencyKey: "it454-photo-origin", CreatedAt: now}).Error; err != nil {
				return err
			}
			return tx.Create(&database.OriginEvidence{ID: identity.NewID(), TenantID: seed.TenantID, OriginVersionID: versionID, MediaID: seed.MediaID, Category: "ROOM", Description: "Existing photo baseline", AttentionItems: json.RawMessage("[]"), CreatedAt: now}).Error
		}); err != nil {
			t.Fatal(err)
		}
	}
	bus := mediator.New()
	if err := bus.RegisterQuery(assetget.Query{}, func(_ context.Context, raw any) (any, error) {
		query := raw.(assetget.Query)
		if query.TenantID != seed.TenantID || query.AssetID != seed.AssetID {
			return nil, apperror.New(apperror.NotFound, "", "resource not found")
		}
		return assetcore.View{Asset: database.Asset{ID: seed.AssetID, TenantID: seed.TenantID, BusinessUnitID: seed.BusinessUnitID, TemplateID: &templateID, Status: "ACTIVE", Version: 1}}, nil
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
	document, err := json.Marshal(catalog.TemplateDocument{SchemaVersion: catalog.SchemaVersion, IsVideo: true, DefaultComparisonMode: mode, AnalysisType: "REAL_ESTATE"})
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(templateresolve.Query{}, func(_ context.Context, raw any) (any, error) {
		query := raw.(templateresolve.Query)
		if query.TenantID != seed.TenantID || query.TemplateID != templateID {
			return nil, apperror.New(apperror.NotFound, "", "resource not found")
		}
		return templateresolve.Result{Template: database.Template{ID: templateID, TenantID: seed.TenantID}, Version: database.TemplateVersion{ID: templateVersionID, TenantID: seed.TenantID, TemplateID: templateID, DefinitionJSON: document}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := resolve_reference.Setup(resolve_reference.Dependencies{DB: h.DB, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	originService := origincore.Service{DB: h.DB}
	if err := originactivate.Setup(originactivate.Dependencies{DB: h.DB, Bus: bus, Service: originService, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	if err := origininvalidate.Setup(origininvalidate.Dependencies{DB: h.DB, Bus: bus, Service: originService, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	if err := originpromotevideo.Setup(originpromotevideo.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{
		DB: h.DB, Bus: bus,
		InspectionService: inspectioncore.Service{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}},
		ScheduleService:   core.Service{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}},
	}}))
	server.SetErrorPresenter(graph.PresentError)
	meta := requestctx.Metadata{TenantID: seed.TenantID, Principal: requestctx.Principal{IdentityID: seed.IdentityID, TenantID: seed.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "it45x"}
	return fixedOriginGraphQLFixture{ctx: requestctx.WithMetadata(ctx, meta), h: h, seed: seed, server: server, bus: bus, templateID: templateID}
}

func executeFixedOriginEntry(t *testing.T, fx fixedOriginGraphQLFixture, entryPoint, clientID string) string {
	t.Helper()
	return executeFixedOriginEntryWithReference(t, fx, entryPoint, clientID, "")
}

func executeFixedOriginEntryWithReference(t *testing.T, fx fixedOriginGraphQLFixture, entryPoint, clientID, referenceID string) string {
	t.Helper()
	query := fixedOriginEntryQuery(fx, entryPoint, clientID, referenceID)
	return executeTask06GraphQL(t, fx.server, query, fx.ctx)
}

func fixedOriginEntryQuery(fx fixedOriginGraphQLFixture, entryPoint, clientID, referenceID string) string {
	common := `assetId:"` + fx.seed.AssetID.String() + `",participantId:"` + fx.seed.ParticipantID.String() + `",templateId:"` + fx.templateID.String() + `",comparisonMode:FIXED_ORIGIN,clientMutationId:"` + clientID + `"`
	if referenceID != "" {
		common += `,referenceVersionId:"` + referenceID + `"`
	}
	var query string
	switch entryPoint {
	case "createInspection":
		query = `mutation { createInspection(input:{` + common + `,dueAt:"2026-10-17T12:00:00Z",deadlineAt:"2026-10-18T12:00:00Z",reminderInstants:[],reason:"` + clientID + `"}) { inspection { id } userErrors { code field } clientMutationId } }`
	case "createSchedule":
		query = `mutation { createSchedule(input:{` + common + `,rrule:"FREQ=DAILY",timezone:"UTC",startsAt:"2026-10-17T12:00:00Z",deadlineMinutes:60,reminderOffsetsMinutes:[]}) { schedule { id } userErrors { code field } clientMutationId } }`
	case "planInspection":
		query = `mutation { planInspection(input:{` + common + `,rrule:"FREQ=DAILY",timezone:"UTC",startsAt:"2026-10-17T12:00:00Z",deadlineMinutes:60,reminderOffsetsMinutes:[]}) { inspection { id } schedule { id } userErrors { code field } clientMutationId } }`
	default:
		panic("unknown entry point " + entryPoint)
	}
	return query
}

func executeFixedOriginRequest(t *testing.T, fx fixedOriginGraphQLFixture, query string, ctx context.Context, operation, failure string) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+quote(query)+`}`))
	req.Header.Set("Content-Type", "application/json")
	if operation != "" {
		req.Header.Set(testfailure.HeaderOperation, operation)
		req.Header.Set(testfailure.HeaderFailure, failure)
	}
	req = req.WithContext(ctx)
	if operation != "" {
		req = testfailure.WithRequest(req, "test")
	}
	fx.server.ServeHTTP(w, req)
	return w.Body.String()
}

func assertFixedOriginTransportError(t *testing.T, response, code string) {
	t.Helper()
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Extensions map[string]any `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(response), &envelope); err != nil {
		t.Fatalf("decode GraphQL response: %v; response=%s", err, response)
	}
	if string(envelope.Data) != "null" || len(envelope.Errors) == 0 {
		t.Fatalf("expected null data and GraphQL error %s, got %s", code, response)
	}
	for _, item := range envelope.Errors {
		if item.Extensions["code"] == code {
			return
		}
	}
	t.Fatalf("expected GraphQL error %s, got %s", code, response)
}

func assertFixedOriginInvalidInput(t *testing.T, response string) {
	t.Helper()
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
		t.Fatalf("expected GraphQL INVALID_INPUT on referenceVersionId, got %s", response)
	}
	if string(envelope.Data) != "null" {
		t.Fatalf("expected null mutation data, got %s", response)
	}
}

func assertFixedOriginNoSideEffects(t *testing.T, fx fixedOriginGraphQLFixture, clientID string) {
	t.Helper()
	err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		for _, check := range []struct {
			name  string
			model any
			query string
			args  []any
		}{
			{"schedule", &database.Schedule{}, "tenant_id=? AND idempotency_key LIKE ?", []any{fx.seed.TenantID, "%" + clientID + "%"}},
			{"inspection", &database.Inspection{}, "tenant_id=? AND source_key LIKE ?", []any{fx.seed.TenantID, clientID + "%"}},
		} {
			var count int64
			if err := tx.Model(check.model).Where(check.query, check.args...).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return gorm.ErrRecordNotFound
			}
		}
		return nil
	})
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			t.Fatalf("rejected mutation %s created a schedule or inspection", clientID)
		}
		t.Fatalf("check rejected mutation side effects: %v", err)
	}
	if strings.Contains(clientID, "IT-455") {
		assertIT079NoMutationSideEffects(t, fx.ctx, fx.h, fx.seed, clientID)
	}
}
