//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	capturebeginvideo "inspection/services/inspection/internal/features/capture/begin_video_capture"
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	dispatchcapture "inspection/services/inspection/internal/features/invitations/dispatch_capture_invitation"
	revokeinvitation "inspection/services/inspection/internal/features/invitations/revoke_invitation"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	origincore "inspection/services/inspection/internal/features/origins/core"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	"inspection/services/inspection/internal/features/templates/catalog"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"

	"github.com/99designs/gqlgen/graphql/handler"
	graph "inspection/services/inspection/internal/platform/graphql"

	"errors"
	"gorm.io/gorm"
)

func TestCreateInspectionChecklistOnlyRejectsMandatoryFixedOriginInstructionsIT092(t *testing.T) {
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
	document, err := json.Marshal(catalog.TemplateDocument{SchemaVersion: catalog.SchemaVersion, IsVideo: true, DefaultComparisonMode: catalog.FixedOrigin, AnalysisType: "REAL_ESTATE"})
	if err != nil {
		t.Fatal(err)
	}
	templateID, versionID := identity.NewID(), identity.NewID()
	asset := database.Asset{ID: fixture.AssetID, TenantID: fixture.TenantID, BusinessUnitID: fixture.BusinessUnitID, TemplateID: &templateID, Status: "ACTIVE", Version: 1}
	participant := database.Participant{ID: fixture.ParticipantID, TenantID: fixture.TenantID, BusinessUnitID: fixture.BusinessUnitID, Status: "ACTIVE", Version: 1}
	bus := mediator.New()
	if err := bus.RegisterQuery(assetget.Query{}, func(context.Context, any) (any, error) { return assetcore.View{Asset: asset}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(participantget.Query{}, func(context.Context, any) (any, error) {
		return participantcore.ParticipantView{Participant: participant, Selected: []identity.ID{identity.NewID()}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(templateresolve.Query{}, func(context.Context, any) (any, error) {
		return templateresolve.Result{Template: database.Template{ID: templateID, TenantID: fixture.TenantID}, Version: database.TemplateVersion{ID: versionID, TenantID: fixture.TenantID, TemplateID: templateID, DefinitionJSON: document}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	principal := requestctx.Principal{IdentityID: fixture.IdentityID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}}
	request := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.TenantID, Principal: principal, CorrelationID: "vv-it092"})
	service := inspectioncore.Service{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}
	now := time.Now().UTC()
	_, err = service.Create(request, inspectioncore.CreateInput{TenantID: fixture.TenantID, AssetID: fixture.AssetID, ParticipantID: fixture.ParticipantID, ComparisonMode: catalog.ChecklistOnly, Source: inspectioncore.SourceManual, SourceKey: "vistoria-video-it092", Reason: "IT-092", DueAt: now.Add(time.Hour), DeadlineAt: now.Add(24 * time.Hour)})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidInput {
		t.Fatalf("createInspection error = %v, want INVALID_INPUT", err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND source_key=?", fixture.TenantID, "vistoria-video-it092").Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			t.Errorf("invalid CHECKLIST_ONLY request created %d inspections", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateInspectionVideoChecklistOnlyWithoutOriginHasNullReferenceIT093(t *testing.T) {
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
	if err := h.AdminDB.Exec("INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, TRUE, 1, ?, ?, ?, '') ON CONFLICT (id) DO UPDATE SET enabled=TRUE", time.Now().UTC(), "isolated VV IT-093 fixture", "vv-it093").Error; err != nil {
		t.Fatal(err)
	}
	templateID, versionID := identity.NewID(), identity.NewID()
	document, err := json.Marshal(catalog.TemplateDocument{SchemaVersion: catalog.SchemaVersion, IsVideo: true, DefaultComparisonMode: catalog.ChecklistOnly, AnalysisType: "REAL_ESTATE"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		version := database.TemplateVersion{ID: versionID, TenantID: fixture.TenantID, TemplateID: templateID, VersionNumber: 1, SchemaVersion: catalog.SchemaVersion, DefinitionJSON: document, CanonicalDigest: "vv-it093", Status: "ACTIVE", IdempotencyKey: "vv-it093-version", PublishedAt: now, CreatedBy: fixture.IdentityID}
		template := database.Template{ID: templateID, TenantID: fixture.TenantID, Key: "vv-it093", Name: "VV IT-093", SegmentVersionID: fixture.SegmentVersionID, ActiveVersionID: &versionID, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&template).Error; err != nil {
			return err
		}
		if err := tx.Create(&version).Error; err != nil {
			return err
		}
		return tx.Model(&database.Asset{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.AssetID).Update("template_id", templateID).Error
	}); err != nil {
		t.Fatal(err)
	}
	asset := database.Asset{ID: fixture.AssetID, TenantID: fixture.TenantID, BusinessUnitID: fixture.BusinessUnitID, TemplateID: &templateID, Status: "ACTIVE", Version: 1}
	participant := database.Participant{ID: fixture.ParticipantID, TenantID: fixture.TenantID, BusinessUnitID: fixture.BusinessUnitID, Status: "ACTIVE", Version: 1}
	bus := mediator.New()
	if err := bus.RegisterQuery(assetget.Query{}, func(context.Context, any) (any, error) { return assetcore.View{Asset: asset}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(participantget.Query{}, func(context.Context, any) (any, error) {
		return participantcore.ParticipantView{Participant: participant, Selected: []identity.ID{identity.NewID()}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(templateresolve.Query{}, func(context.Context, any) (any, error) {
		return templateresolve.Result{Template: database.Template{ID: templateID, TenantID: fixture.TenantID}, Version: database.TemplateVersion{ID: versionID, TenantID: fixture.TenantID, TemplateID: templateID, DefinitionJSON: document}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	request := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.TenantID, Principal: requestctx.Principal{IdentityID: fixture.IdentityID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "vv-it093"})
	now := time.Now().UTC()
	view, err := (inspectioncore.Service{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}).Create(request, inspectioncore.CreateInput{TenantID: fixture.TenantID, AssetID: fixture.AssetID, ParticipantID: fixture.ParticipantID, ComparisonMode: catalog.ChecklistOnly, Source: inspectioncore.SourceManual, SourceKey: "vistoria-video-it093", Reason: "IT-093", DueAt: now.Add(time.Hour), DeadlineAt: now.Add(24 * time.Hour)})
	if err != nil {
		t.Fatalf("createInspection CHECKLIST_ONLY: %v", err)
	}
	if view.Inspection.MediaKind != "VIDEO" || view.Reference.ComparisonMode != string(catalog.ChecklistOnly) || view.Reference.ReferenceVersionID != nil {
		t.Fatalf("created inspection did not remain current-only: inspection=%+v reference=%+v", view.Inspection, view.Reference)
	}
}

func TestVV_IT_088CreateChecklistOnlyVideoThroughGraphQLAndKeepReferenceNull(t *testing.T) {
	fx := newOriginChecklistOnlyTransportFixture(t)
	inspectionID := fx.create(t, "VV-IT-088-current-only")
	fx.assertNullChecklistReference(t, inspectionID)
}

func TestReplayCreateInspectionReturnsSameInspectionAndSingleInvitationIT084IT097(t *testing.T) {
	for _, tc := range []struct {
		id, key string
	}{{"IT-084", "k6"}, {"IT-097", "k7"}} {
		t.Run(tc.id, func(t *testing.T) {
			fx := newInspectionModeGraphQLFixture(t, false, catalog.ChecklistOnly)
			if err := fx.h.AdminDB.Exec("INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, TRUE, 1, ?, ?, ?, '') ON CONFLICT (id) DO UPDATE SET enabled=TRUE", time.Now().UTC(), "isolated "+tc.id+" fixture", tc.id).Error; err != nil {
				t.Fatalf("enable video admission in disposable integration database: %v", err)
			}
			create := func() string {
				now := time.Now().UTC()
				query := `mutation { createInspection(input:{assetId:"` + fx.seed.AssetID.String() + `",participantId:"` + fx.seed.ParticipantID.String() + `",comparisonMode:CHECKLIST_ONLY,dueAt:"` + now.Add(time.Hour).Format(time.RFC3339) + `",deadlineAt:"` + now.Add(24*time.Hour).Format(time.RFC3339) + `",reminderInstants:[],reason:"` + tc.id + `",clientMutationId:"` + tc.key + `"}) { inspection { id } } }`
				response := executeTask06GraphQL(t, fx.server, query, fx.ctx)
				var payload struct {
					Data struct {
						Create struct {
							Inspection struct {
								ID string `json:"id"`
							} `json:"inspection"`
						} `json:"createInspection"`
					} `json:"data"`
					Errors []json.RawMessage `json:"errors"`
				}
				if err := json.Unmarshal([]byte(response), &payload); err != nil || len(payload.Errors) != 0 || payload.Data.Create.Inspection.ID == "" {
					t.Fatalf("createInspection(%s) failed: response=%s err=%v", tc.key, response, err)
				}
				return payload.Data.Create.Inspection.ID
			}
			processInvitation := func(inspectionID identity.ID) identity.ID {
				handler, err := dispatchcapture.Setup(dispatchcapture.Dependencies{Notifications: f6DisabledNotifications{}, CaptureBaseURL: "https://capture.example.test"})
				if err != nil {
					t.Fatal(err)
				}
				var invitationID identity.ID
				if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
					var intent database.OutboxIntent
					if err := tx.Where("tenant_id=? AND type=?", fx.seed.TenantID, "inspection.created.v1").Take(&intent).Error; err != nil {
						return err
					}
					var envelope events.RawEnvelope
					if err := json.Unmarshal(intent.Payload, &envelope); err != nil {
						return err
					}
					if envelope.AggregateID != inspectionID {
						return fmt.Errorf("outbox event references %s, expected %s", envelope.AggregateID, inspectionID)
					}
					if err := handler(context.Background(), tx, envelope); err != nil {
						return err
					}
					var responsibility database.Responsibility
					if err := tx.Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, inspectionID).Take(&responsibility).Error; err != nil {
						return err
					}
					var invitation database.Invitation
					if err := tx.Where("tenant_id=? AND responsibility_id=?", fx.seed.TenantID, responsibility.ID).Take(&invitation).Error; err != nil {
						return err
					}
					invitationID = invitation.ID
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				return invitationID
			}
			first := ""
			if tc.key == "k7" {
				_ = create() // Simulate losing the GraphQL response after commit.
				if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
					var committed database.Inspection
					if err := tx.Where("tenant_id=? AND source=? AND source_key=?", fx.seed.TenantID, inspectioncore.SourceManual, tc.key).Take(&committed).Error; err != nil {
						return err
					}
					first = committed.ID.String()
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			} else {
				first = create()
			}
			inspectionID, err := identity.ParseID(first)
			if err != nil {
				t.Fatal(err)
			}
			firstInvitation := processInvitation(inspectionID)
			second := create()
			if first != second {
				t.Fatalf("replay %s returned different inspection IDs: %s and %s", tc.key, first, second)
			}
			secondInvitation := processInvitation(inspectionID)
			if firstInvitation != secondInvitation {
				t.Fatalf("replay %s returned different invitation IDs: %s and %s", tc.key, firstInvitation, secondInvitation)
			}
			if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
				var inspectionCount, invitationCount int64
				if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND source=? AND source_key=?", fx.seed.TenantID, inspectioncore.SourceManual, tc.key).Count(&inspectionCount).Error; err != nil {
					return err
				}
				if err := tx.Model(&database.Invitation{}).Where("tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?)", fx.seed.TenantID, fx.seed.TenantID, inspectionID).Count(&invitationCount).Error; err != nil {
					return err
				}
				if inspectionCount != 1 || invitationCount != 1 {
					return fmt.Errorf("replay %s persisted %d inspections and %d invitations; want 1 each", tc.key, inspectionCount, invitationCount)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type f6DisabledNotifications struct{}

func (f6DisabledNotifications) Send(context.Context, notificationcore.Notification) (notificationcore.NotificationResult, error) {
	return notificationcore.NotificationResult{}, apperror.New(apperror.IntegrationDisabled, "channel", "notification channel disabled for isolated test")
}

func TestActivateOriginAfterV2CreationLeavesChecklistOnlyIT091(t *testing.T) {
	fx := newOriginChecklistOnlyTransportFixture(t)
	id := fx.create(t, "IT-091")
	if _, err := (origincore.Service{DB: fx.h.DB, Within: fx.h.WithinTenant}).ActivateCompleted(fx.ctx, fx.origin.TenantID, fx.origin.ResponsibilityID); err != nil {
		t.Fatalf("activate O1: %v", err)
	}
	fx.assertNullChecklistReference(t, id)
}

func TestCreateChecklistOnlyWithoutAssetBusinessUnitScopeReturnsNotFoundIT095(t *testing.T) {
	fx := newInspectionModeGraphQLFixture(t, false, catalog.ChecklistOnly)
	if err := fx.h.AdminDB.Exec("INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, TRUE, 1, ?, ?, ?, '') ON CONFLICT (id) DO UPDATE SET enabled=TRUE", time.Now().UTC(), "isolated IT-095 fixture", "vv-it095").Error; err != nil {
		t.Fatal(err)
	}
	meta, ok := requestctx.FromContext(fx.ctx)
	if !ok {
		t.Fatal("fixture request metadata missing")
	}
	meta.Principal.Roles = []string{auth.Manager}
	meta.Principal.Scopes = []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: identity.NewID()}}
	ctx := requestctx.WithMetadata(context.Background(), meta)
	key := "IT-095-" + identity.NewID().String()
	now := time.Now().UTC()
	query := `mutation { createInspection(input:{assetId:"` + fx.seed.AssetID.String() + `",participantId:"` + fx.seed.ParticipantID.String() + `",comparisonMode:CHECKLIST_ONLY,dueAt:"` + now.Add(time.Hour).Format(time.RFC3339) + `",deadlineAt:"` + now.Add(24*time.Hour).Format(time.RFC3339) + `",reminderInstants:[],reason:"unauthorized",clientMutationId:"` + key + `"}) { inspection { id } } }`
	response := executeTask06GraphQL(t, fx.server, query, ctx)
	var payload struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Extensions map[string]any `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil {
		t.Fatalf("decode response %s: %v", response, err)
	}
	if len(payload.Errors) != 1 || payload.Errors[0].Extensions["code"] != string(apperror.NotFound) || string(payload.Data) != "null" {
		t.Fatalf("unauthorized asset scope should return NOT_FOUND with null data, got %s", response)
	}
	if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND source_key=?", fx.seed.TenantID, key).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("unauthorized request created %d inspections", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateChecklistOnlyWithBusinessUnitScopeAndInsufficientRoleReturnsForbiddenVVIT(t *testing.T) {
	fx := newInspectionModeGraphQLFixture(t, false, catalog.ChecklistOnly)
	meta, ok := requestctx.FromContext(fx.ctx)
	if !ok {
		t.Fatal("fixture request metadata missing")
	}
	meta.Principal.Roles = []string{auth.Viewer}
	meta.Principal.Scopes = []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: fx.seed.BusinessUnitID}}
	ctx := requestctx.WithMetadata(context.Background(), meta)
	key := "VV-IT-095-FORBIDDEN-" + identity.NewID().String()
	now := time.Now().UTC()
	query := `mutation { createInspection(input:{assetId:"` + fx.seed.AssetID.String() + `",participantId:"` + fx.seed.ParticipantID.String() + `",comparisonMode:CHECKLIST_ONLY,dueAt:"` + now.Add(time.Hour).Format(time.RFC3339) + `",deadlineAt:"` + now.Add(24*time.Hour).Format(time.RFC3339) + `",reminderInstants:[],reason:"insufficient-role",clientMutationId:"` + key + `"}) { inspection { id } } }`
	response := executeTask06GraphQL(t, fx.server, query, ctx)
	var payload struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Extensions map[string]any `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil {
		t.Fatalf("decode response %s: %v", response, err)
	}
	if len(payload.Errors) != 1 || payload.Errors[0].Extensions["code"] != string(apperror.Forbidden) || string(payload.Data) != "null" {
		t.Fatalf("insufficient role with matching scope should return FORBIDDEN with null data, got %s", response)
	}
	if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND source_key=?", fx.seed.TenantID, key).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("forbidden request created %d inspections", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRevokedInspectionInvitationRejectsOldVideoSessionWithoutAttemptIT086(t *testing.T) {
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
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", seed.TenantID, seed.ResponsibilityID).Updates(map[string]any{"kind": "INSPECTION", "media_kind": "VIDEO", "status": "OPEN"}).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		return tx.Create(&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: seed.TenantID, ResponsibilityID: seed.ResponsibilityID, DisclosureVersion: invitationcore.PrivacyDisclosureVersion, PhotoProcessing: false, VideoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	pepper := []byte(strings.Repeat("p", 32))
	token, err := security.NewScopedToken(seed.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "it086-csrf"
	hash := security.HashToken(token)
	proof, err := security.CSRFProof(hash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	invitation := database.Invitation{ID: identity.NewID(), TenantID: seed.TenantID, ResponsibilityID: seed.ResponsibilityID, TokenHash: hash[:], DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := h.AdminDB.Create(&invitation).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Create(&database.ExternalSession{ID: identity.NewID(), TenantID: seed.TenantID, InvitationID: invitation.ID, ResponsibilityID: seed.ResponsibilityID, SessionDigest: hash[:], CSRFDigest: proof[:], ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	invitations := invitationcore.Service{DB: h.DB, Pepper: pepper}
	if err := revokeinvitation.Setup(revokeinvitation.Dependencies{Bus: bus, Service: invitations}); err != nil {
		t.Fatal(err)
	}
	if err := capturebeginvideo.Setup(capturebeginvideo.Dependencies{DB: h.DB, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	server := captureGraphQLServer(bus, invitations)
	credentials := requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf}
	revoke := `mutation { revokeInvitation(input:{linkToken:"` + token + `",clientMutationId:"it086-revoke"}) { status } }`
	revokeResponse := executeCaptureGraphQL(t, server, revoke, seed.TenantID, credentials, true)
	if !strings.Contains(revokeResponse, `"status":"REVOKED"`) || strings.Contains(revokeResponse, `"errors"`) {
		t.Fatalf("revoke invitation failed: %s", revokeResponse)
	}
	begin := `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"it086-begin"}) { attempt { id } } }`
	beginResponse := executeCaptureGraphQL(t, server, begin, seed.TenantID, credentials, true)
	if !strings.Contains(beginResponse, string(apperror.SessionExpired)) || strings.Contains(beginResponse, `"attempt":{`) {
		t.Fatalf("revoked session should be inaccessible without attempt, got %s", beginResponse)
	}
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&database.VideoAttempt{}).Where("tenant_id=? AND responsibility_id=?", seed.TenantID, seed.ResponsibilityID).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("revoked session created %d video attempts", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOriginActivationRacesChecklistOnlyCreationIT096(t *testing.T) {
	for n := 0; n < 3; n++ {
		t.Run(fmt.Sprintf("race_%d", n+1), func(t *testing.T) {
			fx := newOriginChecklistOnlyTransportFixture(t)
			var wg sync.WaitGroup
			wg.Add(2)
			var inspectionID identity.ID
			var createErr, activationErr error
			go func() {
				defer wg.Done()
				inspectionID, createErr = fx.createResult(t, "IT-096-"+identity.NewID().String())
			}()
			go func() {
				defer wg.Done()
				_, activationErr = (origincore.Service{DB: fx.h.DB, Within: fx.h.WithinTenant}).ActivateCompleted(fx.ctx, fx.origin.TenantID, fx.origin.ResponsibilityID)
			}()
			wg.Wait()
			if createErr != nil {
				t.Fatalf("create V2 during O1 activation: %v", createErr)
			}
			if activationErr != nil {
				t.Fatalf("activate O1 during V2 creation: %v", activationErr)
			}
			fx.assertNullChecklistReference(t, inspectionID)
		})
	}
}

type originChecklistOnlyTransportFixture struct {
	h      *harness.Harness
	origin harness.VideoOriginFixture
	seed   harness.Task06Fixture
	ctx    context.Context
	server *handler.Server
}

func newOriginChecklistOnlyTransportFixture(t *testing.T) originChecklistOnlyTransportFixture {
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
	origin, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var base database.Inspection
	var participant database.Participant
	var asset database.Asset
	var baseTemplate database.Template
	var member database.Membership
	if err := h.WithinTenant(ctx, origin.TenantID, func(tx *gorm.DB) error {
		var responsibility database.Responsibility
		if err := tx.Where("tenant_id=? AND id=?", origin.TenantID, origin.ResponsibilityID).Take(&responsibility).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", origin.TenantID, responsibility.InspectionID).Take(&base).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", origin.TenantID, base.ParticipantID).Take(&participant).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", origin.TenantID, origin.AssetID).Take(&asset).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", origin.TenantID, base.TemplateID).Take(&baseTemplate).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND role=?", origin.TenantID, auth.TenantAdmin).Take(&member).Error
	}); err != nil {
		t.Fatal(err)
	}
	seed := harness.Task06Fixture{TenantID: origin.TenantID, IdentityID: member.IdentityID, MembershipID: member.ID, BusinessUnitID: asset.BusinessUnitID, ParticipantID: participant.ID, AssetID: asset.ID, SegmentVersionID: baseTemplate.SegmentVersionID}
	templateID, templateVersionID, err := createVideoInspectionTemplateWithMode(ctx, h, seed, catalog.ChecklistOnly)
	if err != nil {
		t.Fatal(err)
	}
	asset.TemplateID = &templateID
	var templateVersion database.TemplateVersion
	if err := h.WithinTenant(ctx, origin.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", origin.TenantID, templateVersionID).Take(&templateVersion).Error
	}); err != nil {
		t.Fatal(err)
	}
	document, err := json.Marshal(catalog.TemplateDocument{SchemaVersion: catalog.SchemaVersion, IsVideo: true, DefaultComparisonMode: catalog.ChecklistOnly, AnalysisType: "REAL_ESTATE"})
	if err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := bus.RegisterQuery(assetget.Query{}, func(context.Context, any) (any, error) { return assetcore.View{Asset: asset}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(participantget.Query{}, func(context.Context, any) (any, error) {
		return participantcore.ParticipantView{Participant: participant, Selected: []identity.ID{identity.NewID()}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(templateresolve.Query{}, func(context.Context, any) (any, error) {
		templateVersion.DefinitionJSON = document
		return templateresolve.Result{Template: database.Template{ID: templateID, TenantID: origin.TenantID}, Version: templateVersion}, nil
	}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Bus: bus, InspectionService: inspectioncore.Service{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}}}))
	server.SetErrorPresenter(graph.PresentError)
	principal := requestctx.Principal{IdentityID: member.IdentityID, MembershipID: member.ID, TenantID: origin.TenantID, Roles: []string{auth.TenantAdmin}}
	operator := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: origin.TenantID, Principal: principal, CorrelationID: "vv-it091-096"})
	if err := h.AdminDB.Exec("INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, TRUE, 1, ?, ?, ?, '') ON CONFLICT (id) DO UPDATE SET enabled=TRUE", time.Now().UTC(), "isolated IT-091/096 fixture", "vv-it091-096").Error; err != nil {
		t.Fatal(err)
	}
	return originChecklistOnlyTransportFixture{h: h, origin: origin, seed: seed, ctx: operator, server: server}
}

func (fx originChecklistOnlyTransportFixture) create(t *testing.T, clientID string) identity.ID {
	t.Helper()
	id, err := fx.createResult(t, clientID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (fx originChecklistOnlyTransportFixture) createResult(t *testing.T, clientID string) (identity.ID, error) {
	t.Helper()
	now := time.Now().UTC()
	query := `mutation { createInspection(input:{assetId:"` + fx.seed.AssetID.String() + `",participantId:"` + fx.seed.ParticipantID.String() + `",comparisonMode:CHECKLIST_ONLY,dueAt:"` + now.Add(time.Hour).Format(time.RFC3339) + `",deadlineAt:"` + now.Add(24*time.Hour).Format(time.RFC3339) + `",reminderInstants:[],reason:"` + clientID + `",clientMutationId:"` + clientID + `"}) { inspection { id } } }`
	response := executeTask06GraphQL(t, fx.server, query, fx.ctx)
	var payload struct {
		Data struct {
			Create struct {
				Inspection struct {
					ID string `json:"id"`
				} `json:"inspection"`
			} `json:"createInspection"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil {
		return identity.ID{}, err
	}
	if len(payload.Errors) != 0 || payload.Data.Create.Inspection.ID == "" {
		return identity.ID{}, fmt.Errorf("createInspection %s failed: %s", clientID, response)
	}
	return identity.ParseID(payload.Data.Create.Inspection.ID)
}

func (fx originChecklistOnlyTransportFixture) assertNullChecklistReference(t *testing.T, inspectionID identity.ID) {
	t.Helper()
	if err := fx.h.WithinTenant(context.Background(), fx.origin.TenantID, func(tx *gorm.DB) error {
		var snapshot database.ReferenceSnapshot
		if err := tx.Where("tenant_id=? AND inspection_id=?", fx.origin.TenantID, inspectionID).Take(&snapshot).Error; err != nil {
			return err
		}
		if snapshot.ComparisonMode != string(catalog.ChecklistOnly) || snapshot.ReferenceVersionID != nil {
			return fmt.Errorf("V2 must remain CHECKLIST_ONLY with null reference, got mode=%s reference=%v", snapshot.ComparisonMode, snapshot.ReferenceVersionID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
