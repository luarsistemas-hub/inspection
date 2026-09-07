package httpboundary

import (
	"net/http"
	"net/http/httptest"
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

	rejected := httptest.NewRecorder()
	bad := httptest.NewRequest(http.MethodPost, "/graphql", nil)
	bad.Header.Set("Origin", "https://attacker.example")
	h.ServeHTTP(rejected, bad)
	if rejected.Code != http.StatusForbidden {
		t.Fatalf("rejected origin status = %d", rejected.Code)
	}
}
