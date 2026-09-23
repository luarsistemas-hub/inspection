package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"inspection/services/inspection/internal/platform/observability"
)

type observedGatewayStub struct {
	result StructuredResult
	err    error
}

func (s observedGatewayStub) CompleteStructured(context.Context, StructuredRequest) (StructuredResult, error) {
	return s.result, s.err
}

type observedLedgerStub struct {
	startErr, finishErr error
	starts, finishes    int
}

func (s *observedLedgerStub) Start(context.Context, CallStart) error {
	s.starts++
	return s.startErr
}

func (s *observedLedgerStub) Finish(context.Context, CallFinish) error {
	s.finishes++
	return s.finishErr
}

type countingGateway struct {
	calls  int
	result StructuredResult
	err    error
}

func (g *countingGateway) CompleteStructured(context.Context, StructuredRequest) (StructuredResult, error) {
	g.calls++
	return g.result, g.err
}

func TestObservedGatewayLogsSuccessfulCallAndUsage(t *testing.T) {
	var output bytes.Buffer
	logger := observability.NewJSONLLMLogger(&output, "inspection-worker", "test", "mock")
	metrics := observability.NewMetrics()
	input, outputTokens := int64(7), int64(3)
	gateway := ObservedGateway{Inner: observedGatewayStub{result: StructuredResult{Provider: "provider", Model: "model", GatewayRequestID: "request", InputTokens: &input, OutputTokens: &outputTokens, TransportDelivered: true}}, Metrics: metrics, Logger: logger, Mode: "mock"}
	ctx := observability.WithCorrelation(context.Background(), observability.Correlation{EventID: "event", CorrelationID: "corr", ExecutionID: "exec", JobID: "job", InspectionID: "inspection", TenantID: "tenant"})

	_, err := gateway.CompleteStructured(ctx, StructuredRequest{ModelAlias: "inspection-vision", PromptDigest: "prompt-digest", Mode: "CURRENT_ONLY", Images: make([]NormalizedImage, 2)})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected start and finish logs, got %d: %s", len(lines), output.String())
	}
	var start, finish map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &start); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &finish); err != nil {
		t.Fatal(err)
	}
	if start["event"] != "llm_call_started" || finish["event"] != "llm_call_finished" || start["callId"] != finish["callId"] {
		t.Fatalf("unexpected correlation: start=%v finish=%v", start, finish)
	}
	if finish["gatewayRequestId"] != "request" || finish["inputTokens"] != float64(7) || finish["outputTokens"] != float64(3) {
		t.Fatalf("provider metadata missing: %v", finish)
	}
	if strings.Contains(output.String(), "system prompt") || strings.Contains(output.String(), "data:image") {
		t.Fatalf("sensitive request content leaked: %s", output.String())
	}
	if !strings.Contains(metrics.Prometheus(), "inspection_llm_inflight") || !strings.Contains(metrics.Prometheus(), " 0\n") {
		t.Fatalf("inflight gauge did not return to zero: %s", metrics.Prometheus())
	}
}

func TestHTTPGatewayClassifiesRateLimitAndCancellation(t *testing.T) {
	server := newTestServer(t, 429, `{"error":"rate limited"}`)
	defer server.Close()
	request := StructuredRequest{ModelAlias: "inspection-vision", PromptDigest: "digest", SystemPrompt: "system", UserPrompt: "user", JSONSchema: []byte(`{"type":"object"}`), Images: []NormalizedImage{{EvidenceID: "e1", Source: "CURRENT", Digest: "d1", DataURL: "data:image/png;base64,AAAA"}}}
	result, err := (HTTPGateway{BaseURL: server.URL}).CompleteStructured(context.Background(), request)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != CodeRateLimit || result.HTTPStatus != 429 || !result.TransportDelivered {
		t.Fatalf("unexpected rate limit result=%#v err=%v", result, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = (HTTPGateway{BaseURL: server.URL}).CompleteStructured(ctx, request)
	if !errors.As(err, &typed) || typed.Code != CodeCancelled {
		t.Fatalf("unexpected cancellation error=%v", err)
	}
}

func TestObservedGatewayDoesNotCallProviderWhenLedgerStartFails(t *testing.T) {
	inner := &countingGateway{}
	ledger := &observedLedgerStub{startErr: errors.New("ledger unavailable")}
	gateway := ObservedGateway{Inner: inner, Ledger: ledger, Mode: "live"}
	_, err := gateway.CompleteStructured(context.Background(), StructuredRequest{ModelAlias: "inspection-vision", PromptDigest: "digest"})
	if CodeOf(err) != string(CodeLedgerPersistence) || inner.calls != 0 || ledger.starts != 1 || ledger.finishes != 0 {
		t.Fatalf("start failure was not isolated: err=%v calls=%d starts=%d finishes=%d", err, inner.calls, ledger.starts, ledger.finishes)
	}
}

func TestObservedGatewayDoesNotRepeatProviderWhenLedgerFinishFails(t *testing.T) {
	inner := &countingGateway{result: StructuredResult{TransportDelivered: true}}
	ledger := &observedLedgerStub{finishErr: errors.New("ledger unavailable")}
	gateway := ObservedGateway{Inner: inner, Ledger: ledger, Mode: "live"}
	_, err := gateway.CompleteStructured(context.Background(), StructuredRequest{ModelAlias: "inspection-vision", PromptDigest: "digest"})
	if err != nil || inner.calls != 1 || ledger.starts != 1 || ledger.finishes != 1 {
		t.Fatalf("finish failure changed provider result: err=%v calls=%d starts=%d finishes=%d", err, inner.calls, ledger.starts, ledger.finishes)
	}
}

func newTestServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}
