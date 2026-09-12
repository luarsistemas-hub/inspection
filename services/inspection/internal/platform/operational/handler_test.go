package operational

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"inspection/services/inspection/internal/platform/rollout"
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

func TestRolloutRouteExposesGateDecision(t *testing.T) {
	mux := http.NewServeMux()
	status := rollout.Status{Phase: "drain-v1-work", Snapshot: rollout.Snapshot{V1QueueDepth: 1}}
	var reads int
	if err := SetupWithRollout(mux, func(*http.Request) error { return nil }, "rollout-secret", nil, func(context.Context) (rollout.Status, error) {
		reads++
		return status, nil
	}); err != nil {
		t.Fatal(err)
	}
	unauthorized := httptest.NewRecorder()
	mux.ServeHTTP(unauthorized, httptest.NewRequest("GET", "/rollout", nil))
	if unauthorized.Code != http.StatusNotFound {
		t.Fatalf("unauthorized rollout status=%d", unauthorized.Code)
	}
	if reads != 0 {
		t.Fatalf("rollout status was read for unauthorized request: %d", reads)
	}

	r := httptest.NewRequest("GET", "/rollout", nil)
	r.Header.Set("Authorization", "Bearer rollout-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("rollout status=%d", w.Code)
	}
	var got rollout.Status
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Phase != status.Phase || got.Snapshot.V1QueueDepth != 1 {
		t.Fatalf("unexpected rollout response: %+v", got)
	}
}
