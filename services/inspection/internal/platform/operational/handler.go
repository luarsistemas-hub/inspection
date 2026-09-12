package operational

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"inspection/services/inspection/internal/platform/observability"
	"inspection/services/inspection/internal/platform/rollout"
)

type Readiness func(*http.Request) error
type RolloutStatus func(context.Context) (rollout.Status, error)

// Setup registers liveness, readiness and private metrics.
func Setup(mux *http.ServeMux, readiness Readiness, metricsToken string) error {
	return SetupWithMetrics(mux, readiness, metricsToken, nil)
}

// SetupWithMetrics registers operational routes and an optional dynamic
// notification metrics collector. The legacy Setup function remains a small
// compatibility wrapper for processes that do not emit notification metrics.
func SetupWithMetrics(mux *http.ServeMux, readiness Readiness, metricsToken string, metrics *observability.Metrics) error {
	return SetupWithRollout(mux, readiness, metricsToken, metrics, nil)
}

// SetupWithRollout registers operational routes and an optional live rollout
// status endpoint.
func SetupWithRollout(mux *http.ServeMux, readiness Readiness, metricsToken string, metrics *observability.Metrics, rolloutStatus RolloutStatus) error {
	if mux == nil || readiness == nil {
		return &setupError{"operational", "missing dependency"}
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := readiness(r); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, metricsToken) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusOK)
		if metrics == nil {
			_, _ = w.Write([]byte("inspection_up 1\n"))
			return
		}
		_, _ = w.Write([]byte(metrics.Prometheus()))
	})
	if rolloutStatus != nil {
		mux.HandleFunc("GET /rollout", func(w http.ResponseWriter, r *http.Request) {
			if !authorized(r, metricsToken) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			status, err := rolloutStatus(r.Context())
			if err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
				return
			}
			writeJSON(w, http.StatusOK, status)
		})
	}
	return nil
}

func authorized(r *http.Request, token string) bool {
	return token != "" && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") == token
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type setupError struct{ slice, reason string }

func (e *setupError) Error() string { return "slice " + e.slice + ": " + e.reason }
