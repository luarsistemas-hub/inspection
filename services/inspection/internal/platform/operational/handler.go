package operational

import (
	"encoding/json"
	"net/http"
	"strings"
)

type Readiness func(*http.Request) error

// Setup registers liveness, readiness and private metrics.
func Setup(mux *http.ServeMux, readiness Readiness, metricsToken string) error {
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
		if metricsToken == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") != metricsToken {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("inspection_up 1\n"))
	})
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type setupError struct{ slice, reason string }

func (e *setupError) Error() string { return "slice " + e.slice + ": " + e.reason }
