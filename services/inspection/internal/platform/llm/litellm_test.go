package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPGatewayUsesAliasSchemaAndAuthorizedImages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer gateway-secret" {
			t.Fatalf("authorization=%q", got)
		}
		var body struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Messages) != 2 || body.Messages[0].Role != "system" || body.Messages[1].Role != "user" {
			t.Fatalf("messages=%s", body.Messages)
		}
		if string(body.Messages[0].Content) != `"system"` || !strings.Contains(string(body.Messages[1].Content), "evidenceId=e1; source=CURRENT; pairId=pair-1; position=CURRENT_1") || !strings.Contains(string(body.Messages[1].Content), `"detail":"high"`) {
			t.Fatalf("unexpected prompt=%s", body.Messages[1].Content)
		}
		_, _ = w.Write([]byte(`{"id":"req-1","model":"provider/model","provider":"provider","choices":[{"message":{"content":"{\\\"coverageStatus\\\":\\\"COMPLETE\\\",\\\"comparisonStatus\\\":\\\"UNCHANGED\\\",\\\"findings\\\":[]}"}}],"usage":{"prompt_tokens":3,"completion_tokens":2},"cost":0.01}`))
	}))
	defer server.Close()
	result, err := (HTTPGateway{BaseURL: server.URL, APIKey: "gateway-secret"}).CompleteStructured(context.Background(), StructuredRequest{ModelAlias: "inspection-vision", PromptDigest: "digest", SystemPrompt: "system", UserPrompt: "context", JSONSchema: []byte(`{"type":"object"}`), Images: []NormalizedImage{{EvidenceID: "e1", Source: "CURRENT", PairID: "pair-1", Position: "CURRENT_1", Digest: "digest", DataURL: "data:image/png;base64,AAAA"}}})
	if err != nil || result.Model != "provider/model" || result.InputTokens == nil || result.HTTPStatus != http.StatusOK || !result.TransportDelivered {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestHTTPGatewayRejectsMalformedProviderResponseAndUnauthorizedImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"choices":[]}`)) }))
	defer server.Close()
	result, err := (HTTPGateway{BaseURL: server.URL}).CompleteStructured(context.Background(), StructuredRequest{ModelAlias: "alias", PromptDigest: "d", SystemPrompt: "s", UserPrompt: "u", JSONSchema: []byte(`{"type":"object"}`), Images: []NormalizedImage{{EvidenceID: "e1", Source: "CURRENT", Digest: "d1", DataURL: "data:image/png;base64,AAAA"}}})
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != CodeMalformedResponse || result.HTTPStatus != http.StatusOK || !result.TransportDelivered {
		t.Fatalf("malformed provider response result=%#v err=%v", result, err)
	}
	_, err = (HTTPGateway{BaseURL: server.URL}).CompleteStructured(context.Background(), StructuredRequest{ModelAlias: "alias", PromptDigest: "d", SystemPrompt: "s", UserPrompt: "u", JSONSchema: []byte(`{"type":"object"}`), Images: []NormalizedImage{{EvidenceID: "e1", Source: "CURRENT", Digest: "d1", DataURL: "https://private/object"}}})
	if !errors.As(err, &typed) || typed.Code != CodeInvalidInput {
		t.Fatalf("unauthorized image URL was not classified: %v", err)
	}
}

func TestHTTPGatewayClassifiesProviderHTTPStatuses(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   ErrorCode
	}{
		{http.StatusUnauthorized, CodeAuthentication},
		{http.StatusForbidden, CodeAuthentication},
		{http.StatusTooManyRequests, CodeRateLimit},
		{http.StatusBadGateway, CodeProviderHTTP},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
		}))
		result, err := (HTTPGateway{BaseURL: server.URL}).CompleteStructured(context.Background(), validRequest())
		server.Close()
		var typed *Error
		if !errors.As(err, &typed) || typed.Code != tc.code || result.HTTPStatus != tc.status || !result.TransportDelivered || result.Latency <= 0 {
			t.Fatalf("status=%d result=%#v err=%v", tc.status, result, err)
		}
	}
}

func TestHTTPGatewayClassifiesTimeoutNetworkAndMissingUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-time.After(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	result, err := (HTTPGateway{BaseURL: server.URL}).CompleteStructured(ctx, validRequest())
	cancel()
	server.Close()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != CodeTimeout || result.TransportDelivered || result.Latency <= 0 {
		t.Fatalf("timeout result=%#v err=%v", result, err)
	}

	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	networkURL := closed.URL
	closed.Close()
	result, err = (HTTPGateway{BaseURL: networkURL}).CompleteStructured(context.Background(), validRequest())
	if !errors.As(err, &typed) || typed.Code != CodeTransport || result.TransportDelivered {
		t.Fatalf("network result=%#v err=%v", result, err)
	}

	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"req","choices":[{"message":{"content":"{}"}}]}`))
	}))
	result, err = (HTTPGateway{BaseURL: usageServer.URL}).CompleteStructured(context.Background(), validRequest())
	usageServer.Close()
	if err != nil || result.InputTokens != nil || result.OutputTokens != nil || !result.TransportDelivered {
		t.Fatalf("missing usage result=%#v err=%v", result, err)
	}
}

func validRequest() StructuredRequest {
	return StructuredRequest{ModelAlias: "inspection-vision", PromptDigest: "digest", SystemPrompt: "system", UserPrompt: "user", JSONSchema: []byte(`{"type":"object"}`), Images: []NormalizedImage{{EvidenceID: "e1", Source: "CURRENT", Digest: "d1", DataURL: "data:image/png;base64,AAAA"}}}
}
