package observability

import (
	"net/http"
	"strings"
	"time"
)

// HTTPMetrics observes bounded route and status labels without buffering bodies.
func HTTPMetrics(metrics *Metrics, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := &observedResponseWriter{ResponseWriter: w}
		var responseWriter http.ResponseWriter = wrapped
		if _, ok := w.(http.Flusher); ok {
			responseWriter = flushingResponseWriter{observedResponseWriter: wrapped}
		}
		next.ServeHTTP(responseWriter, r)
		status := wrapped.status
		if status == 0 {
			status = http.StatusOK
		}
		metrics.ObserveHTTPRequest(classifyHTTPRoute(r.URL.Path), httpStatusClass(status), time.Since(started))
	})
}

func classifyHTTPRoute(path string) string {
	switch {
	case path == "/graphql" || strings.HasPrefix(path, "/graphql/"):
		return "graphql"
	case path == "/media" || strings.HasPrefix(path, "/media/"):
		return "media"
	case path == "/healthz" || path == "/readyz":
		return "health"
	default:
		return "other"
	}
}

func httpStatusClass(status int) string {
	switch {
	case status >= 200 && status < 300:
		return "2xx"
	case status >= 300 && status < 400:
		return "3xx"
	case status >= 400 && status < 500:
		return "4xx"
	default:
		return "5xx"
	}
}

type observedResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *observedResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *observedResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *observedResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type flushingResponseWriter struct{ *observedResponseWriter }

func (w flushingResponseWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	w.ResponseWriter.(http.Flusher).Flush()
}
