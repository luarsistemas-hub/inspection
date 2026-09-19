package complete

import (
	"bytes"
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/onboarding/coordinator"
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

func TestPendingResultProjectsOriginStateWithoutCompletedResources(t *testing.T) {
	result := pendingResult(onboardingsession.Session{State: coordinator.StateParticipantSaved}, coordinator.SubmitResult{
		State:          coordinator.StateOriginPending,
		NextAction:     coordinator.NextActionWaitForOrigin,
		OriginStatus:   "PENDING",
		DeliveryStatus: deliveryPending,
	})

	if result.State != coordinator.StateOriginPending || result.NextAction != coordinator.NextActionWaitForOrigin {
		t.Fatalf("unexpected pending transition: %#v", result)
	}
	if result.OriginStatus != "PENDING" || result.Delivery != deliveryPending {
		t.Fatalf("unexpected pending statuses: %#v", result)
	}
	if result.Request.ID.String() != "00000000-0000-0000-0000-000000000000" || result.InspectionID.String() != "00000000-0000-0000-0000-000000000000" {
		t.Fatalf("pending result claimed completed resources: %#v", result)
	}
}

func TestReferencePhotosWaitForScreeningThenActivateOnce(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"media", "origins", "capture"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.MediaObject{}, &database.Origin{}, &database.OriginVersion{}, &database.OriginEvidence{}, &database.CaptureDraft{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenantID, sessionID, assetID, templateID, templateVersionID, mediaID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	photo := database.MediaObject{ID: mediaID, TenantID: tenantID, ResponsibilityID: sessionID, Status: "VERIFIED", RequirementKey: "reference", Description: "Quarto", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 12}
	if err := db.Create(&photo).Error; err != nil {
		t.Fatal(err)
	}
	service := Service{DB: db, Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}}
	status, err := service.originMediaStatus(context.Background(), tenantID, sessionID, []identity.ID{mediaID})
	if err != nil || status != "PENDING" {
		t.Fatalf("unprocessed photo status = %q, %v", status, err)
	}
	if err := db.Model(&photo).Update("status", "READY").Error; err != nil {
		t.Fatal(err)
	}
	status, err = service.originMediaStatus(context.Background(), tenantID, sessionID, []identity.ID{mediaID})
	if err != nil || status != "ACTIVE" {
		t.Fatalf("screened photo status = %q, %v", status, err)
	}
	versionID, err := service.ensureOrigin(context.Background(), tenantID, sessionID, assetID, templateID, templateVersionID, []identity.ID{mediaID})
	if err != nil {
		t.Fatal(err)
	}
	again, err := service.ensureOrigin(context.Background(), tenantID, sessionID, assetID, templateID, templateVersionID, []identity.ID{mediaID})
	if err != nil || again != versionID {
		t.Fatalf("origin retry = %s, %v", again, err)
	}
	var origin database.Origin
	var evidence []database.OriginEvidence
	if err := db.Where("tenant_id=? AND asset_id=?", tenantID, assetID).First(&origin).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("tenant_id=? AND origin_version_id=?", tenantID, versionID).Find(&evidence).Error; err != nil {
		t.Fatal(err)
	}
	if origin.ActiveVersionID == nil || *origin.ActiveVersionID != versionID || len(evidence) != 1 || evidence[0].MediaID != mediaID || evidence[0].Description != "Quarto" {
		t.Fatalf("origin was not activated with the reference photo: %+v %+v", origin, evidence)
	}
	if completed := completedResult(onboardingsession.Session{}, database.OnboardingRequest{OriginVersionID: &versionID}, identity.NewID()); completed.OriginStatus != "ACTIVE" {
		t.Fatalf("completed onboarding hid the active origin: %+v", completed)
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
