package complete

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	onboardingsession "inspection/services/inspection/internal/features/onboarding/session"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type activationInvitationRecorder struct {
	count       int
	destination string
	url         string
	err         error
}

func (r *activationInvitationRecorder) SendActivationInvitation(_ context.Context, destination, url string) error {
	r.count++
	r.destination, r.url = destination, url
	return r.err
}

func TestInspectionInternalFailuresRemainGeneric(t *testing.T) {
	err := mapInspectionError(apperror.Wrap(apperror.Internal, errors.New("provider secret")))
	if code, field, message := apperror.Public(err); code != apperror.Internal || field != "" || message != "Não foi possível concluir a operação." {
		t.Fatalf("internal inspection failure public contract = code:%s field:%q message:%q", code, field, message)
	}
}

func TestAssetErrorsAreMappedToBrazilianPortuguese(t *testing.T) {
	err := mapAssetError(apperror.New(apperror.InvalidInput, "address", "address exceeds limit"))
	if code, field, message := apperror.Public(err); code != apperror.InvalidInput || field != "address" || message != "Confira os dados do imóvel antes de continuar." {
		t.Fatalf("asset validation was not mapped to a safe Portuguese message: code=%s field=%q message=%q", code, field, message)
	}
}

func TestCatalogErrorsAreMappedWithoutLeakingTechnicalDetails(t *testing.T) {
	err := mapCatalogError(apperror.New(apperror.InvalidInput, "templateId", "database constraint detail"))
	if code, field, message := apperror.Public(err); code != apperror.InvalidInput || field != "templateId" || message != "Selecione um modelo válido para o imóvel." {
		t.Fatalf("catalog validation was not safely mapped: code=%s field=%q message=%q", code, field, message)
	}
}

func TestParticipantErrorsAreMappedWithoutLeakingTechnicalDetails(t *testing.T) {
	err := mapParticipantError(apperror.New(apperror.InvalidInput, "email", "provider response detail"))
	if code, field, message := apperror.Public(err); code != apperror.InvalidInput || field != "email" || message != "Confira os dados da pessoa responsável e tente novamente." {
		t.Fatalf("participant validation was not safely mapped: code=%s field=%q message=%q", code, field, message)
	}
}

func TestOnboardingErrorMappersPreserveAuthorizationCodes(t *testing.T) {
	for _, code := range []apperror.Code{apperror.Forbidden, apperror.SessionExpired} {
		t.Run(string(code), func(t *testing.T) {
			original := apperror.New(code, "session", "Acesso negado. Entre novamente.")
			for _, mapped := range []error{mapInspectionError(original), mapAssetError(original)} {
				gotCode, field, message := apperror.Public(mapped)
				if gotCode != code || field != "session" || message != "Acesso negado. Entre novamente." {
					t.Fatalf("authorization failure was replaced: code=%s field=%q message=%q", gotCode, field, message)
				}
			}
		})
	}
}

func TestOnboardingErrorMappersPreservePublicOperationalCodesSafely(t *testing.T) {
	cases := []struct {
		code    apperror.Code
		message string
	}{
		{apperror.DependencyUnavailable, "Um serviço necessário está temporariamente indisponível. Tente novamente."},
		{apperror.RateLimited, "Muitas tentativas foram realizadas. Aguarde um momento e tente novamente."},
		{apperror.Conflict, "Os dados foram alterados. Atualize a página e tente novamente."},
		{apperror.Unauthenticated, "Sua sessão expirou. Entre novamente para continuar."},
	}
	for _, tc := range cases {
		t.Run(string(tc.code), func(t *testing.T) {
			original := apperror.New(tc.code, "assetId", "technical provider detail")
			for _, mapped := range []error{mapInspectionError(original), mapAssetError(original)} {
				code, field, message := apperror.Public(mapped)
				if code != tc.code || field != "assetId" || message != tc.message {
					t.Fatalf("operational error mapping = code:%s field:%q message:%q", code, field, message)
				}
			}
		})
	}
}

func TestCompleteRejectsMissingDependencies(t *testing.T) {
	_, err := (Service{}).Complete(context.Background(), "locator", "csrf", "mutation")
	if err == nil || err.Error() != "onboarding complete: missing dependency" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeadlineAtRejectsMalformedDate(t *testing.T) {
	_, err := deadlineAt(map[string]any{"deadline": "tomorrow"})
	if err == nil {
		t.Fatal("malformed deadline accepted")
	}
	if code, field, _ := apperror.Public(err); code != apperror.InvalidInput || field != "deadline" {
		t.Fatalf("unexpected public error: code=%s field=%q", code, field)
	}
}

func TestCompletedResultHasNoOriginVersionOrStatus(t *testing.T) {
	result := completedResult(onboardingsession.Session{}, database.OnboardingRequest{}, identity.NewID())
	if result.OriginStatus != "NOT_REQUIRED" || result.Request.OriginVersionID != nil {
		t.Fatalf("onboarding unexpectedly has an origin: %+v", result)
	}
}

func TestEnsureActivationInvitationIsRetrySafe(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS onboarding").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.OnboardingActivation{}); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
		t.Fatal(err)
	}
	tenantID, identityID, sessionID := identity.NewID(), identity.NewID(), identity.NewID()
	recorder := &activationInvitationRecorder{}
	service := Service{
		DB: db, ActivationNotifier: recorder, AdminOrigin: "https://admin.example.test/",
		Now: func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) },
		Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
			return db.WithContext(ctx).Transaction(fn)
		},
	}
	current := onboardingsession.Session{ID: sessionID, TenantID: &tenantID, Owner: onboardingsession.Owner{Email: "owner@example.test"}}
	member := database.Membership{IdentityID: identityID}

	if err := service.ensureActivationInvitation(context.Background(), current, member); err != nil {
		t.Fatal(err)
	}
	if err := service.ensureActivationInvitation(context.Background(), current, member); err != nil {
		t.Fatal(err)
	}
	activationURL, err := url.Parse(recorder.url)
	if err != nil {
		t.Fatal(err)
	}
	token := activationURL.Query().Get("token")
	if recorder.count != 1 || recorder.destination != current.Owner.Email || activationURL.Scheme != "https" || activationURL.Host != "admin.example.test" || activationURL.Path != "/activate" || token == "" {
		t.Fatalf("unexpected invitation: %#v", recorder)
	}
	var activation database.OnboardingActivation
	if err := db.First(&activation, "tenant_id = ?", tenantID).Error; err != nil {
		t.Fatal(err)
	}
	tokenDigest := security.HashToken(token)
	if activation.Status != activationInvitationSent || activation.IdentityID != identityID || activation.SessionID != sessionID || !bytes.Equal(activation.InvitationTokenDigest, tokenDigest[:]) || activation.InvitationExpiresAt == nil {
		t.Fatalf("unexpected activation marker: %#v", activation)
	}
}

func TestEnsureActivationInvitationRebindsReplacedSession(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS onboarding").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.OnboardingActivation{}); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
		t.Fatal(err)
	}
	tenantID, identityID := identity.NewID(), identity.NewID()
	oldSessionID, currentSessionID := identity.NewID(), identity.NewID()
	recorder := &activationInvitationRecorder{}
	service := Service{
		DB: db, ActivationNotifier: recorder, AdminOrigin: "https://admin.example.test/",
		Now: func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) },
		Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
			return db.WithContext(ctx).Transaction(fn)
		},
	}
	member := database.Membership{IdentityID: identityID}
	old := onboardingsession.Session{ID: oldSessionID, TenantID: &tenantID, Owner: onboardingsession.Owner{Email: "owner@example.test"}}
	current := old
	current.ID = currentSessionID

	if err := service.ensureActivationInvitation(context.Background(), old, member); err != nil {
		t.Fatal(err)
	}
	firstURL, err := url.Parse(recorder.url)
	if err != nil {
		t.Fatal(err)
	}
	firstToken := firstURL.Query().Get("token")

	if err := service.ensureActivationInvitation(context.Background(), current, member); err != nil {
		t.Fatal(err)
	}
	secondURL, err := url.Parse(recorder.url)
	if err != nil {
		t.Fatal(err)
	}
	secondToken := secondURL.Query().Get("token")
	var activation database.OnboardingActivation
	if err := db.First(&activation, "tenant_id = ?", tenantID).Error; err != nil {
		t.Fatal(err)
	}
	secondDigest := security.HashToken(secondToken)
	if recorder.count != 2 || firstToken == "" || secondToken == "" || firstToken == secondToken || activation.SessionID != currentSessionID || activation.Status != activationInvitationSent || !bytes.Equal(activation.InvitationTokenDigest, secondDigest[:]) {
		t.Fatalf("activation invitation was not rebound: count=%d first=%q second=%q activation=%#v", recorder.count, firstToken, secondToken, activation)
	}
}
