package graphql

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGraphQLTransportIT587ToIT598(t *testing.T) {
	local := Handler{Local: true}
	w := httptest.NewRecorder()
	local.ServeHTTP(w, httptest.NewRequest("GET", "/graphql", nil))
	if w.Code != 200 {
		t.Fatalf("local GET=%d", w.Code)
	}
	production := Handler{}
	w = httptest.NewRecorder()
	production.ServeHTTP(w, httptest.NewRequest("GET", "/graphql", nil))
	if w.Code != 404 {
		t.Fatalf("production GET=%d", w.Code)
	}
	w = httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/graphql", strings.NewReader(`{"query":"bad"}`))
	r.Header.Set("X-Correlation-ID", "corr")
	production.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "INVALID_INPUT") || !strings.Contains(w.Body.String(), "corr") {
		t.Fatalf("invalid error: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/graphql", strings.NewReader(`{"query":"{ me { identityId } }"}`))
	r.Header.Set("X-Correlation-ID", "corr")
	production.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "UNAUTHENTICATED") || !strings.Contains(w.Body.String(), "corr") {
		t.Fatalf("auth error: %s", w.Body.String())
	}
}
