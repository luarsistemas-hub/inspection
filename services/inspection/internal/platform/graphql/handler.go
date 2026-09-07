package graphql

import (
	"encoding/json"
	"net/http"
	"strings"

	"inspection/services/inspection/internal/platform/apperror"
)

// Handler provides a strict schema-first transport shell. Generated resolvers
// are attached through operation slices as the schema grows.
type Handler struct{ Local bool }

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	correlation := r.Header.Get("X-Correlation-ID")
	if correlation == "" {
		correlation = "unavailable"
	}
	if r.Method == http.MethodGet {
		if h.Local {
			_, _ = w.Write([]byte(`{"graphql":"development tooling"}`))
			return
		}
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil || strings.TrimSpace(body.Query) == "" {
		writeError(w, MapError(apperror.New(apperror.InvalidInput, "query", "invalid GraphQL document"), correlation))
		return
	}
	if !strings.Contains(body.Query, "{") {
		writeError(w, MapError(apperror.New(apperror.InvalidInput, "query", "invalid GraphQL document"), correlation))
		return
	}
	writeError(w, MapError(apperror.New(apperror.Unauthenticated, "", "authentication required"), correlation))
}
func writeError(w http.ResponseWriter, e PublicError) {
	_ = json.NewEncoder(w).Encode(map[string]any{"data": nil, "errors": []any{map[string]any{"message": e.Message, "extensions": e}}})
}
