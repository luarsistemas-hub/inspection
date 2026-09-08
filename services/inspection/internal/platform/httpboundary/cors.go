package httpboundary

import (
	"net/http"
	"strings"
)

// CORS allows exactly the configured browser origin. Credentials are needed
// only by the external capture session cookie, so wildcard origins are never
// permitted.
func CORS(configured interface{}, args ...interface{}) http.Handler {
	allowed := map[string]struct{}{}
	switch value := configured.(type) {
	case string:
		allowed[value] = struct{}{}
	case []string:
		for _, origin := range value {
			allowed[origin] = struct{}{}
		}
	}
	var next http.Handler
	captureOrigin := ""
	for _, arg := range args {
		switch value := arg.(type) {
		case http.Handler:
			next = value
		case string:
			captureOrigin = value
		}
	}
	if next == nil {
		panic("httpboundary: missing next handler")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if _, ok := allowed[origin]; !ok || strings.Contains(origin, "*") {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			if captureOrigin == "" || origin == captureOrigin {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-CSRF-Token, X-Correlation-ID")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
