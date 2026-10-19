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
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	originactivate "inspection/services/inspection/internal/features/origins/activate_version"
	origincore "inspection/services/inspection/internal/features/origins/core"
	origininvalidate "inspection/services/inspection/internal/features/origins/invalidate_version"
	origininvite "inspection/services/inspection/internal/features/origins/invite_capture"
	originlist "inspection/services/inspection/internal/features/origins/list_versions"
	originpromotevideo "inspection/services/inspection/internal/features/origins/promote_inspection_video"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	"inspection/services/inspection/internal/features/templates/catalog"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/testfailure"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

var api06Operations = []string{"inviteOriginCapture", "activateOriginVersion", "invalidateOriginVersion", "originVersions", "promoteInspectionVideo"}

func TestVV_IT_437OriginLifecycleThenPromotionKeepsFrameLineage(t *testing.T) {
	fx := newAPI06Fixture(t)
	invite := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, "inviteOriginCapture", "vv-it-437-invite", ""), fx.ctx, "", "")
	var invited struct {
		Data struct {
			Invite struct {
				VersionID string `json:"originVersionId"`
				Status    string `json:"status"`
			} `json:"inviteOriginCapture"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(invite), &invited); err != nil || invited.Data.Invite.VersionID == "" || strings.Contains(invite, `"errors"`) {
		t.Fatalf("inviteOriginCapture failed: %s (%v)", invite, err)
	}
	versionID, err := identity.ParseID(invited.Data.Invite.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	preparationID := prepareAPI06OriginCapture(t, fx, versionID, "READY", "SUBMITTED")
	activated := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, "activateOriginVersion", "vv-it-437-activate", versionID.String()), fx.ctx, "", "")
	if strings.Contains(activated, `"errors"`) || !strings.Contains(activated, `"status":"ACTIVE"`) || !strings.Contains(activated, `"videoPreparationId":"`+preparationID.String()+`"`) {
		t.Fatalf("activateOriginVersion did not pin the prepared VIDEO bank: %s", activated)
	}
	listed := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, "originVersions", "", ""), fx.ctx, "", "")
	if strings.Contains(listed, `"errors"`) || !strings.Contains(listed, `"id":"`+versionID.String()+`"`) || !strings.Contains(listed, `"mediaKind":"VIDEO"`) || !strings.Contains(listed, `"preparationStatus":"READY"`) {
		t.Fatalf("originVersions did not list the VIDEO scope of O1: %s", listed)
	}
	invalidated := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, "invalidateOriginVersion", "vv-it-437-invalidate", versionID.String()), fx.ctx, "", "")
	if strings.Contains(invalidated, `"errors"`) || !strings.Contains(invalidated, `"status":"INVALIDATED"`) {
		t.Fatalf("invalidateOriginVersion did not invalidate O1: %s", invalidated)
	}
	promotedPreparation, _ := seedPromotableVideo(t, fx.fixedOriginGraphQLFixture, "RETAINED", true)
	promoted := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, "promoteInspectionVideo", "vv-it-437-promote", ""), fx.ctx, "", "")
	if strings.Contains(promoted, `"errors"`) || !strings.Contains(promoted, `"userErrors":[]`) || !strings.Contains(promoted, `"videoPreparationId":"`+promotedPreparation.String()+`"`) || !strings.Contains(promoted, `"sourceAvailability":"RETAINED"`) || !strings.Contains(promoted, `"status":"ACTIVE"`) {
		t.Fatalf("promoteInspectionVideo lost frame lineage or source availability: %s", promoted)
	}
}

func TestVV_IT_438OriginOperationsRequireAuthentication(t *testing.T) {
	fx := newAPI06Fixture(t)
	resources := seedAPI06Resources(t, fx)
	for _, operation := range api06Operations {
		t.Run(operation, func(t *testing.T) {
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, operation, "vv-it-438-"+operation, resources.versionFor(operation)), context.Background(), "", "")
			assertFixedOriginTransportError(t, response, "UNAUTHENTICATED")
		})
	}
	resources.assertUnchanged(t, fx)
}

func TestVV_IT_439OriginOperationsRequireCapability(t *testing.T) {
	fx := newAPI06Fixture(t)
	resources := seedAPI06Resources(t, fx)
	for _, operation := range api06Operations {
		t.Run(operation, func(t *testing.T) {
			// Viewer may read origins but not manage them; a customer
			// viewer lacks the internal origin read capability.
			role := auth.Viewer
			if operation == "originVersions" {
				role = auth.CustomerViewer
			}
			actor := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: requestctx.Principal{IdentityID: fx.seed.IdentityID, TenantID: fx.seed.TenantID, Roles: []string{role}}, CorrelationID: "vv-it-439"})
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, operation, "vv-it-439-"+operation, resources.versionFor(operation)), actor, "", "")
			assertFixedOriginTransportError(t, response, "FORBIDDEN")
		})
	}
	resources.assertUnchanged(t, fx)
}

func TestVV_IT_440ForeignTenantOriginResourcesAreNotFound(t *testing.T) {
	fx := newAPI06Fixture(t)
	resources := seedAPI06Resources(t, fx)
	foreign, err := fx.h.SeedVideoOriginFixture(context.Background(), harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	foreignBase, err := fx.h.SeedTask06Fixture(context.Background(), harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"inviteOriginCapture/asset":       strings.Replace(api06Query(fx, "inviteOriginCapture", "vv-it-440-asset", ""), fx.seed.AssetID.String(), foreign.AssetID.String(), 1),
		"inviteOriginCapture/participant": strings.Replace(api06Query(fx, "inviteOriginCapture", "vv-it-440-participant", ""), fx.seed.ParticipantID.String(), foreignBase.ParticipantID.String(), 1),
		"activateOriginVersion":           api06Query(fx, "activateOriginVersion", "vv-it-440-activate", foreign.OriginVersionID.String()),
		"invalidateOriginVersion":         api06Query(fx, "invalidateOriginVersion", "vv-it-440-invalidate", foreign.OriginVersionID.String()),
		"originVersions":                  strings.Replace(api06Query(fx, "originVersions", "", ""), fx.seed.AssetID.String(), foreign.AssetID.String(), 1),
		"promoteInspectionVideo":          strings.Replace(api06Query(fx, "promoteInspectionVideo", "vv-it-440-promote", ""), fx.seed.InspectionID.String(), foreignBase.InspectionID.String(), 1),
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
			assertAPI06Rejected(t, response, "NOT_FOUND")
			for _, leaked := range []string{foreign.TenantID.String(), foreign.OriginVersionID.String(), foreignBase.TenantID.String()} {
				if strings.Contains(response, leaked) {
					t.Fatalf("foreign tenant metadata leaked: %s", response)
				}
			}
		})
	}
	resources.assertUnchanged(t, fx)
	t.Run("tenant selector is not part of the schema", func(t *testing.T) {
		query := `query { originVersions(assetId:"` + fx.seed.AssetID.String() + `",tenantId:"` + foreign.TenantID.String() + `") { nodes { id } } }`
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
		if !strings.Contains(response, `"errors"`) || !strings.Contains(response, "tenantId") || strings.Contains(response, `"nodes"`) {
			t.Fatalf("injected tenant selector was not rejected by schema validation: %s", response)
		}
	})
}

func TestVV_IT_441OriginOperationsAdapterUnavailable(t *testing.T) {
	for _, operation := range api06Operations {
		t.Run(operation, func(t *testing.T) {
			fx := newAPI06Fixture(t)
			resources := seedAPI06Resources(t, fx)
			if err := testfailure.Install(fx.h.DB); err != nil {
				t.Fatal(err)
			}
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, operation, "vv-it-441-"+operation, resources.versionFor(operation)), fx.ctx, operation, "dependency")
			assertFixedOriginTransportError(t, response, "DEPENDENCY_UNAVAILABLE")
			resources.assertUnchanged(t, fx)
		})
	}
}

func TestVV_IT_442OriginOperationsInternalErrorHidesSecret(t *testing.T) {
	for _, operation := range api06Operations {
		t.Run(operation, func(t *testing.T) {
			fx := newAPI06Fixture(t)
			resources := seedAPI06Resources(t, fx)
			if err := testfailure.Install(fx.h.DB); err != nil {
				t.Fatal(err)
			}
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, operation, "vv-it-442-"+operation, resources.versionFor(operation)), fx.ctx, operation, "internal")
			assertFixedOriginTransportError(t, response, "INTERNAL")
			if strings.Contains(response, testfailure.Secret) {
				t.Fatalf("internal adapter error leaked secret: %s", response)
			}
			resources.assertUnchanged(t, fx)
		})
	}
}

func TestVV_IT_443UnreadyActivationAndPromotionAreInvalidState(t *testing.T) {
	t.Run("activateOriginVersion", func(t *testing.T) {
		fx := newAPI06Fixture(t)
		versionID := seedAPI06DraftVersion(t, fx)
		prepareAPI06OriginCapture(t, fx, versionID, "PROCESSING", "SUBMITTED")
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, "activateOriginVersion", "vv-it-443-activate", versionID.String()), fx.ctx, "", "")
		assertAPI06Rejected(t, response, "INVALID_STATE")
		assertAPI06VersionStatus(t, fx, versionID, "DRAFT")
	})
	t.Run("promoteInspectionVideo", func(t *testing.T) {
		fx := newAPI06Fixture(t)
		seedPromotableVideo(t, fx.fixedOriginGraphQLFixture, "RETAINED", false)
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, "promoteInspectionVideo", "vv-it-443-promote", ""), fx.ctx, "", "")
		assertAPI06Rejected(t, response, "INVALID_STATE")
		assertAPI06NoPromotion(t, fx)
	})
}

func TestVV_IT_444UnsubmittedActivationAndPromotionAreInvalidState(t *testing.T) {
	t.Run("activateOriginVersion", func(t *testing.T) {
		fx := newAPI06Fixture(t)
		versionID := seedAPI06DraftVersion(t, fx)
		prepareAPI06OriginCapture(t, fx, versionID, "READY", "OPEN")
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, "activateOriginVersion", "vv-it-444-activate", versionID.String()), fx.ctx, "", "")
		assertAPI06Rejected(t, response, "INVALID_STATE")
		assertAPI06VersionStatus(t, fx, versionID, "DRAFT")
	})
	t.Run("promoteInspectionVideo", func(t *testing.T) {
		fx := newAPI06Fixture(t)
		seedPromotableVideo(t, fx.fixedOriginGraphQLFixture, "RETAINED", true)
		if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.InspectionID).Update("status", "SUBMITTED").Error
		}); err != nil {
			t.Fatal(err)
		}
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, "promoteInspectionVideo", "vv-it-444-promote", ""), fx.ctx, "", "")
		assertAPI06Rejected(t, response, "INVALID_STATE")
		assertAPI06NoPromotion(t, fx)
	})
}

func TestVV_IT_445PhotoPromotionIsInvalidState(t *testing.T) {
	fx := newAPI06Fixture(t)
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.InspectionID).Updates(map[string]any{"status": "COMPLETED", "media_kind": "PHOTO"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, "promoteInspectionVideo", "vv-it-445-promote", ""), fx.ctx, "", "")
	assertAPI06Rejected(t, response, "INVALID_STATE")
	assertAPI06NoPromotion(t, fx)
}

func TestVV_IT_447VideoInviteWhileSuspendedIsIntegrationDisabled(t *testing.T) {
	fx := newAPI06Fixture(t)
	resources := seedAPI06Resources(t, fx)
	if err := fx.h.AdminDB.Exec("UPDATE platform.video_admission SET enabled=FALSE, version=version+1 WHERE id=1").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { setFixedVideoAdmission(t, fx.fixedOriginGraphQLFixture) })
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, "inviteOriginCapture", "vv-it-447-invite", ""), fx.ctx, "", "")
	assertAPI06Rejected(t, response, "INTEGRATION_DISABLED")
	resources.assertUnchanged(t, fx)
}

type api06Fixture struct {
	fixedOriginGraphQLFixture
}

func newAPI06Fixture(t *testing.T) api06Fixture {
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
	// Origin capture needs the continuous video requirement of the template.
	document, err := json.Marshal(catalog.TemplateDocument{SchemaVersion: catalog.SchemaVersion, IsVideo: true, DefaultComparisonMode: catalog.FixedOrigin, AnalysisType: "REAL_ESTATE", Requirements: []catalog.CaptureRequirement{{Key: "video:main", Section: "property", Label: "Vídeo do imóvel", EvidenceKind: "VIDEO", MinimumCount: 1, MaximumCount: 1, Required: true, CaptureSourcePolicy: "CAMERA_DEFAULT"}}})
	if err != nil {
		t.Fatal(err)
	}
	templateID, templateVersionID := identity.NewID(), identity.NewID()
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Create(&database.Template{ID: templateID, TenantID: seed.TenantID, Key: "vv-api06-video", Name: "API06 video template", SegmentVersionID: seed.SegmentVersionID, ActiveVersionID: &templateVersionID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.TemplateVersion{ID: templateVersionID, TenantID: seed.TenantID, TemplateID: templateID, VersionNumber: 1, SchemaVersion: catalog.SchemaVersion, DefinitionJSON: document, CanonicalDigest: strings.Repeat("0", 64), Status: "ACTIVE", IdempotencyKey: "vv-api06-template-version", PublishedAt: now, CreatedBy: seed.IdentityID}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Asset{}).Where("tenant_id=? AND id=?", seed.TenantID, seed.AssetID).Update("template_id", templateID).Error
	}); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	service := origincore.Service{DB: h.DB, Within: h.WithinTenant}
	for _, setup := range []func() error{
		func() error {
			return assetget.Setup(assetget.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}})
		},
		func() error {
			return participantget.Setup(participantget.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}})
		},
		func() error { return templateresolve.Setup(templateresolve.Dependencies{DB: h.DB, Bus: bus}) },
		func() error {
			return origininvite.Setup(origininvite.Dependencies{DB: h.DB, Bus: bus, Service: service, Notifications: originInviteNotificationStub{}, CaptureBaseURL: "https://capture.example.test", Authorizer: auth.Authorizer{}})
		},
		func() error {
			return originactivate.Setup(originactivate.Dependencies{DB: h.DB, Bus: bus, Service: service, Authorizer: auth.Authorizer{}})
		},
		func() error {
			return origininvalidate.Setup(origininvalidate.Dependencies{DB: h.DB, Bus: bus, Service: service, Authorizer: auth.Authorizer{}})
		},
		func() error {
			return originlist.Setup(originlist.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Within: h.WithinTenant})
		},
		func() error {
			return originpromotevideo.Setup(originpromotevideo.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}})
		},
	} {
		if err := setup(); err != nil {
			t.Fatal(err)
		}
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Bus: bus}}))
	server.SetErrorPresenter(graph.PresentError)
	meta := requestctx.Metadata{TenantID: seed.TenantID, Principal: requestctx.Principal{IdentityID: seed.IdentityID, MembershipID: seed.MembershipID, TenantID: seed.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "vv-api06"}
	fx := api06Fixture{fixedOriginGraphQLFixture{ctx: requestctx.WithMetadata(ctx, meta), h: h, seed: seed, server: server, bus: bus, templateID: templateID}}
	setFixedVideoAdmission(t, fx.fixedOriginGraphQLFixture)
	return fx
}

func api06Query(fx api06Fixture, operation, clientID, versionID string) string {
	version := `version { id status mediaKind videoPreparationId preparationStatus sourceAvailability } userErrors { code field } clientMutationId`
	switch operation {
	case "inviteOriginCapture":
		return `mutation { inviteOriginCapture(input:{assetId:"` + fx.seed.AssetID.String() + `",participantId:"` + fx.seed.ParticipantID.String() + `",expiresAt:"` + time.Now().UTC().Add(time.Hour).Format(time.RFC3339) + `",clientMutationId:"` + clientID + `"}) { invitationId originVersionId status userErrors { code field } clientMutationId } }`
	case "activateOriginVersion", "invalidateOriginVersion":
		return `mutation { ` + operation + `(input:{versionId:"` + versionID + `",clientMutationId:"` + clientID + `"}) { ` + version + ` } }`
	case "originVersions":
		return `query { originVersions(assetId:"` + fx.seed.AssetID.String() + `") { nodes { id status mediaKind videoPreparationId preparationStatus sourceAvailability } } }`
	case "promoteInspectionVideo":
		return `mutation { promoteInspectionVideo(input:{inspectionId:"` + fx.seed.InspectionID.String() + `",clientMutationId:"` + clientID + `"}) { ` + version + ` } }`
	}
	panic("unknown API06 operation " + operation)
}

// api06Resources holds valid targets for each operation so rejected requests
// can prove they left origins and invitations untouched.
type api06Resources struct {
	activatable, active  identity.ID
	invitations, active0 int64
}

func seedAPI06Resources(t *testing.T, fx api06Fixture) api06Resources {
	t.Helper()
	active := seedFixedVideoOrigin(t, fx.fixedOriginGraphQLFixture)
	activatable := seedAPI06DraftVersion(t, fx)
	prepareAPI06OriginCapture(t, fx, activatable, "READY", "SUBMITTED")
	seedPromotableVideo(t, fx.fixedOriginGraphQLFixture, "RETAINED", true)
	resources := api06Resources{activatable: activatable, active: active}
	resources.invitations, resources.active0 = api06Counts(t, fx)
	return resources
}

func (r api06Resources) versionFor(operation string) string {
	switch operation {
	case "activateOriginVersion":
		return r.activatable.String()
	case "invalidateOriginVersion":
		return r.active.String()
	}
	return ""
}

func (r api06Resources) assertUnchanged(t *testing.T, fx api06Fixture) {
	t.Helper()
	invitations, active := api06Counts(t, fx)
	if invitations != r.invitations || active != r.active0 {
		t.Fatalf("rejected origin operation mutated state: invitations %d→%d active versions %d→%d", r.invitations, invitations, r.active0, active)
	}
	assertAPI06VersionStatus(t, fx, r.activatable, "DRAFT")
	assertAPI06VersionStatus(t, fx, r.active, "ACTIVE")
}

func api06Counts(t *testing.T, fx api06Fixture) (int64, int64) {
	t.Helper()
	var invitations, active int64
	if err := fx.h.AdminDB.Model(&database.Invitation{}).Where("tenant_id=?", fx.seed.TenantID).Count(&invitations).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND status='ACTIVE'", fx.seed.TenantID).Count(&active).Error; err != nil {
		t.Fatal(err)
	}
	return invitations, active
}

// seedAPI06DraftVersion creates a DRAFT VIDEO origin version on its own
// origin-capture responsibility.
func seedAPI06DraftVersion(t *testing.T, fx api06Fixture) identity.ID {
	t.Helper()
	now := time.Now().UTC()
	responsibilityID, draftID, versionID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		var origin database.Origin
		if err := tx.Where("tenant_id=? AND asset_id=?", fx.seed.TenantID, fx.seed.AssetID).Take(&origin).Error; err == gorm.ErrRecordNotFound {
			origin = database.Origin{ID: identity.NewID(), TenantID: fx.seed.TenantID, AssetID: fx.seed.AssetID, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&origin).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if err := tx.Create(&database.Responsibility{ID: responsibilityID, TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, ParticipantID: fx.seed.ParticipantID, Status: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.CaptureDraft{ID: draftID, TenantID: fx.seed.TenantID, ResponsibilityID: responsibilityID, Kind: "ORIGIN", MediaKind: "VIDEO", TemplateVersionID: fx.seed.TemplateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[]`), Status: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND origin_id=?", fx.seed.TenantID, origin.ID).Count(&count).Error; err != nil {
			return err
		}
		return tx.Create(&database.OriginVersion{ID: versionID, TenantID: fx.seed.TenantID, OriginID: origin.ID, VersionNumber: int(count) + 1, ResponsibilityID: responsibilityID, Status: "DRAFT", MediaKind: "VIDEO", IdempotencyKey: "vv-api06-" + versionID.String(), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	return versionID
}

// prepareAPI06OriginCapture simulates the participant submitting the origin
// video and the worker preparing its frame bank.
func prepareAPI06OriginCapture(t *testing.T, fx api06Fixture, versionID identity.ID, preparationStatus, draftStatus string) identity.ID {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	mediaID, preparationID, frameID := identity.NewID(), identity.NewID(), identity.NewID()
	digest := strings.Repeat("b", 64)
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		var version database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", fx.seed.TenantID, versionID).Take(&version).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fx.seed.TenantID, version.ResponsibilityID).Updates(map[string]any{"kind": "ORIGIN", "media_kind": "VIDEO", "status": draftStatus}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fx.seed.TenantID, ResponsibilityID: version.ResponsibilityID, ObjectKey: "vv-api06/" + mediaID.String() + ".mp4", ContentType: "video/mp4", SHA256: digest, SizeBytes: 10, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "vv-api06-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		preparation := database.VideoPreparationJob{ID: preparationID, TenantID: fx.seed.TenantID, MediaID: mediaID, Status: preparationStatus, Phase: preparationStatus, PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: digest, PendingObjectKeys: []byte(`[]`), CreatedAt: now}
		if preparationStatus == "READY" {
			preparation.ManifestDigest, preparation.FirstReadyAt = digest, &now
		} else {
			preparation.Phase = "EXTRACTING"
		}
		if err := tx.Create(&preparation).Error; err != nil {
			return err
		}
		days, expiry := 30, now.Add(30*24*time.Hour)
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fx.seed.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &now, RetentionDays: &days, SourceExpiryAt: &expiry, Status: "RETAINED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fx.seed.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: digest, ObjectKey: "vv-api06/frame/" + frameID.String(), Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 10, CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fx.seed.TenantID, PreparationID: preparationID, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	return preparationID
}

// assertAPI06Rejected accepts the rejection either as a transport error or as
// a userError, but never with a success-shaped result.
func assertAPI06Rejected(t *testing.T, response, code string) {
	t.Helper()
	var envelope struct {
		Data   map[string]map[string]json.RawMessage `json:"data"`
		Errors []struct {
			Extensions map[string]any `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(response), &envelope); err != nil {
		t.Fatalf("decode GraphQL response: %v; response=%s", err, response)
	}
	for _, item := range envelope.Errors {
		if item.Extensions["code"] == code {
			if envelope.Data != nil {
				for _, payload := range envelope.Data {
					if payload != nil {
						t.Fatalf("transport %s carried a success payload: %s", code, response)
					}
				}
			}
			return
		}
	}
	for _, payload := range envelope.Data {
		for _, field := range []string{"version", "invitationId", "originVersionId"} {
			if value, ok := payload[field]; ok && string(value) != "null" {
				t.Fatalf("rejected operation reported a result in %s: %s", field, response)
			}
		}
		if strings.Contains(string(payload["userErrors"]), `"code":"`+code+`"`) {
			return
		}
	}
	t.Fatalf("expected %s rejection, got %s", code, response)
}

func assertAPI06VersionStatus(t *testing.T, fx api06Fixture, versionID identity.ID, status string) {
	t.Helper()
	var version database.OriginVersion
	if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, versionID).Take(&version).Error; err != nil {
		t.Fatal(err)
	}
	if version.Status != status {
		t.Fatalf("origin version %s status=%s, want %s", versionID, version.Status, status)
	}
}

func assertAPI06NoPromotion(t *testing.T, fx api06Fixture) {
	t.Helper()
	var promoted int64
	if err := fx.h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND idempotency_key=?", fx.seed.TenantID, "video-promotion:"+fx.seed.InspectionID.String()).Count(&promoted).Error; err != nil {
		t.Fatal(err)
	}
	if promoted != 0 {
		t.Fatalf("rejected promotion created %d origin versions", promoted)
	}
}
