package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/ratelimit"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type limitsStub struct {
	allowed bool
	err     error
}

func (l *limitsStub) AllowSend(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: l.allowed}, l.err
}
func (l *limitsStub) AllowResend(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: l.allowed}, l.err
}
func (l *limitsStub) AllowAttempt(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: l.allowed}, l.err
}

type notifierStub struct {
	code string
	err  error
}

func (n *notifierStub) SendOTP(_ context.Context, _, _ identity.ID, code string, _ []DeliveryIntent) error {
	n.code = code
	return n.err
}
func invitationDB(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`ATTACH DATABASE ':memory:' AS invitations`).Error; err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{`CREATE TABLE invitations.invitations (id blob primary key,tenant_id blob,responsibility_id blob,token_hash blob unique,previous_token_hash blob,delivery_intents blob,status text,expires_at datetime,revoked_at datetime,created_at datetime)`, `CREATE TABLE invitations.otp_challenges (id blob primary key,tenant_id blob,invitation_id blob,code_hmac blob,attempts integer,send_count integer,last_sent_at datetime,expires_at datetime,verified_at datetime,created_at datetime)`, `CREATE TABLE invitations.external_sessions (id blob primary key,tenant_id blob,invitation_id blob,responsibility_id blob,session_digest blob unique,csrf_digest blob,expires_at datetime,revoked_at datetime,created_at datetime)`, `CREATE TABLE invitations.processing_acceptances (id blob primary key,tenant_id blob,responsibility_id blob unique,disclosure_version text,photo_processing integer,ai_analysis integer,gps_use integer,accepted_at datetime)`} {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID := identity.NewID()
	token, err := security.NewScopedToken(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	hash := security.HashToken(token)
	intents, _ := json.Marshal([]DeliveryIntent{{Channel: "EMAIL", Destination: "person@example.test"}})
	now := time.Unix(1000, 0).UTC()
	row := database.Invitation{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: identity.NewID(), TokenHash: hash[:], DeliveryIntents: intents, Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	return db, token
}

func TestExternalAccessContractsIT141ToIT150(t *testing.T) {
	newService := func(t *testing.T) (*gorm.DB, string, *notifierStub, *limitsStub, *time.Time, Service) {
		t.Helper()
		db, token := invitationDB(t)
		now := time.Unix(1000, 0).UTC()
		limits := &limitsStub{allowed: true}
		notifier := &notifierStub{}
		service := Service{DB: db, Pepper: []byte("01234567890123456789012345678901"), Limits: limits, Notifier: notifier, Clock: func() time.Time { return now }, Within: testWithin(db)}
		return db, token, notifier, limits, &now, service
	}
	t.Run("IT-141 wrong or malformed OTP is safely rejected", func(t *testing.T) {
		_, token, _, _, _, service := newService(t)
		if err := service.RequestOTP(context.Background(), token); err != nil {
			t.Fatal(err)
		}
		if _, err := service.VerifyOTP(context.Background(), token, "not-a-code"); appCode(err) != apperror.InvalidInput || strings.Contains(err.Error(), "expected") {
			t.Fatalf("unsafe OTP rejection: %v", err)
		}
	})
	t.Run("IT-142 acceptance is required before capture", func(t *testing.T) {
		_, token, notifier, _, _, service := newService(t)
		if err := service.RequestOTP(context.Background(), token); err != nil {
			t.Fatal(err)
		}
		session, err := service.VerifyOTP(context.Background(), token, notifier.code)
		if err != nil {
			t.Fatal(err)
		}
		accepted, err := service.HasProcessingAcceptance(context.Background(), identity.NewID(), session.ResponsibilityID)
		if err != nil || accepted {
			t.Fatalf("missing acceptance was not detected: %v %v", accepted, err)
		}
		if err := service.AcceptProcessing(context.Background(), session.Token, session.CSRF, ProcessingConsent{DisclosureVersion: PrivacyDisclosureVersion}); appCode(err) != apperror.InvalidInput {
			t.Fatalf("partial consent accepted: %v", err)
		}
	})
	t.Run("IT-143 OTP send and attempt limits fail closed", func(t *testing.T) {
		_, token, _, limits, _, service := newService(t)
		limits.allowed = false
		if err := service.RequestOTP(context.Background(), token); appCode(err) != apperror.RateLimited {
			t.Fatalf("send limit bypassed: %v", err)
		}
	})
	t.Run("IT-144 another invitation token cannot widen access", func(t *testing.T) {
		_, token, _, _, _, service := newService(t)
		otherTenantToken, err := security.NewScopedToken(identity.NewID())
		if err != nil {
			t.Fatal(err)
		}
		if err := service.RequestOTP(context.Background(), otherTenantToken); appCode(err) == "" {
			t.Fatal("foreign invitation token accepted")
		}
		if err := service.RequestOTP(context.Background(), token); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("IT-145 simultaneous validation yields one invitation policy", func(t *testing.T) {
		db, token, notifier, _, _, service := newService(t)
		if err := service.RequestOTP(context.Background(), token); err != nil {
			t.Fatal(err)
		}
		first, err := service.VerifyOTP(context.Background(), token, notifier.code)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.VerifyOTP(context.Background(), token, notifier.code); appCode(err) == "" {
			t.Fatal("verified code replay created another session")
		}
		var count int64
		db.Model(&database.ExternalSession{}).Where("responsibility_id=?", first.ResponsibilityID).Count(&count)
		if count != 1 {
			t.Fatalf("session count=%d", count)
		}
	})
	t.Run("IT-146 validated session survives browser re-entry", func(t *testing.T) {
		_, token, notifier, _, _, service := newService(t)
		_ = service.RequestOTP(context.Background(), token)
		session, err := service.VerifyOTP(context.Background(), token, notifier.code)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if responsibility, err := service.ValidateSession(context.Background(), session.Token, session.CSRF); err != nil || responsibility != session.ResponsibilityID {
				t.Fatalf("re-entry failed: %v %v", responsibility, err)
			}
		}
	})
	t.Run("IT-147 used OTP cannot be replayed", func(t *testing.T) {
		_, token, notifier, _, _, service := newService(t)
		_ = service.RequestOTP(context.Background(), token)
		if _, err := service.VerifyOTP(context.Background(), token, notifier.code); err != nil {
			t.Fatal(err)
		}
		if _, err := service.VerifyOTP(context.Background(), token, notifier.code); appCode(err) != apperror.InvalidInput {
			t.Fatalf("used code replay result=%v", err)
		}
	})
	t.Run("IT-148 capture before authentication is blocked", func(t *testing.T) {
		_, _, _, _, _, service := newService(t)
		if _, err := service.ValidateSession(context.Background(), "missing", "missing"); appCode(err) != apperror.SessionExpired {
			t.Fatalf("missing authentication accepted: %v", err)
		}
	})
	t.Run("IT-149 canceled completed or expired invitation is unavailable", func(t *testing.T) {
		for _, status := range []string{"REVOKED", "COMPLETED"} {
			db, token, _, _, _, service := newService(t)
			if err := db.Session(&gorm.Session{AllowGlobalUpdate: true}).Model(&database.Invitation{}).Update("status", status).Error; err != nil {
				t.Fatal(err)
			}
			if err := service.RequestOTP(context.Background(), token); appCode(err) != apperror.InvalidState {
				t.Fatalf("%s invitation opened: %v", status, err)
			}
		}
	})
	t.Run("IT-150 participant rate state remains invitation scoped", func(t *testing.T) {
		_, firstToken, _, firstLimits, _, first := newService(t)
		_, secondToken, _, secondLimits, _, second := newService(t)
		firstLimits.allowed = false
		if err := first.RequestOTP(context.Background(), firstToken); appCode(err) != apperror.RateLimited {
			t.Fatalf("first participant was not limited: %v", err)
		}
		if err := second.RequestOTP(context.Background(), secondToken); err != nil || !secondLimits.allowed {
			t.Fatalf("second participant inherited limit: %v", err)
		}
	})
}

func testWithin(db *gorm.DB) func(context.Context, identity.ID, func(*gorm.DB) error) error {
	return func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
}
func TestIT369IT383IT385InvitationSessionFlow(t *testing.T) {
	db, token := invitationDB(t)
	now := time.Unix(1000, 0).UTC()
	limits := &limitsStub{allowed: true}
	notifier := &notifierStub{}
	service := Service{DB: db, Pepper: []byte("01234567890123456789012345678901"), Limits: limits, Notifier: notifier, Clock: func() time.Time { return now }, Within: testWithin(db)}
	if err := service.RequestOTP(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if len(notifier.code) != 6 {
		t.Fatalf("shared code absent: %q", notifier.code)
	}
	session, err := service.VerifyOTP(context.Background(), token, notifier.code)
	if err != nil {
		t.Fatal(err)
	}
	if session.ExpiresAt != now.Add(2*time.Hour) {
		t.Fatalf("session expiry=%v", session.ExpiresAt)
	}
	if _, err := service.ValidateSession(context.Background(), session.Token, "wrong"); appCode(err) != apperror.Forbidden {
		t.Fatalf("bad csrf: %v", err)
	}
	if responsibility, err := service.ValidateSession(context.Background(), session.Token, session.CSRF); err != nil || responsibility != session.ResponsibilityID {
		t.Fatalf("valid session: %v %v", responsibility, err)
	}
	now = session.ExpiresAt
	if _, err := service.ValidateSession(context.Background(), session.Token, session.CSRF); appCode(err) != apperror.SessionExpired {
		t.Fatalf("exact expiry accepted: %v", err)
	}
}
func TestIT384IT595ImmediateRevocation(t *testing.T) {
	db, token := invitationDB(t)
	now := time.Unix(1000, 0).UTC()
	limits := &limitsStub{allowed: true}
	notifier := &notifierStub{}
	service := Service{DB: db, Pepper: []byte("01234567890123456789012345678901"), Limits: limits, Notifier: notifier, Clock: func() time.Time { return now }, Within: testWithin(db)}
	_ = service.RequestOTP(context.Background(), token)
	session, err := service.VerifyOTP(context.Background(), token, notifier.code)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Revoke(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ValidateSession(context.Background(), session.Token, session.CSRF); appCode(err) != apperror.SessionExpired {
		t.Fatalf("revoked session active: %v", err)
	}
}
func TestIT370IT594IT596LimitsFailClosed(t *testing.T) {
	db, token := invitationDB(t)
	service := Service{DB: db, Pepper: []byte("01234567890123456789012345678901"), Limits: &limitsStub{allowed: false}, Notifier: &notifierStub{}, Clock: func() time.Time { return time.Unix(1000, 0).UTC() }, Within: testWithin(db)}
	if err := service.RequestOTP(context.Background(), token); appCode(err) != apperror.RateLimited {
		t.Fatalf("limit bypass: %v", err)
	}
	service.Limits = &limitsStub{allowed: true, err: errors.New("dragonfly down")}
	if err := service.RequestOTP(context.Background(), token); appCode(err) != apperror.DependencyUnavailable {
		t.Fatalf("dependency exposed: %v", err)
	}
}
func appCode(err error) apperror.Code {
	var app *apperror.Error
	if errors.As(err, &app) {
		return app.Code
	}
	return ""
}
