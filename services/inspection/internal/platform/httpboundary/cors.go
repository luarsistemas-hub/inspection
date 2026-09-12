package httpboundary

import (
	"net/http"
	"strings"
)

// CORS allows exactly the configured browser origins. Credentials are enabled
// for every allowed origin because the admin and onboarding flows also use
// cookies. Wildcard origins are never permitted.
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
	for _, arg := range args {
		switch value := arg.(type) {
		case http.Handler:
			next = value
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
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Expose-Headers", "X-CSRF-Token")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-CSRF-Token, X-Correlation-ID, X-Inspection-Membership-ID")
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
