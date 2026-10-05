package keycloak

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProvisioningClientSetsPasswordThroughAdminBoundary(t *testing.T) {
	var sawPassword bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/protocol/openid-connect/token"):
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "token"})
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/reset-password"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			sawPassword = body["value"] == "Safe-password-123!"
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := ProvisioningClient{BaseURL: server.URL, Realm: "inspection", ClientID: "client", ClientSecret: "secret"}
	if err := client.SetInitialPassword(context.Background(), Owner{ID: "user-1"}, "Safe-password-123!"); err != nil {
		t.Fatal(err)
	}
	if !sawPassword {
		t.Fatal("password was not sent to the Keycloak adapter boundary")
	}
	if err := client.SetInitialPassword(context.Background(), Owner{ID: "user-1"}, "short"); err != ErrInvalid {
		t.Fatalf("short password error = %v", err)
	}
	client.Stage = "dev"
	if err := client.SetInitialPassword(context.Background(), Owner{ID: "user-1"}, ""); err != ErrInvalid {
		t.Fatalf("empty password should be rejected in dev: %v", err)
	}
	if err := client.SetInitialPassword(context.Background(), Owner{ID: "user-1"}, "short"); err != nil {
		t.Fatalf("dev password should skip policy: %v", err)
	}
}

func TestVerifyUserIDRequiresExactExistingSubject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/protocol/openid-connect/token"):
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "token"})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/users"):
			if r.URL.Query().Get("username") != "inspection-super-admin" || r.URL.Query().Get("exact") != "true" {
				t.Errorf("unexpected user query: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode([]map[string]string{{"id": "expected-id", "username": "inspection-super-admin"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := ProvisioningClient{BaseURL: server.URL, Realm: "inspection", ClientID: "client", ClientSecret: "secret"}
	if err := client.VerifyUserID(context.Background(), "inspection-super-admin", "expected-id"); err != nil {
		t.Fatal(err)
	}
	if err := client.VerifyUserID(context.Background(), "inspection-super-admin", "different-id"); !errors.Is(err, ErrConflict) {
		t.Fatalf("mismatched subject error = %v, want ErrConflict", err)
	}
}

func TestInternalUserIsCreatedUnverifiedAndVerifiedOnlyAfterAcceptance(t *testing.T) {
	var createdUnverified, verified, resetPassword bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/protocol/openid-connect/token"):
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "token"})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/users"):
			_ = json.NewEncoder(w).Encode([]any{})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/users"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			createdUnverified = body["emailVerified"] == false && body["email"] == "ada@example.test"
			w.Header().Set("Location", "/users/user-1")
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/users/user-1"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			verified = body["emailVerified"] == true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/reset-password"):
			resetPassword = true
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := ProvisioningClient{BaseURL: server.URL, Realm: "inspection", ClientID: "client", ClientSecret: "secret", Stage: "dev"}
	user, err := client.EnsureInternalUser(context.Background(), "Ada@Example.Test", "Ada")
	if err != nil {
		t.Fatal(err)
	}
	if !user.Created || user.ID != "user-1" || !createdUnverified {
		t.Fatalf("unexpected provisioned user: %+v", user)
	}
	if err := (InternalUserActivationProvider{Client: client}).VerifyEmail(context.Background(), user.ID); err != nil {
		t.Fatal(err)
	}
	if err := (InternalUserActivationProvider{Client: client}).SetInitialPassword(context.Background(), user.ID, "temporary"); err != nil {
		t.Fatal(err)
	}
	if !verified || !resetPassword {
		t.Fatalf("activation did not verify/set password: verified=%v password=%v", verified, resetPassword)
	}
}
