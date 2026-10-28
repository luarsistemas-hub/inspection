//go:build integration

package resolvers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	counterpartyacknowledge "inspection/services/inspection/internal/features/counterparty/acknowledge"
	counterpartycontest "inspection/services/inspection/internal/features/counterparty/contest"
	counterpartycore "inspection/services/inspection/internal/features/counterparty/core"
	counterpartyrequestotp "inspection/services/inspection/internal/features/counterparty/request_otp"
	counterpartyresend "inspection/services/inspection/internal/features/counterparty/resend"
	counterpartyresolve "inspection/services/inspection/internal/features/counterparty/resolve"
	counterpartysweep "inspection/services/inspection/internal/features/counterparty/sweep_deadlines"
	counterpartyupdatepolicy "inspection/services/inspection/internal/features/counterparty/update_policy"
	counterpartyverifyotp "inspection/services/inspection/internal/features/counterparty/verify_otp"
	counterpartyviewbaseline "inspection/services/inspection/internal/features/counterparty/view_baseline"
	"inspection/services/inspection/internal/features/inventory/activate_version"
	"inspection/services/inspection/internal/features/inventory/baseline"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/features/inventory/discard_draft"
	"inspection/services/inspection/internal/features/inventory/draft"
	"inspection/services/inspection/internal/features/inventory/trail"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	configurevideopolicy "inspection/services/inspection/internal/features/media/configure_video_policy"
	"inspection/services/inspection/internal/features/notifications/execute_delivery"
	recordinapp "inspection/services/inspection/internal/features/notifications/record_in_app"
	notificationrequest "inspection/services/inspection/internal/features/notifications/request"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	platformnotifications "inspection/services/inspection/internal/platform/notifications"
	"inspection/services/inspection/internal/platform/ratelimit"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

const counterpartyCaptureBaseURL = "https://capture.example.test"

// contextMembershipStore trusts the principal placed in the test context, so a
// test can exercise each role without seeding one membership per role.
type contextMembershipStore struct{}

func (contextMembershipStore) Resolve(ctx context.Context, _, _ identity.ID) (requestctx.Principal, error) {
	meta, _ := requestctx.FromContext(ctx)
	return meta.Principal, nil
}

type counterpartyOTPCapture struct {
	mu    sync.Mutex
	codes map[identity.ID]string
}

func (c *counterpartyOTPCapture) SendOTP(_ context.Context, _, invitationID identity.ID, code string, _ []invitationcore.DeliveryIntent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.codes[invitationID] = code
	return nil
}

func (c *counterpartyOTPCapture) code(id identity.ID) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.codes[id]
}

type allowCounterpartyITLimits struct{}

func (allowCounterpartyITLimits) AllowSend(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}
func (allowCounterpartyITLimits) AllowResend(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}
func (allowCounterpartyITLimits) AllowAttempt(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}

// counterpartyEmailAdapter records rendered e-mails and fails while failing is set.
type counterpartyEmailAdapter struct {
	mu      sync.Mutex
	failing bool
	sent    []platformnotifications.DeliveryIntent
}

func (a *counterpartyEmailAdapter) Send(_ context.Context, intent platformnotifications.DeliveryIntent) (platformnotifications.Receipt, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failing {
		return platformnotifications.Receipt{}, &platformnotifications.DeliveryError{Kind: platformnotifications.ErrorPermanent, Code: "smtp_recipient_rejected", PreSend: true}
	}
	a.sent = append(a.sent, intent)
	return platformnotifications.Receipt{Provider: "smtp", ID: identity.NewID().String(), AcceptedAt: time.Now().UTC()}, nil
}

var baselineLinkPattern = regexp.MustCompile(regexp.QuoteMeta(counterpartyCaptureBaseURL) + `/baseline/([A-Za-z0-9._~-]+)`)

func (a *counterpartyEmailAdapter) lastToken(t *testing.T) string {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sent) == 0 {
		t.Fatal("no baseline invitation e-mail was sent")
	}
	last := a.sent[len(a.sent)-1]
	match := baselineLinkPattern.FindStringSubmatch(last.Text + " " + last.HTML)
	if match == nil {
		t.Fatalf("baseline invitation e-mail has no /baseline/ link: %s", last.Text)
	}
	return match[1]
}

type counterpartyITFixture struct {
	api06Fixture
	server   *handler.Server
	otp      *counterpartyOTPCapture
	email    *counterpartyEmailAdapter
	executor *execute_delivery.Executor
}

func newCounterpartyITFixture(t *testing.T, role string) counterpartyITFixture {
	t.Helper()
	fx := newAPI06Fixture(t)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	cipher, err := platformnotifications.NewPayloadCipher("counterparty-it", map[string]string{"counterparty-it": base64.StdEncoding.EncodeToString(key)})
	if err != nil {
		t.Fatal(err)
	}
	notificationService, err := notificationrequest.Setup(notificationrequest.Dependencies{DB: fx.h.DB, Payloads: cipher, Within: fx.h.WithinTenant})
	if err != nil {
		t.Fatal(err)
	}
	email := &counterpartyEmailAdapter{}
	gateway, err := platformnotifications.NewGateway(map[platformnotifications.Channel]map[platformnotifications.Provider]platformnotifications.Adapter{platformnotifications.Email: {platformnotifications.ProviderSMTP: email}})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := execute_delivery.Setup(execute_delivery.Dependencies{DB: fx.h.AdminDB, Gateway: gateway, Payloads: cipher, LeaseDuration: time.Minute, MaxAttempts: 4, RetryDelays: []time.Duration{time.Second, time.Second, time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	otp := &counterpartyOTPCapture{codes: map[identity.ID]string{}}
	pepper := []byte(strings.Repeat("p", 32))
	inventoryService := inventorycore.Service{DB: fx.h.DB, Within: fx.h.WithinTenant, Retention: retentioncore.BaselineTransition{}}
	counterparty := counterpartycore.Service{DB: fx.h.DB, Within: fx.h.WithinTenant, Pepper: pepper, Limits: allowCounterpartyITLimits{}, Notifier: otp, Notifications: notificationService, CaptureBaseURL: counterpartyCaptureBaseURL, Inventory: inventoryService, Stage: "test"}
	inventoryService.Counterparties = counterparty
	authorizer := auth.Authorizer{Store: contextMembershipStore{}}
	for _, setup := range []func() error{
		func() error { return activate_version.Setup(fx.bus, inventoryService) },
		func() error { return discard_draft.Setup(fx.bus, inventoryService) },
		func() error { return draft.Setup(fx.bus, inventoryService) },
		func() error { return baseline.Setup(fx.bus, inventoryService) },
		func() error { return trail.Setup(fx.bus, inventoryService) },
		func() error {
			return counterpartyrequestotp.Setup(counterpartyrequestotp.Dependencies{Bus: fx.bus, Service: counterparty})
		},
		func() error {
			return counterpartyverifyotp.Setup(counterpartyverifyotp.Dependencies{Bus: fx.bus, Service: counterparty})
		},
		func() error {
			return counterpartyviewbaseline.Setup(counterpartyviewbaseline.Dependencies{Bus: fx.bus, Service: counterparty})
		},
		func() error {
			return counterpartyacknowledge.Setup(counterpartyacknowledge.Dependencies{Bus: fx.bus, Service: counterparty})
		},
		func() error {
			return counterpartycontest.Setup(counterpartycontest.Dependencies{Bus: fx.bus, Service: counterparty})
		},
		func() error {
			return counterpartyresolve.Setup(counterpartyresolve.Dependencies{Bus: fx.bus, Service: counterparty, Authorizer: authorizer})
		},
		func() error {
			return counterpartyresend.Setup(counterpartyresend.Dependencies{Bus: fx.bus, Service: counterparty, Authorizer: authorizer})
		},
		func() error {
			return counterpartyupdatepolicy.Setup(counterpartyupdatepolicy.Dependencies{Bus: fx.bus, Service: counterparty, Authorizer: authorizer})
		},
		func() error {
			return counterpartysweep.Setup(counterpartysweep.Dependencies{Bus: fx.bus, Service: counterparty})
		},
		func() error {
			return configurevideopolicy.Setup(configurevideopolicy.Dependencies{DB: fx.h.DB, Bus: fx.bus, Authorizer: authorizer, Within: fx.h.WithinTenant})
		},
	} {
		if err := setup(); err != nil {
			t.Fatal(err)
		}
	}
	invitations := invitationcore.Service{DB: fx.h.DB, Pepper: pepper, Within: fx.h.WithinTenant}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: fx.h.DB, Bus: fx.bus, Authorizer: authorizer, Invitations: invitations, Counterparty: counterparty}}))
	server.SetErrorPresenter(graph.PresentError)
	fx.ctx = inventoryITContext(fx, role)
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.ResourceScope{ID: identity.NewID(), TenantID: fx.seed.TenantID, MembershipID: fx.seed.MembershipID, Kind: "ASSET", ResourceID: fx.seed.AssetID, CreatedAt: time.Now().UTC()}).Error
	}); err != nil {
		t.Fatal(err)
	}
	return counterpartyITFixture{api06Fixture: fx, server: server, otp: otp, email: email, executor: executor}
}

// graphql executes one request through the real gqlgen server and returns the
// body plus the recorder, so cookie-issuing mutations can be inspected.
func (fx counterpartyITFixture) graphql(t *testing.T, ctx context.Context, query string) (string, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	ctx = requestctx.WithResponseWriter(requestctx.WithSecureCookies(ctx, false), w)
	fx.server.ServeHTTP(w, req.WithContext(ctx))
	return w.Body.String(), w
}

func (fx counterpartyITFixture) roleContext(role string) context.Context {
	return inventoryITContext(fx.api06Fixture, role)
}

// activateWithCounterparties seeds a READY draft with one included item and
// activates it through GraphQL, inviting the given participants.
func (fx counterpartyITFixture) activateWithCounterparties(t *testing.T, participants ...identity.ID) (identity.ID, []database.CounterpartyInvitation) {
	t.Helper()
	seed := seedInventoryDraft(t, fx.api06Fixture, false)
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.InventoryItem{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, seed.items[0]).Update("decision", "ACCEPTED").Error
	}); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(participants))
	for _, participant := range participants {
		ids = append(ids, `"`+participant.String()+`"`)
	}
	response, _ := fx.graphql(t, fx.roleContext(auth.TenantAdmin), fmt.Sprintf(`mutation { activateBaselineVersion(input:{originVersionId:"%s",purpose:"MOVE_IN",expectedOriginVersion:1,counterpartyParticipantIds:[%s],clientMutationId:"cp-activate-%s"}) { version { status } userErrors { code field } } }`, seed.version, strings.Join(ids, ","), seed.version))
	if !strings.Contains(response, `"status":"ACTIVE"`) || !strings.Contains(response, `"userErrors":[]`) {
		t.Fatalf("activation with counterparties failed: %s", response)
	}
	var invitations []database.CounterpartyInvitation
	if err := fx.h.AdminDB.Where("tenant_id=? AND origin_version_id=?", fx.seed.TenantID, seed.version).Order("created_at,id").Find(&invitations).Error; err != nil {
		t.Fatal(err)
	}
	if len(invitations) != len(participants) {
		t.Fatalf("activation created %d invitations, want %d", len(invitations), len(participants))
	}
	return seed.version, invitations
}

// deliverAll runs the delivery executor until no work is due.
func (fx counterpartyITFixture) deliverAll(t *testing.T) {
	t.Helper()
	for i := 0; i < 20; i++ {
		worked, err := fx.executor.RunDue(context.Background())
		if err != nil {
			t.Fatalf("delivery executor: %v", err)
		}
		if !worked {
			return
		}
	}
	t.Fatal("delivery executor did not drain")
}

// verifySession runs the external OTP flow and returns the credentials a
// browser would hold afterwards (HttpOnly cookie plus CSRF token).
func (fx counterpartyITFixture) verifySession(t *testing.T, token string, invitationID identity.ID) requestctx.ExternalCredentials {
	t.Helper()
	external := context.Background()
	requested, _ := fx.graphql(t, external, fmt.Sprintf(`mutation { requestCounterpartyOtp(input:{token:%s,clientMutationId:"cp-otp"}) { status userErrors { code } clientMutationId } }`, quote(token)))
	if !strings.Contains(requested, `"status":"SENT"`) || !strings.Contains(requested, `"clientMutationId":"cp-otp"`) {
		t.Fatalf("requestCounterpartyOtp failed: %s", requested)
	}
	code := fx.otp.code(invitationID)
	if code == "" {
		t.Fatal("OTP was not delivered to the counterparty channel")
	}
	verified, recorder := fx.graphql(t, external, fmt.Sprintf(`mutation { verifyCounterpartyOtp(input:{token:%s,code:"%s",clientMutationId:"cp-verify"}) { status csrfToken clientMutationId } }`, quote(token), code))
	var body struct {
		Data struct {
			Verify struct {
				Status, CsrfToken, ClientMutationID string
			} `json:"verifyCounterpartyOtp"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(verified), &body); err != nil || body.Data.Verify.Status != "ACTIVE" || body.Data.Verify.ClientMutationID != "cp-verify" {
		t.Fatalf("verifyCounterpartyOtp failed: %s (%v)", verified, err)
	}
	var session string
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == "inspection_external" {
			if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
				t.Fatalf("counterparty session cookie is not HttpOnly/Lax: %+v", cookie)
			}
			session = cookie.Value
		}
	}
	if session == "" {
		t.Fatal("verifyCounterpartyOtp did not set the inspection_external cookie")
	}
	return requestctx.ExternalCredentials{SessionToken: session, CSRFToken: body.Data.Verify.CsrfToken}
}

func (fx counterpartyITFixture) addVerifiedParticipant(t *testing.T, name string) identity.ID {
	t.Helper()
	participantID, contactID := identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		var owner database.Participant
		if err := tx.Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.ParticipantID).Take(&owner).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Participant{ID: participantID, TenantID: fx.seed.TenantID, BusinessUnitID: owner.BusinessUnitID, Name: name, SegmentRole: "OWNER", Status: "ACTIVE", Version: 1, IdempotencyKey: "cp-it-" + participantID.String(), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		address := strings.ToLower(strings.ReplaceAll(name, " ", ".")) + "@example.test"
		if err := tx.Create(&database.ParticipantContact{ID: contactID, TenantID: fx.seed.TenantID, ParticipantID: participantID, Channel: "EMAIL", Value: address, Normalized: address, Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ContactVerification{ID: identity.NewID(), TenantID: fx.seed.TenantID, ContactID: contactID, IdempotencyKey: "cp-it-verification-" + contactID.String(), Status: "VERIFIED", VerifiedAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ChannelSelection{ID: identity.NewID(), TenantID: fx.seed.TenantID, ParticipantID: participantID, ContactID: contactID, CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	return participantID
}

func TestCounterpartyIT033ActivationOTPContestNoticeAndAcceptedResolution(t *testing.T) {
	fx := newCounterpartyITFixture(t, auth.TenantAdmin)
	managerID := identity.NewID()
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Create(&database.Membership{ID: managerID, TenantID: fx.seed.TenantID, IdentityID: fx.seed.IdentityID, Issuer: "cp-it", Subject: managerID.String(), Role: auth.Manager, Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ResourceScope{ID: identity.NewID(), TenantID: fx.seed.TenantID, MembershipID: managerID, Kind: "BUSINESS_UNIT", ResourceID: fx.seed.BusinessUnitID, CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ResourceScope{ID: identity.NewID(), TenantID: fx.seed.TenantID, MembershipID: managerID, Kind: "ASSET", ResourceID: fx.seed.AssetID, CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	managerCtx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: requestctx.Principal{IdentityID: fx.seed.IdentityID, MembershipID: managerID, TenantID: fx.seed.TenantID, Roles: []string{auth.Manager}, Scopes: []requestctx.Scope{{Kind: "ASSET", ID: fx.seed.AssetID}, {Kind: "BUSINESS_UNIT", ID: fx.seed.BusinessUnitID}}, ProductEntitlements: auth.ProductsForRole(auth.Manager)}, CorrelationID: "it033-manager"})
	versionID, invitations := fx.activateWithCounterparties(t, fx.seed.ParticipantID)
	invitation := invitations[0]

	var delivery database.Delivery
	if err := fx.h.AdminDB.Where("tenant_id=? AND correlation_id LIKE ?", fx.seed.TenantID, "counterparty-"+invitation.ID.String()+"-g%").Take(&delivery).Error; err != nil {
		t.Fatalf("IT-033 activation did not request a delivery: %v", err)
	}
	if delivery.LogicalTemplate != "baseline-acknowledgment" || delivery.TemplateVersion != "v1" {
		t.Fatalf("IT-033 delivery template=%s:%s, want baseline-acknowledgment:v1", delivery.LogicalTemplate, delivery.TemplateVersion)
	}
	fx.deliverAll(t)
	token := fx.email.lastToken(t)
	credentials := fx.verifySession(t, token, invitation.ID)
	sessionCtx := requestctx.WithExternalCredentials(context.Background(), credentials)

	view, _ := fx.graphql(t, sessionCtx, `query { counterpartyBaseline { baseline { hasInventory environments { name items { name condition } } } deadlineAt response resolution readOnly newerVersionExists } }`)
	if strings.Contains(view, `"errors"`) || !strings.Contains(view, `"response":"PENDING"`) || !strings.Contains(view, `"readOnly":false`) || !strings.Contains(view, `"name":"Sofá 0"`) || strings.Contains(view, `"name":"Sofá 1"`) {
		t.Fatalf("IT-033 counterpartyBaseline view mismatch (pending item must be hidden): %s", view)
	}
	statement := "Discordo do estado do sofá.\nHá manchas não registradas."
	statementLiteral, err := json.Marshal(statement)
	if err != nil {
		t.Fatal(err)
	}
	contested, _ := fx.graphql(t, sessionCtx, fmt.Sprintf(`mutation { contestBaseline(input:{statement:%s,clientMutationId:"it033-contest"}) { response respondedAt userErrors { code field } clientMutationId } }`, statementLiteral))
	if !strings.Contains(contested, `"response":"CONTESTED"`) || !strings.Contains(contested, `"userErrors":[]`) || !strings.Contains(contested, `"clientMutationId":"it033-contest"`) {
		t.Fatalf("IT-033 contestBaseline failed: %s", contested)
	}
	assertInventoryVersionStatus(t, fx.api06Fixture, versionID, "ACTIVE")

	var outbox database.OutboxIntent
	if err := fx.h.AdminDB.Where("tenant_id=? AND type='counterparty.responded.v1' AND correlation_id=?", fx.seed.TenantID, "counterparty-"+invitation.ID.String()).Take(&outbox).Error; err != nil {
		t.Fatalf("IT-033 contestation did not emit counterparty.responded.v1: %v", err)
	}
	if strings.Contains(string(outbox.Payload), "manchas") || strings.Contains(string(outbox.Payload), token) {
		t.Fatalf("IT-033 event leaked statement or token: %s", outbox.Payload)
	}
	internalView, _ := fx.graphql(t, managerCtx, fmt.Sprintf(`query { baselineVersions(assetId:"%s",first:25) { nodes { id contestationSummary { contested open } counterpartyInvitations { id response respondedAt statement resolution resolutionReason adjustmentVersionId } } } }`, fx.seed.AssetID))
	if !strings.Contains(internalView, `"open":1`) || !strings.Contains(internalView, `"statement":"Discordo do estado do sofá.`) || !strings.Contains(internalView, `"response":"CONTESTED"`) {
		t.Fatalf("IT-033 internal manager cannot inspect the open contestation statement: %s", internalView)
	}
	var envelope events.RawEnvelope
	if err := json.Unmarshal(outbox.Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
		return recordinapp.Setup()(context.Background(), tx, envelope)
	}); err != nil {
		t.Fatalf("IT-033 in-app notice handler failed: %v", err)
	}
	var notices int64
	if err := fx.h.AdminDB.Model(&database.RecipientNotification{}).Where("tenant_id=? AND recipient_membership_id=? AND kind='COUNTERPARTY_RESPONDED' AND resource_id=?", fx.seed.TenantID, managerID, versionID).Count(&notices).Error; err != nil || notices != 1 {
		t.Fatalf("IT-033 MANAGER in-app notices=%d err=%v, want 1", notices, err)
	}

	resolved, _ := fx.graphql(t, managerCtx, fmt.Sprintf(`mutation { resolveBaselineContestation(input:{counterpartyInvitationId:"%s",outcome:"ACCEPT",reason:"Ajustar sofá",clientMutationId:"it033-resolve"}) { invitationId resolution adjustmentVersionId userErrors { code field } clientMutationId } }`, invitation.ID))
	if !strings.Contains(resolved, `"resolution":"ACCEPTED"`) || !strings.Contains(resolved, `"userErrors":[]`) || !strings.Contains(resolved, `"clientMutationId":"it033-resolve"`) {
		t.Fatalf("IT-033 resolveBaselineContestation ACCEPT failed: %s", resolved)
	}
	var stored database.CounterpartyInvitation
	if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, invitation.ID).Take(&stored).Error; err != nil || stored.AdjustmentVersionID == nil {
		t.Fatalf("IT-033 adjustment version not recorded: %+v err=%v", stored, err)
	}
	var adjustment database.OriginVersion
	if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, *stored.AdjustmentVersionID).Take(&adjustment).Error; err != nil {
		t.Fatal(err)
	}
	if adjustment.Status != "DRAFT" || adjustment.Source != "CONTESTATION" || adjustment.SourceVersionID == nil || *adjustment.SourceVersionID != versionID || !adjustment.HasInventory {
		t.Fatalf("IT-033 adjustment draft mismatch: status=%s source=%s sourceVersion=%v", adjustment.Status, adjustment.Source, adjustment.SourceVersionID)
	}
	outcome, _ := fx.graphql(t, sessionCtx, `query { counterpartyBaseline { response resolution resolutionReason adjustmentStatus readOnly } }`)
	if !strings.Contains(outcome, `"resolution":"ACCEPTED"`) || !strings.Contains(outcome, `"resolutionReason":"ACCEPTED"`) || !strings.Contains(outcome, `"adjustmentStatus":"DRAFT"`) || strings.Contains(outcome, "Ajustar sofá") {
		t.Fatalf("IT-033 counterparty must see the outcome category, not the internal reason: %s", outcome)
	}
	again, _ := fx.graphql(t, managerCtx, fmt.Sprintf(`mutation { resolveBaselineContestation(input:{counterpartyInvitationId:"%s",outcome:"DECLINE",reason:"x",clientMutationId:"it033-again"}) { userErrors { code field } clientMutationId } }`, invitation.ID))
	if !strings.Contains(again, `"code":"ALREADY_RESOLVED"`) || !strings.Contains(again, `"clientMutationId":"it033-again"`) {
		t.Fatalf("IT-033 second resolution must report ALREADY_RESOLVED: %s", again)
	}
}

func TestCounterpartyIT034AcknowledgeAndDeadlineSweepMarksNoResponse(t *testing.T) {
	fx := newCounterpartyITFixture(t, auth.TenantAdmin)
	participantB := fx.addVerifiedParticipant(t, "Contraparte B")
	_, invitations := fx.activateWithCounterparties(t, fx.seed.ParticipantID, participantB)
	byParticipant := map[identity.ID]database.CounterpartyInvitation{}
	for _, invitation := range invitations {
		byParticipant[invitation.ParticipantID] = invitation
		if !invitation.DeadlineAt.After(time.Now().UTC().AddDate(0, 0, 4)) {
			t.Fatalf("IT-034 default deadline must be 5 days ahead: %s", invitation.DeadlineAt)
		}
	}
	fx.deliverAll(t)
	invitationA := byParticipant[fx.seed.ParticipantID]
	var tokenA string
	for _, sent := range fx.email.sent {
		if strings.Contains(sent.Destination, "task06@") {
			tokenA = baselineLinkPattern.FindStringSubmatch(sent.Text + " " + sent.HTML)[1]
		}
	}
	if tokenA == "" {
		t.Fatalf("IT-034 no link for counterparty A in %d e-mails", len(fx.email.sent))
	}
	sessionCtx := requestctx.WithExternalCredentials(context.Background(), fx.verifySession(t, tokenA, invitationA.ID))
	ack, _ := fx.graphql(t, sessionCtx, `mutation { acknowledgeBaseline(input:{clientMutationId:"it034-ack"}) { response respondedAt userErrors { code } clientMutationId } }`)
	if !strings.Contains(ack, `"response":"ACKNOWLEDGED"`) || !strings.Contains(ack, `"clientMutationId":"it034-ack"`) || strings.Contains(ack, `"respondedAt":null`) {
		t.Fatalf("IT-034 acknowledgment failed: %s", ack)
	}
	invitationB := byParticipant[participantB]
	past := time.Now().UTC().Add(-time.Minute)
	if err := fx.h.AdminDB.Model(&database.CounterpartyInvitation{}).Where("tenant_id=? AND id IN ?", fx.seed.TenantID, []identity.ID{invitationA.ID, invitationB.ID}).Update("deadline_at", past).Error; err != nil {
		t.Fatal(err)
	}
	var discovered []identity.ID
	now := time.Now().UTC()
	if err := fx.h.DB.Raw("SELECT pending.tenant_id FROM platform.discover_scheduler_tenants(?, ?, ?) AS pending(tenant_id)", now, now.AddDate(-5, 0, 0), now.Add(24*time.Hour)).Scan(&discovered).Error; err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tenantID := range discovered {
		found = found || tenantID == fx.seed.TenantID
	}
	if !found {
		t.Fatal("IT-034 scheduler discovery must include a tenant with overdue pending invitations")
	}
	tenantCtx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID})
	raw, err := fx.bus.Send(tenantCtx, counterpartysweep.Command{TenantID: fx.seed.TenantID, Now: now, Limit: 100})
	if err != nil || raw.(int) != 1 {
		t.Fatalf("IT-034 sweep updated=%v err=%v, want exactly B", raw, err)
	}
	for id, want := range map[identity.ID]string{invitationA.ID: "ACKNOWLEDGED", invitationB.ID: "NO_RESPONSE"} {
		var row database.CounterpartyInvitation
		if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, id).Take(&row).Error; err != nil || row.Response != want {
			t.Fatalf("IT-034 invitation %s response=%s err=%v, want %s", id, row.Response, err, want)
		}
	}
	late, _ := fx.graphql(t, sessionCtx, `query { counterpartyBaseline { response readOnly } }`)
	if !strings.Contains(late, `"response":"ACKNOWLEDGED"`) || !strings.Contains(late, `"readOnly":true`) {
		t.Fatalf("IT-034 acknowledged view after deadline must stay read-only and acknowledged: %s", late)
	}
}

func TestCounterpartyIT035SessionKindsAreIsolatedAtTransport(t *testing.T) {
	fx := newCounterpartyITFixture(t, auth.TenantAdmin)
	_, invitations := fx.activateWithCounterparties(t, fx.seed.ParticipantID)
	fx.deliverAll(t)
	counterpartyCredentials := fx.verifySession(t, fx.email.lastToken(t), invitations[0].ID)
	counterpartyCtx := requestctx.WithExternalCredentials(context.Background(), counterpartyCredentials)
	ok, _ := fx.graphql(t, counterpartyCtx, `query { counterpartyBaseline { response } }`)
	if !strings.Contains(ok, `"response":"PENDING"`) {
		t.Fatalf("IT-035 control: counterparty session must read its baseline: %s", ok)
	}
	for _, query := range []string{
		`query { externalCapture { kind mediaKind } }`,
		`mutation { declareCaptureEnvironment(input:{typeCode:"KITCHEN",name:"Cozinha",clientMutationId:"it035-declare"}) { clientMutationId } }`,
		`mutation { submitCapture(input:{confirmIncomplete:true,clientMutationId:"it035-submit"}) { clientMutationId } }`,
	} {
		response, _ := fx.graphql(t, counterpartyCtx, query)
		if !strings.Contains(response, "UNAUTHENTICATED") || !strings.Contains(response, `"data":null`) {
			t.Fatalf("IT-035 COUNTERPARTY session reached a capture operation (%s): %s", query, response)
		}
	}

	captureToken, err := security.NewScopedToken(fx.seed.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := security.NewOpaqueToken()
	if err != nil {
		t.Fatal(err)
	}
	digest := security.HashToken(captureToken)
	proof, err := security.CSRFProof(digest, csrf, []byte(strings.Repeat("p", 32)))
	if err != nil {
		t.Fatal(err)
	}
	invitationID := identity.NewID()
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Create(&database.Invitation{ID: invitationID, TenantID: fx.seed.TenantID, ResponsibilityID: fx.seed.ResponsibilityID, TokenHash: randomTokenHash(t), DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ExternalSession{ID: identity.NewID(), TenantID: fx.seed.TenantID, Kind: "CAPTURE", InvitationID: &invitationID, ResponsibilityID: &fx.seed.ResponsibilityID, SessionDigest: digest[:], CSRFDigest: proof[:], ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	captureCredentials := requestctx.ExternalCredentials{SessionToken: captureToken, CSRFToken: csrf}
	if _, err := (invitationcore.Service{DB: fx.h.DB, Pepper: []byte(strings.Repeat("p", 32)), Within: fx.h.WithinTenant}).ValidateSession(context.Background(), captureToken, csrf); err != nil {
		t.Fatalf("IT-035 control: the CAPTURE session must be valid for capture: %v", err)
	}
	captureCtx := requestctx.WithExternalCredentials(context.Background(), captureCredentials)
	for _, query := range []string{
		`query { counterpartyBaseline { response } }`,
		`mutation { acknowledgeBaseline(input:{clientMutationId:"it035-ack"}) { response } }`,
		`mutation { contestBaseline(input:{statement:"x",clientMutationId:"it035-contest"}) { response } }`,
	} {
		response, _ := fx.graphql(t, captureCtx, query)
		if !strings.Contains(response, "UNAUTHENTICATED") || !strings.Contains(response, `"data":null`) {
			t.Fatalf("IT-035 CAPTURE session reached a counterparty operation (%s): %s", query, response)
		}
	}
	var row database.CounterpartyInvitation
	if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, invitations[0].ID).Take(&row).Error; err != nil || row.Response != "PENDING" {
		t.Fatalf("IT-035 rejected requests changed the invitation: %+v err=%v", row.Response, err)
	}
}

func TestCounterpartyIT037PolicyMutationsEnforceRoles(t *testing.T) {
	fx := newCounterpartyITFixture(t, auth.TenantAdmin)
	for _, tc := range []struct {
		role    string
		allowed bool
	}{
		{auth.TenantAdmin, true},
		{auth.InspectionConfigAdmin, true},
		{auth.Manager, false},
		{auth.Employee, false},
		{auth.Viewer, false},
	} {
		t.Run("IT-037 updateBaselinePolicy "+tc.role, func(t *testing.T) {
			response, _ := fx.graphql(t, fx.roleContext(tc.role), `mutation { updateBaselinePolicy(input:{contestationDeadlineDays:10,clientMutationId:"it037-policy"}) { policy { contestationDeadlineDays version } userErrors { code field } clientMutationId } }`)
			if tc.allowed && (!strings.Contains(response, `"contestationDeadlineDays":10`) || !strings.Contains(response, `"userErrors":[]`) || !strings.Contains(response, `"clientMutationId":"it037-policy"`)) {
				t.Fatalf("%s should update the baseline policy: %s", tc.role, response)
			}
			if !tc.allowed && !strings.Contains(response, "FORBIDDEN") {
				t.Fatalf("%s must be forbidden from updating the baseline policy: %s", tc.role, response)
			}
		})
	}
	var policy database.BaselinePolicy
	if err := fx.h.AdminDB.Where("tenant_id=?", fx.seed.TenantID).Take(&policy).Error; err != nil || policy.ContestationDeadlineDays != 10 || policy.Version != 2 {
		t.Fatalf("IT-037 persisted policy=%+v err=%v, want 10 days at version 2", policy, err)
	}
	for _, days := range []string{"0", "91"} {
		response, _ := fx.graphql(t, fx.roleContext(auth.TenantAdmin), `mutation { updateBaselinePolicy(input:{contestationDeadlineDays:`+days+`,clientMutationId:"it037-invalid"}) { policy { version } userErrors { code field } clientMutationId } }`)
		if !strings.Contains(response, `"code":"INVALID_INPUT"`) || !strings.Contains(response, `"field":"contestationDeadlineDays"`) || !strings.Contains(response, `"clientMutationId":"it037-invalid"`) || !strings.Contains(response, `"policy":null`) || strings.Contains(response, `"errors"`) {
			t.Fatalf("UT-302 %s days must be a contestationDeadlineDays user error: %s", days, response)
		}
	}
	fractional, _ := fx.graphql(t, fx.roleContext(auth.TenantAdmin), `mutation { updateBaselinePolicy(input:{contestationDeadlineDays:2.5,clientMutationId:"it037-fraction"}) { policy { version } userErrors { code } } }`)
	if !strings.Contains(fractional, `"errors"`) || strings.Contains(fractional, `"policy":{`) {
		t.Fatalf("UT-302 2.5 days must be rejected before reaching the policy: %s", fractional)
	}
	if err := fx.h.AdminDB.Where("tenant_id=?", fx.seed.TenantID).Take(&policy).Error; err != nil || policy.ContestationDeadlineDays != 10 || policy.Version != 2 {
		t.Fatalf("UT-302 rejected values changed the policy: %+v err=%v", policy, err)
	}

	read, _ := fx.graphql(t, fx.roleContext(auth.TenantAdmin), `query { videoPolicy { version } }`)
	version := 1
	var body struct {
		Data struct {
			Policy struct{ Version int } `json:"videoPolicy"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(read), &body); err == nil && body.Data.Policy.Version > 0 {
		version = body.Data.Policy.Version
	}
	for _, tc := range []struct {
		role    string
		allowed bool
	}{
		{auth.Manager, false},
		{auth.Employee, false},
		{auth.TenantAdmin, true},
	} {
		t.Run("IT-037 configureVideoPolicy "+tc.role, func(t *testing.T) {
			response, _ := fx.graphql(t, fx.roleContext(tc.role), fmt.Sprintf(`mutation { configureVideoPolicy(input:{maxDurationMinutes:15,originRetentionDays:30,currentRetentionDays:30,supersededBaselineRetentionDays:400,expectedVersion:%d,clientMutationId:"it037-video-%s"}) { policy { supersededBaselineRetentionDays } userErrors { code field } } }`, version, tc.role))
			if tc.allowed && !strings.Contains(response, `"supersededBaselineRetentionDays":400`) {
				t.Fatalf("%s should configure the superseded retention: %s", tc.role, response)
			}
			if !tc.allowed && !strings.Contains(response, "FORBIDDEN") {
				t.Fatalf("%s must be forbidden from configuring video policy: %s", tc.role, response)
			}
		})
	}
	invalid, _ := fx.graphql(t, fx.roleContext(auth.TenantAdmin), fmt.Sprintf(`mutation { configureVideoPolicy(input:{maxDurationMinutes:15,originRetentionDays:30,currentRetentionDays:30,supersededBaselineRetentionDays:3651,expectedVersion:%d,clientMutationId:"it037-video-invalid"}) { userErrors { code field } } }`, version+1))
	if !strings.Contains(invalid, `"field":"supersededBaselineRetentionDays"`) {
		t.Fatalf("UT-315 3651 days must be rejected on supersededBaselineRetentionDays: %s", invalid)
	}
}

func TestCounterpartyIT050DeliveryFailureIsListedAndResendRecovers(t *testing.T) {
	fx := newCounterpartyITFixture(t, auth.Manager)
	versionID, invitations := fx.activateWithCounterparties(t, fx.seed.ParticipantID)
	// Listing reuses origins/list_versions, which requires a business-unit
	// grant; resends below run as an asset-scoped MANAGER.
	admin := fx.roleContext(auth.TenantAdmin)
	invitation := invitations[0]
	listQuery := fmt.Sprintf(`query { baselineVersions(assetId:"%s",first:25) { nodes { id counterpartyInvitations { id response deliveryStatus deliveryId } } } }`, fx.seed.AssetID)
	pending, _ := fx.graphql(t, admin, listQuery)
	if !strings.Contains(pending, `"deliveryStatus":"QUEUED"`) {
		t.Fatalf("IT-050 queued delivery should be listed as QUEUED: %s", pending)
	}
	early, _ := fx.graphql(t, fx.ctx, fmt.Sprintf(`mutation { resendCounterpartyInvitation(input:{counterpartyInvitationId:"%s",clientMutationId:"it050-early"}) { userErrors { code field } clientMutationId } }`, invitation.ID))
	if !strings.Contains(early, `"code":"INVALID_STATE"`) {
		t.Fatalf("IT-050 resend before a failure must be INVALID_STATE: %s", early)
	}

	fx.email.failing = true
	fx.deliverAll(t)
	var failed database.Delivery
	if err := fx.h.AdminDB.Where("tenant_id=? AND correlation_id=?", fx.seed.TenantID, fmt.Sprintf("counterparty-%s-g1", invitation.ID)).Take(&failed).Error; err != nil || failed.Status != "FAILED" {
		t.Fatalf("IT-050 provider failure must leave the first delivery FAILED: %+v err=%v", failed.Status, err)
	}
	var terminal int64
	if err := fx.h.AdminDB.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='notification.delivery_terminal.v1' AND payload::text LIKE ?", fx.seed.TenantID, "%"+failed.ID.String()+"%").Count(&terminal).Error; err != nil || terminal != 1 {
		t.Fatalf("IT-050 terminal events=%d err=%v, want 1", terminal, err)
	}
	listed, _ := fx.graphql(t, admin, listQuery)
	if !strings.Contains(listed, `"deliveryStatus":"FAILED"`) || !strings.Contains(listed, `"deliveryId":"`+failed.ID.String()+`"`) || !strings.Contains(listed, versionID.String()) {
		t.Fatalf("IT-050 counterparty list must show the failed delivery: %s", listed)
	}

	fx.email.failing = false
	resent, _ := fx.graphql(t, fx.ctx, fmt.Sprintf(`mutation { resendCounterpartyInvitation(input:{counterpartyInvitationId:"%s",clientMutationId:"it050-resend"}) { invitationId response deadlineAt userErrors { code field } clientMutationId } }`, invitation.ID))
	if !strings.Contains(resent, `"invitationId":"`+invitation.ID.String()+`"`) || !strings.Contains(resent, `"userErrors":[]`) || !strings.Contains(resent, `"clientMutationId":"it050-resend"`) {
		t.Fatalf("IT-050 resend failed: %s", resent)
	}
	var after database.CounterpartyInvitation
	if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, invitation.ID).Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	if !after.DeadlineAt.Equal(invitation.DeadlineAt) || after.DeliveryGeneration != 2 || string(after.TokenHash) == string(invitation.TokenHash) {
		t.Fatalf("UT-304 resend must keep the deadline, bump the generation and rotate the token: %+v", after)
	}
	fx.deliverAll(t)
	recovered, _ := fx.graphql(t, admin, listQuery)
	var second database.Delivery
	if err := fx.h.AdminDB.Where("tenant_id=? AND correlation_id=?", fx.seed.TenantID, fmt.Sprintf("counterparty-%s-g2", invitation.ID)).Take(&second).Error; err != nil || second.Status == "FAILED" {
		t.Fatalf("IT-050 resend did not create a successful second delivery: %+v err=%v", second.Status, err)
	}
	// SMTP has no delivery receipts: the provider acceptance is the best state.
	if !strings.Contains(recovered, `"deliveryStatus":"ACCEPTED"`) || !strings.Contains(recovered, `"deliveryId":"`+second.ID.String()+`"`) {
		t.Fatalf("IT-050 list must show the recovered generation: %s", recovered)
	}
	token := fx.email.lastToken(t)
	if digest := security.HashToken(token); string(digest[:]) != string(after.TokenHash) {
		t.Fatal("IT-050 resent e-mail must carry the rotated token")
	}
}

func TestCounterpartyUT322AndUT323MutationsEchoAndRejectWrongCallers(t *testing.T) {
	fx := newCounterpartyITFixture(t, auth.TenantAdmin)
	_, invitations := fx.activateWithCounterparties(t, fx.seed.ParticipantID)
	unknown := identity.NewID()
	for _, tc := range []struct{ name, query, want string }{
		{"UT-322 resolve echo on user error", fmt.Sprintf(`mutation { resolveBaselineContestation(input:{counterpartyInvitationId:"%s",outcome:"DECLINE",reason:"r",clientMutationId:"ut322-resolve"}) { userErrors { code field } clientMutationId } }`, invitations[0].ID), `"clientMutationId":"ut322-resolve"`},
		{"UT-322 resend echo on user error", fmt.Sprintf(`mutation { resendCounterpartyInvitation(input:{counterpartyInvitationId:"%s",clientMutationId:"ut322-resend"}) { userErrors { code field } clientMutationId } }`, invitations[0].ID), `"clientMutationId":"ut322-resend"`},
		{"UT-322 policy echo", `mutation { updateBaselinePolicy(input:{contestationDeadlineDays:7,clientMutationId:"ut322-policy"}) { userErrors { code } clientMutationId } }`, `"clientMutationId":"ut322-policy"`},
		{"UT-323 resolve unknown invitation", fmt.Sprintf(`mutation { resolveBaselineContestation(input:{counterpartyInvitationId:"%s",outcome:"DECLINE",reason:"r",clientMutationId:"ut323-unknown"}) { userErrors { code field } } }`, unknown), `"field":"counterpartyInvitationId"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, _ := fx.graphql(t, fx.ctx, tc.query)
			if !strings.Contains(response, tc.want) || strings.Contains(response, `"errors"`) {
				t.Fatalf("%s: %s", tc.name, response)
			}
		})
	}
	viewer, _ := fx.graphql(t, fx.roleContext(auth.Viewer), fmt.Sprintf(`mutation { resolveBaselineContestation(input:{counterpartyInvitationId:"%s",outcome:"DECLINE",reason:"r",clientMutationId:"ut323-viewer"}) { userErrors { code } } }`, unknown))
	if !strings.Contains(viewer, "FORBIDDEN") || strings.Contains(viewer, "NOT_FOUND") {
		t.Fatalf("UT-323 a role without access must be forbidden before existence is revealed: %s", viewer)
	}
	external, _ := fx.graphql(t, context.Background(), `mutation { updateBaselinePolicy(input:{contestationDeadlineDays:7,clientMutationId:"ut323-anon"}) { clientMutationId } }`)
	if !strings.Contains(external, "UNAUTHENTICATED") {
		t.Fatalf("UT-323 policy update without a principal must be unauthenticated: %s", external)
	}
	customer, _ := fx.graphql(t, fx.roleContext(auth.CustomerViewer), fmt.Sprintf(`query { baselineVersions(assetId:"%s",first:5) { nodes { counterpartyInvitations { id } } } }`, fx.seed.AssetID))
	if !strings.Contains(customer, "FORBIDDEN") && !strings.Contains(customer, `"errors"`) {
		t.Fatalf("UT-323 customer viewers must not list counterparty invitations: %s", customer)
	}
}

func randomTokenHash(t *testing.T) []byte {
	t.Helper()
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return value
}
