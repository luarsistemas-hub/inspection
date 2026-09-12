package keycloak

import (
	"context"
	"encoding/json"
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
			sawPassword = body["value"] == "safe-password-123"
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := ProvisioningClient{BaseURL: server.URL, Realm: "inspection", ClientID: "client", ClientSecret: "secret"}
	if err := client.SetInitialPassword(context.Background(), Owner{ID: "user-1"}, "safe-password-123"); err != nil {
		t.Fatal(err)
	}
	if !sawPassword {
		t.Fatal("password was not sent to the Keycloak adapter boundary")
	}
	if err := client.SetInitialPassword(context.Background(), Owner{ID: "user-1"}, "short"); err != ErrInvalid {
		t.Fatalf("short password error = %v", err)
	}
}
