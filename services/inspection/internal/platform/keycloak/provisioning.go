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
	"unicode"
)

var (
	ErrUnavailable = errors.New("keycloak unavailable")
	ErrInvalid     = errors.New("keycloak rejected request")
	ErrConflict    = errors.New("keycloak identity conflict")
)

type Owner struct{ ID, Email, Name string }

// InternalUser identifies a Keycloak account by its verified email address.
type InternalUser struct {
	Owner
	// Created is true while the account has no password credential yet, either
	// because it was just created or because a previous invitation never finished.
	Created bool
}

type OwnerIdentityProvider interface {
	EnsureOwner(context.Context, string, string) (Owner, error)
	SendActivation(context.Context, Owner) error
	SetInitialPassword(context.Context, Owner, string) error
}

type ActivationProvider struct{ Client ProvisioningClient }

func (p ActivationProvider) SetInitialPassword(ctx context.Context, subject, password string) error {
	return p.Client.SetInitialPassword(ctx, Owner{ID: subject}, password)
}

type InternalUserActivationProvider struct{ Client ProvisioningClient }

func (p InternalUserActivationProvider) VerifyEmail(ctx context.Context, subject string) error {
	return p.Client.VerifyEmail(ctx, subject)
}

func (p InternalUserActivationProvider) SetInitialPassword(ctx context.Context, subject, password string) error {
	return p.Client.SetInitialPassword(ctx, Owner{ID: subject}, password)
}

type ProvisioningClient struct {
	BaseURL, Realm, ClientID, ClientSecret string
	Stage                                  string
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

// EnsureInternalUser finds or creates an account without marking a newly
// supplied email as verified. The access invitation flow verifies ownership.
func (c ProvisioningClient) EnsureInternalUser(ctx context.Context, email, name string) (InternalUser, error) {
	return c.ensureInternalUser(ctx, email, name, false)
}

func (c ProvisioningClient) ensureInternalUser(ctx context.Context, email, name string, retried bool) (InternalUser, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)
	if email == "" || name == "" {
		return InternalUser{}, ErrInvalid
	}
	token, err := c.bearer(ctx)
	if err != nil {
		return InternalUser{}, err
	}
	client, _ := c.client()
	base := strings.TrimRight(c.BaseURL, "/") + "/admin/realms/" + url.PathEscape(c.Realm) + "/users"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?exact=true&email="+url.QueryEscape(email), nil)
	if err != nil {
		return InternalUser{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return InternalUser{}, fmt.Errorf("%w: user lookup: %v", ErrUnavailable, err)
	}
	var users []struct{ ID, Email string }
	decodeErr := json.NewDecoder(resp.Body).Decode(&users)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return InternalUser{}, classify(resp.StatusCode, "user lookup")
	}
	if decodeErr != nil {
		return InternalUser{}, fmt.Errorf("%w: malformed user response", ErrUnavailable)
	}
	if len(users) > 1 {
		return InternalUser{}, ErrConflict
	}
	if len(users) == 1 {
		hasPassword, err := c.hasPassword(ctx, client, base, token, users[0].ID)
		if err != nil {
			return InternalUser{}, err
		}
		return InternalUser{Owner: Owner{ID: users[0].ID, Email: email, Name: name}, Created: !hasPassword}, nil
	}
	body, _ := json.Marshal(map[string]any{"username": email, "email": email, "firstName": name, "enabled": true, "emailVerified": false})
	createReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base, bytes.NewReader(body))
	if err != nil {
		return InternalUser{}, err
	}
	createReq.Header.Set("Authorization", "Bearer "+token)
	createReq.Header.Set("Content-Type", "application/json")
	created, err := client.Do(createReq)
	if err != nil {
		return InternalUser{}, fmt.Errorf("%w: user create: %v", ErrUnavailable, err)
	}
	defer created.Body.Close()
	if created.StatusCode == http.StatusConflict {
		// A concurrent create may have won the race; retry the lookup once.
		if retried {
			return InternalUser{}, ErrConflict
		}
		return c.ensureInternalUser(ctx, email, name, true)
	}
	if created.StatusCode < 200 || created.StatusCode >= 300 {
		return InternalUser{}, classify(created.StatusCode, "user create")
	}
	location := created.Header.Get("Location")
	if location == "" {
		return InternalUser{}, fmt.Errorf("%w: missing user location", ErrUnavailable)
	}
	return InternalUser{Owner: Owner{ID: location[strings.LastIndex(location, "/")+1:], Email: email, Name: name}, Created: true}, nil
}

func (c ProvisioningClient) hasPassword(ctx context.Context, client *http.Client, base, token, subject string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+url.PathEscape(subject)+"/credentials", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("%w: credential lookup: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, classify(resp.StatusCode, "credential lookup")
	}
	var credentials []struct{ Type string }
	if err := json.NewDecoder(resp.Body).Decode(&credentials); err != nil {
		return false, fmt.Errorf("%w: malformed credential response", ErrUnavailable)
	}
	for _, credential := range credentials {
		if credential.Type == "password" {
			return true, nil
		}
	}
	return false, nil
}

// VerifyEmail records email ownership after the invitation code was accepted.
func (c ProvisioningClient) VerifyEmail(ctx context.Context, subject string) error {
	token, err := c.bearer(ctx)
	if err != nil {
		return err
	}
	client, _ := c.client()
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/admin/realms/" + url.PathEscape(c.Realm) + "/users/" + url.PathEscape(subject)
	body, _ := json.Marshal(map[string]any{"emailVerified": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: email verification: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classify(resp.StatusCode, "email verification")
	}
	return nil
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
	if !c.passwordAllowed(password) {
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

func (c ProvisioningClient) passwordAllowed(password string) bool {
	if strings.TrimSpace(password) == "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(c.Stage), "dev") {
		return true
	}
	runes := []rune(password)
	if len(runes) < 6 {
		return false
	}
	var uppercase, special bool
	for _, char := range runes {
		uppercase = uppercase || unicode.IsUpper(char)
		special = special || unicode.IsPunct(char) || unicode.IsSymbol(char)
	}
	return uppercase && special
}

func classify(status int, operation string) error {
	if status == http.StatusBadRequest || status == http.StatusConflict {
		return fmt.Errorf("%w: %s (%d)", ErrInvalid, operation, status)
	}
	return fmt.Errorf("%w: %s (%d)", ErrUnavailable, operation, status)
}
