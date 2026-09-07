package operational

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOperationalRoutesIT535ToIT540(t *testing.T) {
	mux := http.NewServeMux()
	ready := true
	if err := Setup(mux, func(*http.Request) error {
		if !ready {
			return errors.New("db details must remain private")
		}
		return nil
	}, "metrics-secret"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path, auth string
		want       int
	}{{"/healthz", "", 200}, {"/readyz", "", 200}, {"/metrics", "Bearer metrics-secret", 200}, {"/metrics", "", 404}}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", tc.path, nil)
		r.Header.Set("Authorization", tc.auth)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s=%d", tc.path, w.Code)
		}
	}
	ready = false
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/readyz", nil))
	if w.Code != 503 || w.Body.String() == "db details must remain private" {
		t.Fatalf("unsafe readiness: %d %s", w.Code, w.Body.String())
	}
}
