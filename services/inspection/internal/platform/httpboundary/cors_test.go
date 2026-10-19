package httpboundary

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORSAllowsOnlyConfiguredOrigin(t *testing.T) {
	h := CORS("https://app.example", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	req := httptest.NewRequest(http.MethodOptions, "/graphql", nil)
	req.Header.Set("Origin", "https://app.example")
	allowed := httptest.NewRecorder()
	h.ServeHTTP(allowed, req)
	if allowed.Code != http.StatusNoContent || allowed.Header().Get("Access-Control-Allow-Origin") != "https://app.example" {
		t.Fatalf("allowed origin response = %#v", allowed.Result())
	}
	if got := allowed.Header().Get("Access-Control-Allow-Headers"); got == "" || !containsHeader(got, "X-Inspection-Membership-ID") {
		t.Fatalf("membership header not allowed: %q", got)
	}

	rejected := httptest.NewRecorder()
	bad := httptest.NewRequest(http.MethodPost, "/graphql", nil)
	bad.Header.Set("Origin", "https://attacker.example")
	h.ServeHTTP(rejected, bad)
	if rejected.Code != http.StatusForbidden {
		t.Fatalf("rejected origin status = %d", rejected.Code)
	}
}

func containsHeader(headers, wanted string) bool {
	for _, header := range strings.Split(headers, ",") {
		if strings.TrimSpace(header) == wanted {
			return true
		}
	}
	return false
}

func TestCORSAllowsCredentialsAndCSRFHeaderForEveryConfiguredOrigin(t *testing.T) {
	h := CORS([]string{"https://admin.example", "https://capture.example"}, "https://capture.example", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	admin := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/graphql", nil)
	req.Header.Set("Origin", "https://admin.example")
	h.ServeHTTP(admin, req)
	if admin.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("admin origin missing credential permission")
	}
	if !containsHeader(admin.Header().Get("Access-Control-Expose-Headers"), "X-CSRF-Token") {
		t.Fatal("admin origin cannot read CSRF token header")
	}
	capture := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/graphql", nil)
	req.Header.Set("Origin", "https://capture.example")
	h.ServeHTTP(capture, req)
	if capture.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("capture origin missing credential permission")
	}
	if !containsHeader(capture.Header().Get("Access-Control-Expose-Headers"), "X-CSRF-Token") {
		t.Fatal("capture origin cannot read CSRF token header")
	}
}

func TestCORSAllowsAuthenticatedMediaReadPreflight(t *testing.T) {
	h := CORS("https://dashboard.example", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	req := httptest.NewRequest(http.MethodOptions, "/media/video-evidence/frame", nil)
	req.Header.Set("Origin", "https://dashboard.example")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "authorization,x-inspection-membership-id,range")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusNoContent || !containsHeader(response.Header().Get("Access-Control-Allow-Methods"), http.MethodGet) || !containsHeader(response.Header().Get("Access-Control-Allow-Headers"), "Authorization") || !containsHeader(response.Header().Get("Access-Control-Allow-Headers"), "Range") || !containsHeader(response.Header().Get("Access-Control-Expose-Headers"), "Content-Range") {
		t.Fatalf("media preflight denied: status=%d headers=%v", response.Code, response.Header())
	}
}
