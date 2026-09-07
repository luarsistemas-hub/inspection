package core

import (
	"context"
	"testing"
	"time"

	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
)

func securityFixture(t *testing.T) (*gorm.DB, Service, string, *notifierStub, *time.Time) {
	t.Helper()
	db, token := invitationDB(t)
	now := time.Unix(1000, 0).UTC()
	notifier := &notifierStub{}
	service := Service{DB: db, Within: testWithin(db), Pepper: []byte("01234567890123456789012345678901"), Limits: &limitsStub{allowed: true}, Notifier: notifier, Clock: func() time.Time { return now }}
	return db, service, token, notifier, &now
}

func TestIT143IncorrectOTPAttemptsPersistAndExhaustChallenge(t *testing.T) {
	db, service, token, notifier, _ := securityFixture(t)
	ctx := context.Background()
	if err := service.RequestOTP(ctx, token); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 5; attempt++ {
		if _, err := service.VerifyOTP(ctx, token, "invalid"); appCode(err) != apperror.InvalidInput {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		var challenge database.OTPChallenge
		if err := db.First(&challenge).Error; err != nil || challenge.Attempts != attempt {
			t.Fatalf("attempt was rolled back: %+v %v", challenge, err)
		}
	}
	if _, err := service.VerifyOTP(ctx, token, notifier.code); appCode(err) != apperror.InvalidInput {
		t.Fatalf("exhausted challenge accepted correct code: %v", err)
	}
	var sessions int64
	if err := db.Model(&database.ExternalSession{}).Count(&sessions).Error; err != nil || sessions != 0 {
		t.Fatalf("exhaustion created sessions: %d %v", sessions, err)
	}
}

func TestIT147ResendInvalidatesPreviousOTPWithoutFallback(t *testing.T) {
	db, service, token, notifier, now := securityFixture(t)
	ctx := context.Background()
	if err := service.RequestOTP(ctx, token); err != nil {
		t.Fatal(err)
	}
	oldCode := notifier.code
	var old database.OTPChallenge
	if err := db.First(&old).Error; err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	if err := service.RequestOTP(ctx, token); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&old, "id=?", old.ID).Error; err != nil || old.ExpiresAt.After(*now) {
		t.Fatalf("superseded challenge still active: %+v %v", old, err)
	}
	if _, err := service.VerifyOTP(ctx, token, notifier.code); err != nil {
		t.Fatal(err)
	}
	if _, err := service.VerifyOTP(ctx, token, oldCode); appCode(err) != apperror.InvalidInput {
		t.Fatalf("verification fell back to old challenge: %v", err)
	}
}

func TestIT146IT149SessionFollowsInvitationValidity(t *testing.T) {
	for _, status := range []string{"REVOKED", "COMPLETED", "EXPIRED"} {
		t.Run(status, func(t *testing.T) {
			db, service, token, notifier, now := securityFixture(t)
			ctx := context.Background()
			if err := service.RequestOTP(ctx, token); err != nil {
				t.Fatal(err)
			}
			session, err := service.VerifyOTP(ctx, token, notifier.code)
			if err != nil {
				t.Fatal(err)
			}
			if status == "EXPIRED" {
				*now = now.Add(time.Hour)
			} else {
				hash := security.HashToken(token)
				if err := db.Model(&database.Invitation{}).Where("token_hash=?", hash[:]).Update("status", status).Error; err != nil {
					t.Fatal(err)
				}
			}
			if _, err := service.ValidateSession(ctx, session.Token, session.CSRF); appCode(err) != apperror.SessionExpired {
				t.Fatalf("terminal invitation retained session: %v", err)
			}
			consent := ProcessingConsent{DisclosureVersion: PrivacyDisclosureVersion, PhotoProcessing: true, AIAnalysis: true, GPSUse: true}
			if err := service.AcceptProcessing(ctx, session.Token, session.CSRF, consent); appCode(err) != apperror.SessionExpired {
				t.Fatalf("terminal session accepted processing: %v", err)
			}
		})
	}
}

func TestIT142ProcessingAcceptanceRetryIsIdempotent(t *testing.T) {
	db, service, token, notifier, _ := securityFixture(t)
	ctx := context.Background()
	if err := service.RequestOTP(ctx, token); err != nil {
		t.Fatal(err)
	}
	session, err := service.VerifyOTP(ctx, token, notifier.code)
	if err != nil {
		t.Fatal(err)
	}
	consent := ProcessingConsent{DisclosureVersion: PrivacyDisclosureVersion, PhotoProcessing: true, AIAnalysis: true, GPSUse: true}
	for range 2 {
		if err := service.AcceptProcessing(ctx, session.Token, session.CSRF, consent); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := db.Model(&database.ProcessingAcceptance{}).Where("responsibility_id=?", session.ResponsibilityID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("duplicate acceptance: %d %v", count, err)
	}
}

func TestCompletedCaptureReadDoesNotRestoreMutationAuthority(t *testing.T) {
	db, service, token, notifier, now := securityFixture(t)
	ctx := context.Background()
	if err := service.RequestOTP(ctx, token); err != nil {
		t.Fatal(err)
	}
	session, err := service.VerifyOTP(ctx, token, notifier.code)
	if err != nil {
		t.Fatal(err)
	}
	hash := security.HashToken(token)
	if err := db.Model(&database.Invitation{}).Where("token_hash=?", hash[:]).Updates(map[string]any{"status": "COMPLETED", "revoked_at": *now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.ExternalSession{}).Where("responsibility_id=?", session.ResponsibilityID).Update("revoked_at", *now).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.ValidateSession(ctx, session.Token, session.CSRF); appCode(err) != apperror.SessionExpired {
		t.Fatalf("completion restored mutation: %v", err)
	}
	if id, err := service.ValidateCaptureRead(ctx, session.Token, session.CSRF); err != nil || id != session.ResponsibilityID {
		t.Fatalf("confirmation unavailable: %s %v", id, err)
	}
	if _, err := service.ValidateCaptureRead(ctx, session.Token, "wrong"); appCode(err) != apperror.Forbidden {
		t.Fatalf("confirmation ignored CSRF: %v", err)
	}
	*now = now.Add(security.SessionTTL)
	if _, err := service.ValidateCaptureRead(ctx, session.Token, session.CSRF); appCode(err) != apperror.SessionExpired {
		t.Fatalf("confirmation ignored session expiry: %v", err)
	}
}
