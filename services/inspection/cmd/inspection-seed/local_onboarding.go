package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const localBootstrapMutation = `mutation Bootstrap($input: CreateTenantInput!) {
  createTenant(input: $input) {
    tenant { id }
    userErrors { field message code }
    clientMutationId
  }
}`

type tokenResponse struct {
	AccessToken string `json:"access_token"`
}

type bootstrapResponse struct {
	Data struct {
		CreateTenant struct {
			Tenant struct {
				ID string `json:"id"`
			} `json:"tenant"`
			UserErrors []struct {
				Field   string `json:"field"`
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"userErrors"`
		} `json:"createTenant"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func bootstrapLocalAdmin(ctx context.Context, apiURL, issuer, username, password string) error {
	if strings.TrimSpace(apiURL) == "" || strings.TrimSpace(issuer) == "" {
		return fmt.Errorf("API URL and OIDC issuer are required")
	}
	token, err := requestLocalToken(ctx, strings.TrimRight(issuer, "/")+"/protocol/openid-connect/token", username, password)
	if err != nil {
		return err
	}
	return createLocalTenant(ctx, apiURL, token, username)
}

func requestLocalToken(ctx context.Context, endpoint, username, password string) (string, error) {
	form := url.Values{
		"grant_type": {"password"},
		"client_id":  {"inspection-admin"},
		"username":   {username},
		"password":   {password},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create Keycloak token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("request Keycloak token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read Keycloak token response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("Keycloak token request returned HTTP %d", resp.StatusCode)
	}
	var payload tokenResponse
	if err := json.Unmarshal(body, &payload); err != nil || payload.AccessToken == "" {
		return "", fmt.Errorf("Keycloak token response did not contain an access token")
	}
	return payload.AccessToken, nil
}

func createLocalTenant(ctx context.Context, endpoint, token, username string) error {
	body, err := json.Marshal(map[string]any{
		"query": localBootstrapMutation,
		"variables": map[string]any{"input": map[string]any{
			"name":             "Minha operação",
			"businessUnitCode": "MATRIZ",
			"businessUnitName": "Matriz",
			"language":         "pt-BR",
			"timezone":         "America/Sao_Paulo",
			"clientMutationId": "local-seed-bootstrap-v1",
		}},
	})
	if err != nil {
		return fmt.Errorf("encode local onboarding: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("create local onboarding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("request local onboarding: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read local onboarding response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("local onboarding returned HTTP %d", resp.StatusCode)
	}
	var payload bootstrapResponse
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return fmt.Errorf("decode local onboarding response: %w", err)
	}
	if len(payload.Errors) > 0 {
		return fmt.Errorf("local onboarding GraphQL error: %s", payload.Errors[0].Message)
	}
	if len(payload.Data.CreateTenant.UserErrors) > 0 {
		userError := payload.Data.CreateTenant.UserErrors[0]
		return fmt.Errorf("local onboarding rejected for %s: %s (%s)", username, userError.Message, userError.Code)
	}
	if payload.Data.CreateTenant.Tenant.ID == "" {
		return fmt.Errorf("local onboarding response did not contain a tenant")
	}
	return nil
}
