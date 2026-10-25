package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/ratelimit"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
)

type scriptedOTPLimits struct {
	send, resend, attempt ratelimit.Result
	err                   error
}

func (l scriptedOTPLimits) AllowSend(context.Context, string) (ratelimit.Result, error) {
	return l.send, l.err
}
func (l scriptedOTPLimits) AllowResend(context.Context, string) (ratelimit.Result, error) {
	return l.resend, l.err
}
func (l scriptedOTPLimits) AllowAttempt(context.Context, string) (ratelimit.Result, error) {
	return l.attempt, l.err
}

func assertAppError(t *testing.T, err error, code apperror.Code, field string) {
	t.Helper()
	if gotCode, gotField, _ := apperror.Public(err); gotCode != code || gotField != field {
		t.Fatalf("error=%v code=%s field=%q, want %s/%q", err, gotCode, gotField, code, field)
	}
}

func TestUnit_UT284_VerifyOTPIssuesCounterpartySessionBoundToInvitation(t *testing.T) {
	f := newCounterpartyFixture(t, 1)
	f.invite(t, f.participants[0])
	invitation := f.invitation(t, f.participants[0])
	token := f.tokenFor(t, f.participants[0])
	if err := f.service.RequestOTP(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	issued, err := f.service.VerifyOTP(context.Background(), token, f.otp.code)
	if err != nil {
		t.Fatalf("UT-284 verify correct code: %v", err)
	}
	digest := security.HashToken(issued.Token)
	var session database.ExternalSession
	if err := f.db.Where("tenant_id=? AND session_digest=?", f.tenantID, digest[:]).First(&session).Error; err != nil {
		t.Fatal(err)
	}
	if session.Kind != "COUNTERPARTY" || session.CounterpartyInvitationID == nil || *session.CounterpartyInvitationID != invitation.ID || session.InvitationID != nil || session.ResponsibilityID != nil || !session.ExpiresAt.Equal(f.clock.Add(security.SessionTTL)) {
		t.Fatalf("UT-284 session=%+v, want COUNTERPARTY bound to invitation %s", session, invitation.ID)
	}
	if stored := f.invitation(t, f.participants[0]); stored.OTPHash != nil || stored.OTPExpiresAt != nil || stored.OTPAttempts != 0 {
		t.Fatalf("UT-284 verified OTP was not consumed: %+v", stored)
	}
	owner, err := f.service.ValidateSession(context.Background(), issued.Token, issued.CSRF)
	if err != nil || owner != (SessionOwner{TenantID: f.tenantID, InvitationID: invitation.ID}) {
		t.Fatalf("UT-284 ValidateSession owner=%+v err=%v", owner, err)
	}
	if _, err := f.service.ValidateSession(context.Background(), issued.Token, "wrong-csrf"); !apperror.Is(err, apperror.Forbidden) {
		t.Fatalf("UT-284 wrong CSRF proof error=%v, want FORBIDDEN", err)
	}
	if _, err := f.service.VerifyOTP(context.Background(), token, f.otp.code); !apperror.Is(err, apperror.RateLimited) {
		t.Fatalf("UT-284 consumed code replay error=%v, want RATE_LIMITED", err)
	}
	*f.clock = f.clock.Add(security.SessionTTL)
	if _, err := f.service.ValidateSession(context.Background(), issued.Token, issued.CSRF); !apperror.Is(err, apperror.Unauthenticated) {
		t.Fatalf("UT-284 expired session error=%v, want UNAUTHENTICATED", err)
	}
}

func TestUnit_UT285_FiveWrongCodesLockInvitationOTP(t *testing.T) {
	f := newCounterpartyFixture(t, 1)
	f.invite(t, f.participants[0])
	token := f.tokenFor(t, f.participants[0])
	if err := f.service.RequestOTP(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	wrong := "000000"
	if wrong == f.otp.code {
		wrong = "999999"
	}
	for attempt := 1; attempt <= 5; attempt++ {
		_, err := f.service.VerifyOTP(context.Background(), token, wrong)
		assertAppError(t, err, apperror.InvalidInput, "code")
		if stored := f.invitation(t, f.participants[0]); stored.OTPAttempts != attempt {
			t.Fatalf("UT-285 attempts after %d wrong codes=%d", attempt, stored.OTPAttempts)
		}
	}
	_, err := f.service.VerifyOTP(context.Background(), token, f.otp.code)
	assertAppError(t, err, apperror.RateLimited, "code")
	var sessions int64
	if err := f.db.Model(&database.ExternalSession{}).Where("tenant_id=?", f.tenantID).Count(&sessions).Error; err != nil || sessions != 0 {
		t.Fatalf("UT-285 locked OTP issued %d sessions err=%v", sessions, err)
	}
}

func TestUnit_UT285_AttemptLimiterDecidesBeforeCodeComparison(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits scriptedOTPLimits
		code   apperror.Code
	}{
		{name: "UT-285 limiter denies attempt", limits: scriptedOTPLimits{send: ratelimit.Result{Allowed: true}, resend: ratelimit.Result{Allowed: true}}, code: apperror.RateLimited},
		{name: "UT-285 limiter unavailable", limits: scriptedOTPLimits{err: errors.New("dragonfly down")}, code: apperror.DependencyUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCounterpartyFixture(t, 1)
			f.invite(t, f.participants[0])
			token := f.tokenFor(t, f.participants[0])
			if err := f.service.RequestOTP(context.Background(), token); err != nil {
				t.Fatal(err)
			}
			f.service.Limits = tc.limits
			_, err := f.service.VerifyOTP(context.Background(), token, f.otp.code)
			if !apperror.Is(err, tc.code) {
				t.Fatalf("error=%v, want %s", err, tc.code)
			}
			if strings.Contains(err.Error(), "dragonfly") {
				t.Fatalf("limiter failure leaked: %v", err)
			}
			if stored := f.invitation(t, f.participants[0]); stored.OTPAttempts != 0 || stored.OTPHash == nil {
				t.Fatalf("limited attempt changed OTP state: %+v", stored)
			}
		})
	}
}

func TestUnit_RequestOTPRateLimitAndLimiterFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits scriptedOTPLimits
		code   apperror.Code
	}{
		{name: "send denied", limits: scriptedOTPLimits{}, code: apperror.RateLimited},
		{name: "limiter unavailable", limits: scriptedOTPLimits{err: errors.New("dragonfly down")}, code: apperror.DependencyUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCounterpartyFixture(t, 1)
			f.invite(t, f.participants[0])
			f.service.Limits = tc.limits
			if err := f.service.RequestOTP(context.Background(), f.tokenFor(t, f.participants[0])); !apperror.Is(err, tc.code) {
				t.Fatalf("error=%v, want %s", err, tc.code)
			}
			if f.otp.code != "" {
				t.Fatal("rate-limited request delivered a code")
			}
		})
	}
}

func TestUnit_VerifyOTPRejectsExpiredCode(t *testing.T) {
	f := newCounterpartyFixture(t, 1)
	f.invite(t, f.participants[0])
	token := f.tokenFor(t, f.participants[0])
	if err := f.service.RequestOTP(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	*f.clock = f.clock.Add(security.OTPTTL - time.Millisecond)
	// Still valid one millisecond before expiry: a wrong code is counted, not locked.
	_, err := f.service.VerifyOTP(context.Background(), token, "not-the-code")
	assertAppError(t, err, apperror.InvalidInput, "code")
	*f.clock = f.clock.Add(time.Millisecond)
	_, err = f.service.VerifyOTP(context.Background(), token, f.otp.code)
	assertAppError(t, err, apperror.RateLimited, "code")
}

func TestUnit_RequestOTPNotifierFailureIsDependencyUnavailableWithoutLeak(t *testing.T) {
	f := newCounterpartyFixture(t, 1)
	f.invite(t, f.participants[0])
	f.otp.err = errors.New("smtp auth failed for user=ops password=hunter2")
	err := f.service.RequestOTP(context.Background(), f.tokenFor(t, f.participants[0]))
	if !apperror.Is(err, apperror.DependencyUnavailable) {
		t.Fatalf("notifier failure error=%v, want DEPENDENCY_UNAVAILABLE", err)
	}
	code, field, safe := apperror.Public(err)
	if code != apperror.DependencyUnavailable || field != "" || strings.Contains(err.Error(), "hunter2") || strings.Contains(safe, "hunter2") {
		t.Fatalf("notifier failure leaked details: code=%s field=%q safe=%q error=%q", code, field, safe, err.Error())
	}
	f.service.Notifier = nil
	if err := f.service.RequestOTP(context.Background(), f.tokenFor(t, f.participants[0])); !apperror.Is(err, apperror.DependencyUnavailable) {
		t.Fatalf("missing notifier error=%v, want DEPENDENCY_UNAVAILABLE", err)
	}
}

func TestUnit_UT286_SessionKindsAreNotInterchangeable(t *testing.T) {
	f := newCounterpartyFixture(t, 1)
	f.invite(t, f.participants[0])
	token := f.tokenFor(t, f.participants[0])
	if err := f.service.RequestOTP(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	issued, err := f.service.VerifyOTP(context.Background(), token, f.otp.code)
	if err != nil {
		t.Fatal(err)
	}
	captureValidator := invitationcore.Service{DB: f.db, Pepper: f.service.Pepper, Within: f.service.Within}
	if _, err := captureValidator.ValidateSession(context.Background(), issued.Token, issued.CSRF); !apperror.Is(err, apperror.Unauthenticated) {
		t.Fatalf("UT-286 capture ValidateSession accepted a COUNTERPARTY session: %v", err)
	}
	captureToken, err := security.NewScopedToken(f.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "capture-csrf"
	captureDigest := security.HashToken(captureToken)
	proof, err := security.CSRFProof(captureDigest, csrf, f.service.Pepper)
	if err != nil {
		t.Fatal(err)
	}
	responsibilityID, invitationID := identity.NewID(), identity.NewID()
	if err := f.db.Create(&database.ExternalSession{ID: identity.NewID(), TenantID: f.tenantID, Kind: "CAPTURE", InvitationID: &invitationID, ResponsibilityID: &responsibilityID, SessionDigest: captureDigest[:], CSRFDigest: proof[:], ExpiresAt: f.clock.Add(time.Hour), CreatedAt: *f.clock}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ValidateSession(context.Background(), captureToken, csrf); !apperror.Is(err, apperror.Unauthenticated) {
		t.Fatalf("UT-286 counterparty ValidateSession accepted a CAPTURE session: %v", err)
	}
}

func TestUnit_UT308_RequestOTPUnknownToken(t *testing.T) {
	f := newCounterpartyFixture(t, 1)
	f.invite(t, f.participants[0])
	wellFormed, err := security.NewScopedToken(f.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	foreignTenant, err := security.NewScopedToken(identity.NewID())
	if err != nil {
		t.Fatal(err)
	}
	within := f.service.Within
	lookups := 0
	f.service.Within = func(ctx context.Context, tenantID identity.ID, fn func(*gorm.DB) error) error {
		lookups++
		return within(ctx, tenantID, fn)
	}
	for _, tc := range []struct {
		name    string
		token   string
		queries int
	}{
		{name: "UT-308 well-formed unknown token reaches the database", token: wellFormed, queries: 2},
		{name: "UT-308 well-formed token of another tenant", token: foreignTenant, queries: 2},
		{name: "UT-308 malformed token", token: "malformed"},
		{name: "UT-308 empty token", token: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookups = 0
			assertAppError(t, f.service.RequestOTP(context.Background(), tc.token), apperror.NotFound, "token")
			_, err := f.service.VerifyOTP(context.Background(), tc.token, "123456")
			assertAppError(t, err, apperror.NotFound, "token")
			if lookups != tc.queries {
				t.Fatalf("database lookups=%d, want %d", lookups, tc.queries)
			}
		})
	}
	if f.otp.code != "" {
		t.Fatal("UT-308 unknown token delivered a code")
	}
}
