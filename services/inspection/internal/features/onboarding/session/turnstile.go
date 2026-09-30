package session

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"inspection/services/inspection/internal/platform/apperror"
)

const turnstileSiteverifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// TurnstileVerifier validates tokens with Cloudflare's Siteverify endpoint.
type TurnstileVerifier struct {
	Secret            string
	Hostname          string
	AllowTestResponse bool
	Client            *http.Client
	Endpoint          string
}

type turnstileRequest struct {
	Secret   string `json:"secret"`
	Response string `json:"response"`
}

type turnstileResponse struct {
	Success    bool     `json:"success"`
	Hostname   string   `json:"hostname"`
	Action     string   `json:"action"`
	ErrorCodes []string `json:"error-codes"`
}

// Verify checks that the token is valid, unused, and intended for this form.
func (v TurnstileVerifier) Verify(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 2048 {
		return apperror.New(apperror.InvalidInput, "turnstileToken", "Conclua a verificação de segurança e tente novamente.")
	}
	if strings.TrimSpace(v.Secret) == "" || strings.TrimSpace(v.Hostname) == "" {
		return apperror.New(apperror.DependencyUnavailable, "", "service temporarily unavailable")
	}

	body, err := json.Marshal(turnstileRequest{Secret: v.Secret, Response: token})
	if err != nil {
		return apperror.Wrap(apperror.Internal, err)
	}
	endpoint := v.Endpoint
	if endpoint == "" {
		endpoint = turnstileSiteverifyURL
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return apperror.Wrap(apperror.Internal, err)
	}
	request.Header.Set("Content-Type", "application/json")
	client := v.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		slog.WarnContext(ctx, "onboarding Turnstile verification unavailable", "error", err.Error())
		return apperror.Wrap(apperror.DependencyUnavailable, err)
	}
	defer response.Body.Close()
	var result turnstileResponse
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&result) != nil {
		slog.WarnContext(ctx, "onboarding Turnstile returned an invalid response", "status", response.StatusCode)
		return apperror.New(apperror.DependencyUnavailable, "", "service temporarily unavailable")
	}
	if !result.Success {
		slog.InfoContext(ctx, "onboarding Turnstile rejected a token", "errorCodes", result.ErrorCodes)
		return apperror.New(apperror.InvalidInput, "turnstileToken", "Conclua a verificação de segurança e tente novamente.")
	}
	actionMatches := result.Action == "onboarding_otp" || (v.AllowTestResponse && result.Action == "test")
	if !actionMatches || !strings.EqualFold(result.Hostname, v.Hostname) {
		slog.WarnContext(ctx, "onboarding Turnstile token context mismatch", "action", result.Action, "hostname", result.Hostname)
		return apperror.New(apperror.InvalidInput, "turnstileToken", "Conclua a verificação de segurança e tente novamente.")
	}
	return nil
}
