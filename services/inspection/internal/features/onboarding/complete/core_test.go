package complete

import (
	"bytes"
	"context"
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
}

func (r *activationInvitationRecorder) SendActivationInvitation(_ context.Context, destination, url string) error {
	r.count++
	r.destination, r.url = destination, url
	return nil
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
