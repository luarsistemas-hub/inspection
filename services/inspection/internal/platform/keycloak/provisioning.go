// Package keycloak contains the narrow confidential-client boundary used by onboarding.
package keycloak

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrUnavailable = errors.New("keycloak unavailable")
	ErrInvalid     = errors.New("keycloak rejected request")
	ErrConflict    = errors.New("keycloak identity conflict")
)

type Owner struct{ ID, Email, Name string }

type OwnerIdentityProvider interface {
	EnsureOwner(context.Context, string, string) (Owner, error)
	SendActivation(context.Context, Owner) error
	SetInitialPassword(context.Context, Owner, string) error
}

type ActivationProvider struct{ Client ProvisioningClient }

func (p ActivationProvider) SetInitialPassword(ctx context.Context, subject, password string) error {
	return p.Client.SetInitialPassword(ctx, Owner{ID: subject}, password)
}

type ProvisioningClient struct {
	BaseURL, Realm, ClientID, ClientSecret string
	HTTPClient                             *http.Client
	Timeout                                time.Duration
}

func (c ProvisioningClient) client() (*http.Client, error) {
	if c.BaseURL == "" || c.Realm == "" || c.ClientID == "" || c.ClientSecret == "" {
		return nil, errors.New("keycloak provisioning: incomplete configuration")
	}
	if c.HTTPClient != nil {
		return c.HTTPClient, nil
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &http.Client{Timeout: timeout}, nil
}

func (c ProvisioningClient) bearer(ctx context.Context) (string, error) {
	httpClient, err := c.client()
	if err != nil {
		return "", err
	}
	values := url.Values{"grant_type": {"client_credentials"}, "client_id": {c.ClientID}, "client_secret": {c.ClientSecret}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/realms/"+url.PathEscape(c.Realm)+"/protocol/openid-connect/token", strings.NewReader(values.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: token request: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", classify(resp.StatusCode, "token request")
	}
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil || payload.AccessToken == "" {
		return "", fmt.Errorf("%w: malformed token response", ErrUnavailable)
	}
	return payload.AccessToken, nil
}

func (c ProvisioningClient) EnsureOwner(ctx context.Context, email, name string) (Owner, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)
	if email == "" || name == "" {
		return Owner{}, ErrInvalid
	}
	token, err := c.bearer(ctx)
	if err != nil {
		return Owner{}, err
	}
	httpClient, _ := c.client()
	base := strings.TrimRight(c.BaseURL, "/") + "/admin/realms/" + url.PathEscape(c.Realm) + "/users"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?exact=true&email="+url.QueryEscape(email), nil)
	if err != nil {
		return Owner{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return Owner{}, fmt.Errorf("%w: user lookup: %v", ErrUnavailable, err)
	}
	var users []struct{ ID, Username, Email, FirstName, LastName string }
	decodeErr := json.NewDecoder(resp.Body).Decode(&users)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Owner{}, classify(resp.StatusCode, "user lookup")
	}
	if decodeErr != nil {
		return Owner{}, fmt.Errorf("%w: malformed user response", ErrUnavailable)
	}
	if len(users) > 1 {
		return Owner{}, ErrConflict
	}
	if len(users) == 1 {
		return Owner{ID: users[0].ID, Email: email, Name: name}, nil
	}
	body, _ := json.Marshal(map[string]any{"username": email, "email": email, "firstName": name, "enabled": true, "emailVerified": true})
	createReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base, bytes.NewReader(body))
	if err != nil {
		return Owner{}, err
	}
	createReq.Header.Set("Authorization", "Bearer "+token)
	createReq.Header.Set("Content-Type", "application/json")
	created, err := httpClient.Do(createReq)
	if err != nil {
		return Owner{}, fmt.Errorf("%w: user create: %v", ErrUnavailable, err)
	}
	defer created.Body.Close()
	if created.StatusCode == http.StatusConflict {
		return c.EnsureOwner(ctx, email, name)
	}
	if created.StatusCode < 200 || created.StatusCode >= 300 {
		return Owner{}, classify(created.StatusCode, "user create")
	}
	location := created.Header.Get("Location")
	if location == "" {
		return Owner{}, fmt.Errorf("%w: missing user location", ErrUnavailable)
	}
	return Owner{ID: location[strings.LastIndex(location, "/")+1:], Email: email, Name: name}, nil
}

func (c ProvisioningClient) SendActivation(ctx context.Context, owner Owner) error {
	token, err := c.bearer(ctx)
	if err != nil {
		return err
	}
	client, _ := c.client()
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/admin/realms/" + url.PathEscape(c.Realm) + "/users/" + url.PathEscape(owner.ID) + "/execute-actions-email"
	body, _ := json.Marshal([]string{"UPDATE_PASSWORD"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: activation email: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classify(resp.StatusCode, "activation email")
	}
	return nil
}

func (c ProvisioningClient) SetInitialPassword(ctx context.Context, owner Owner, password string) error {
	if len(password) < 12 {
		return ErrInvalid
	}
	token, err := c.bearer(ctx)
	if err != nil {
		return err
	}
	client, _ := c.client()
	body, _ := json.Marshal(map[string]any{"type": "password", "value": password, "temporary": false})
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/admin/realms/" + url.PathEscape(c.Realm) + "/users/" + url.PathEscape(owner.ID) + "/reset-password"
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: password setup: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classify(resp.StatusCode, "password setup")
	}
	return nil
}

func classify(status int, operation string) error {
	if status == http.StatusBadRequest || status == http.StatusConflict {
		return fmt.Errorf("%w: %s (%d)", ErrInvalid, operation, status)
	}
	return fmt.Errorf("%w: %s (%d)", ErrUnavailable, operation, status)
}
