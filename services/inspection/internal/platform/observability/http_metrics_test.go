package observability

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPMetricsClassifiesRouteAndStatus(t *testing.T) {
	metrics := NewMetrics()
	handler := HTTPMetrics(metrics, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql" {
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))

	for _, path := range []string{"/graphql", "/media/object/secret-id", "/healthz", "/tenant/secret-id"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	output := metrics.Prometheus()
	for _, sample := range []string{
		`inspection_http_request_duration_seconds_count{route="graphql",status_class="4xx"} 1`,
		`inspection_http_request_duration_seconds_count{route="media",status_class="2xx"} 1`,
		`inspection_http_request_duration_seconds_count{route="health",status_class="2xx"} 1`,
		`inspection_http_request_duration_seconds_count{route="other",status_class="2xx"} 1`,
	} {
		if !strings.Contains(output, sample) {
			t.Fatalf("missing sample %q in:\n%s", sample, output)
		}
	}
	if strings.Contains(output, "secret-id") || strings.Contains(output, "/tenant/") {
		t.Fatalf("raw path leaked into labels: %s", output)
	}
}

func TestHTTPMetricsPreservesFlusherAndStreamsBody(t *testing.T) {
	metrics := NewMetrics()
	body := "first chunk\nsecond chunk\n"
	flushed := false
	handler := HTTPMetrics(metrics, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("wrapped writer does not preserve http.Flusher")
		}
		_, _ = io.WriteString(w, "first chunk\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "second chunk\n")
	}))
	recorder := &flushRecorder{ResponseRecorder: httptest.NewRecorder(), onFlush: func() { flushed = true }}
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/media/stream", nil))

	if recorder.Body.String() != body {
		t.Fatalf("body changed: got %q, want %q", recorder.Body.String(), body)
	}
	if !flushed {
		t.Fatal("underlying writer was not flushed")
	}
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	onFlush func()
}

func (w *flushRecorder) Flush() {
	w.onFlush()
	w.ResponseRecorder.Flush()
}
