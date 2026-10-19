//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	origincore "inspection/services/inspection/internal/features/origins/core"
	origininvite "inspection/services/inspection/internal/features/origins/invite_capture"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	"inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

type originInviteNotificationStub struct{}

func (originInviteNotificationStub) Send(context.Context, notificationcore.Notification) (notificationcore.NotificationResult, error) {
	return notificationcore.NotificationResult{ID: identity.NewID(), State: notificationcore.State("SENT")}, nil
}

func TestInviteOriginCaptureAuthenticatedOperatorCreatesScopedInvitationIT030(t *testing.T) {
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
	definition := json.RawMessage(`{"schemaVersion":1,"requirements":[{"key":"room","section":"property","label":"Room","evidenceKind":"PHOTO","required":true,"minimumCount":1,"maximumCount":1}],"analysisType":"REAL_ESTATE","policy":{"gpsRequired":false,"geofenceMeters":0,"allowGallery":true}}`)
	var asset database.Asset
	var participant database.Participant
	var contact database.ParticipantContact
	var templateVersion database.TemplateVersion
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", seed.TenantID, seed.AssetID).Take(&asset).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", seed.TenantID, seed.ParticipantID).Take(&participant).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", seed.TenantID, seed.ContactID).Take(&contact).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND id=?", seed.TenantID, seed.TemplateVersionID).Take(&templateVersion).Error
	}); err != nil {
		t.Fatal(err)
	}
	templateVersion.DefinitionJSON = definition
	bus := mediator.New()
	for query, result := range map[any]any{
		assetget.Query{}:         assetcore.View{Asset: asset},
		participantget.Query{}:   participantcore.ParticipantView{Participant: participant, Contacts: []database.ParticipantContact{contact}, Selected: []identity.ID{contact.ID}, Verified: map[identity.ID]bool{contact.ID: true}},
		resolve_template.Query{}: resolve_template.Result{Template: database.Template{ID: seed.TemplateID, TenantID: seed.TenantID}, Version: templateVersion},
	} {
		query, result := query, result
		if err := bus.RegisterQuery(query, func(context.Context, any) (any, error) { return result, nil }); err != nil {
			t.Fatal(err)
		}
	}
	service := origincore.Service{DB: h.DB, Within: h.WithinTenant}
	if err := origininvite.Setup(origininvite.Dependencies{DB: h.DB, Bus: bus, Service: service, Notifications: originInviteNotificationStub{}, CaptureBaseURL: "https://capture.example.test", Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Bus: bus}}))
	server.SetErrorPresenter(graph.PresentError)
	mutation := `mutation { inviteOriginCapture(input:{assetId:"` + seed.AssetID.String() + `",participantId:"` + seed.ParticipantID.String() + `",expiresAt:"` + time.Now().UTC().Add(time.Hour).Format(time.RFC3339) + `",clientMutationId:"it030-origin-invite"}) { invitationId originVersionId status clientMutationId } }`
	operator := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: seed.TenantID, Principal: requestctx.Principal{IdentityID: seed.IdentityID, TenantID: seed.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "it030"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+quote(mutation)+`}`)).WithContext(operator)
	req.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(w, req)
	body := w.Body.String()
	if strings.Contains(body, `"errors"`) || !strings.Contains(body, `"status":"SENT"`) || !strings.Contains(body, `"clientMutationId":"it030-origin-invite"`) || strings.Contains(body, "linkToken") {
		t.Fatalf("authenticated origin invite returned unexpected payload: %s", body)
	}
	var payload struct {
		Data struct {
			Invite struct {
				InvitationID string `json:"invitationId"`
				VersionID    string `json:"originVersionId"`
			} `json:"inviteOriginCapture"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil || payload.Data.Invite.InvitationID == "" || payload.Data.Invite.VersionID == "" {
		t.Fatalf("invite response ids missing: %s (%v)", body, err)
	}
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		var invitation database.Invitation
		if err := tx.Where("tenant_id=? AND id=?", seed.TenantID, payload.Data.Invite.InvitationID).Take(&invitation).Error; err != nil {
			return err
		}
		var version database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", seed.TenantID, payload.Data.Invite.VersionID).Take(&version).Error; err != nil {
			return err
		}
		var draft database.CaptureDraft
		if err := tx.Where("tenant_id=? AND responsibility_id=?", seed.TenantID, version.ResponsibilityID).Take(&draft).Error; err != nil {
			return err
		}
		var origin database.Origin
		if err := tx.Where("tenant_id=? AND id=?", seed.TenantID, version.OriginID).Take(&origin).Error; err != nil {
			return err
		}
		if invitation.Status != "ACTIVE" || invitation.ResponsibilityID != version.ResponsibilityID || version.Status != "DRAFT" || draft.Kind != "ORIGIN" || origin.AssetID != seed.AssetID {
			t.Errorf("invitation scope mismatch: invitation=%+v version=%+v draft=%+v origin=%+v", invitation, version, draft, origin)
		}
		var otherAssetOrigins int64
		if err := tx.Model(&database.Origin{}).Where("tenant_id=? AND asset_id<>?", seed.TenantID, seed.AssetID).Count(&otherAssetOrigins).Error; err != nil {
			return err
		}
		if otherAssetOrigins != 0 {
			t.Errorf("invitation created %d origin rows for a different asset", otherAssetOrigins)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
