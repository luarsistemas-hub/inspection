package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/gorm"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/ratelimit"
)

type allowTestOTPLimits struct{}

func (allowTestOTPLimits) AllowSend(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}
func (allowTestOTPLimits) AllowResend(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}
func (allowTestOTPLimits) AllowAttempt(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}

type unusedOTPNotifier struct{}

func (unusedOTPNotifier) SendOTP(context.Context, string, string) error { return nil }

type rejectingCaptcha struct{ calls int }

func (v *rejectingCaptcha) Verify(context.Context, string) error {
	v.calls++
	return apperror.New(apperror.InvalidInput, "turnstileToken", "Conclua a verificação de segurança e tente novamente.")
}

func TestTurnstileVerifierValidatesTokenContext(t *testing.T) {
	tests := []struct {
		name      string
		response  string
		hostname  string
		allowTest bool
		wantCode  apperror.Code
		wantField string
	}{
		{name: "valid", response: `{"success":true,"hostname":"onboard.example.test","action":"onboarding_otp"}`},
		{name: "invalid token", response: `{"success":false,"error-codes":["invalid-input-response"]}`, wantCode: apperror.InvalidInput, wantField: "turnstileToken"},
		{name: "expired or already-used token", response: `{"success":false,"error-codes":["timeout-or-duplicate"]}`, wantCode: apperror.InvalidInput, wantField: "turnstileToken"},
		{name: "wrong hostname", response: `{"success":true,"hostname":"other.example.test","action":"onboarding_otp"}`, wantCode: apperror.InvalidInput, wantField: "turnstileToken"},
		{name: "wrong action", response: `{"success":true,"hostname":"onboard.example.test","action":"login"}`, wantCode: apperror.InvalidInput, wantField: "turnstileToken"},
		{name: "test response in local environment", response: `{"success":true,"hostname":"localhost","action":"test"}`, hostname: "localhost", allowTest: true},
		{name: "test response rejected in production", response: `{"success":true,"hostname":"onboard.example.test","action":"test"}`, wantCode: apperror.InvalidInput, wantField: "turnstileToken"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("unexpected request: method=%s content-type=%s", r.Method, r.Header.Get("Content-Type"))
				}
				var body turnstileRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
				if body.Secret != "private-secret" || body.Response != "one-time-token" {
					t.Errorf("unexpected verification request")
				}
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()

			hostname := test.hostname
			if hostname == "" {
				hostname = "onboard.example.test"
			}
			verifier := TurnstileVerifier{Secret: "private-secret", Hostname: hostname, Endpoint: server.URL, AllowTestResponse: test.allowTest}
			err := verifier.Verify(context.Background(), "one-time-token")
			if test.wantCode == "" {
				if err != nil {
					t.Fatalf("Verify() error = %v", err)
				}
				return
			}
			code, field, _ := apperror.Public(err)
			if code != test.wantCode || field != test.wantField {
				t.Fatalf("Verify() error = (%q, %q), want (%q, %q): %v", code, field, test.wantCode, test.wantField, err)
			}
		})
	}
}

func TestTurnstileVerifierFailsClosedForUnavailableProviderAndMissingToken(t *testing.T) {
	verifier := TurnstileVerifier{Secret: "private-secret", Hostname: "onboard.example.test", Endpoint: "http://127.0.0.1:1"}
	for _, test := range []struct {
		name  string
		token string
		code  apperror.Code
	}{
		{name: "missing token", code: apperror.InvalidInput},
		{name: "provider unavailable", token: "one-time-token", code: apperror.DependencyUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := verifier.Verify(context.Background(), test.token)
			code, _, _ := apperror.Public(err)
			if code != test.code {
				t.Fatalf("Verify() code = %q, want %q", code, test.code)
			}
			if strings.Contains(err.Error(), test.token) && test.token != "" {
				t.Fatal("error exposed the submitted token")
			}
		})
	}
}

func TestTurnstileVerifierTreatsNonSuccessProviderStatusAsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	err := (TurnstileVerifier{Secret: "private-secret", Hostname: "onboard.example.test", Endpoint: server.URL}).Verify(context.Background(), "one-time-token")
	code, _, _ := apperror.Public(err)
	if code != apperror.DependencyUnavailable {
		t.Fatalf("Verify() code = %q, want %q", code, apperror.DependencyUnavailable)
	}
}

func TestRequestOTPRejectsCaptchaBeforeDatabaseOrEmail(t *testing.T) {
	captcha := &rejectingCaptcha{}
	service := Service{
		DB: &gorm.DB{}, Pepper: []byte("01234567890123456789012345678901"), Limits: allowTestOTPLimits{},
		Notifier: unusedOTPNotifier{}, Captcha: captcha, RequireCaptcha: true, Stage: "test",
	}
	_, err := service.RequestOTP(context.Background(), "Ana", "ana@example.test", "192.0.2.10", "invalid-token")
	code, field, _ := apperror.Public(err)
	if code != apperror.InvalidInput || field != "turnstileToken" {
		t.Fatalf("RequestOTP() error = (%q, %q), want invalid Turnstile input", code, field)
	}
	if captcha.calls != 1 {
		t.Fatalf("captcha verifier calls = %d, want 1", captcha.calls)
	}
}
