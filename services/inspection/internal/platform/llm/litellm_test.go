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

	"inspection/services/inspection/internal/platform/video"
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
		if string(body.Messages[0].Content) != `"system"` || !strings.Contains(string(body.Messages[1].Content), "evidenceId=e1; role=CURRENT; pairId=pair-1; position=CURRENT_1") || !strings.Contains(string(body.Messages[1].Content), `"detail":"high"`) {
			t.Fatalf("unexpected prompt=%s", body.Messages[1].Content)
		}
		_, _ = w.Write([]byte(`{"id":"req-1","model":"provider/model","provider":"provider","choices":[{"message":{"content":"{\\\"noRelevantChange\\\":true,\\\"findings\\\":[]}"}}],"usage":{"prompt_tokens":3,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":2}},"cost":0.01}`))
	}))
	defer server.Close()
	result, err := (HTTPGateway{BaseURL: server.URL, APIKey: "gateway-secret"}).CompleteStructured(context.Background(), StructuredRequest{ModelAlias: "inspection-vision", PromptDigest: "digest", SystemPrompt: "system", UserPrompt: "context", JSONSchema: []byte(`{"type":"object"}`), Images: []NormalizedImage{{EvidenceID: "e1", Source: "CURRENT", PairID: "pair-1", Position: "CURRENT_1", Digest: "digest", DataURL: "data:image/png;base64,AAAA"}}})
	if err != nil || result.Model != "provider/model" || result.InputTokens == nil || result.CachedInputTokens == nil || *result.CachedInputTokens != 2 || result.RequestBodyBytes <= 0 || result.HTTPStatus != http.StatusOK || !result.TransportDelivered {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestHTTPGatewayRejectsInvalidCachedTokenCountsWithoutFailingResponse(t *testing.T) {
	for _, cached := range []string{"-1", "4", `"four"`, "1.5"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{}"}}],"usage":{"prompt_tokens":3,"prompt_tokens_details":{"cached_tokens":` + cached + `}}}`))
		}))
		result, err := (HTTPGateway{BaseURL: server.URL}).CompleteStructured(context.Background(), validRequest())
		server.Close()
		if err != nil || result.CachedInputTokens != nil || !result.InvalidCachedInputTokens {
			t.Fatalf("cached=%s result=%#v err=%v", cached, result, err)
		}
	}
}

func TestHTTPGatewayPreservesKnownZeroCachedTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{}"}}],"usage":{"prompt_tokens":3,"prompt_tokens_details":{"cached_tokens":0}}}`))
	}))
	defer server.Close()
	result, err := (HTTPGateway{BaseURL: server.URL}).CompleteStructured(context.Background(), validRequest())
	if err != nil || result.CachedInputTokens == nil || *result.CachedInputTokens != 0 || result.InvalidCachedInputTokens {
		t.Fatalf("known cache miss was not preserved: result=%#v err=%v", result, err)
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

func TestHTTPGatewayAllowsVideoFramesOnlyForAuxiliaryModes(t *testing.T) {
	image := NormalizedImage{EvidenceID: "frame-1", Source: "VIDEO_FRAME", Digest: "digest", DataURL: "data:image/jpeg;base64,AAAA"}
	_, err := (HTTPGateway{BaseURL: "http://127.0.0.1:1"}).CompleteStructured(context.Background(), StructuredRequest{ModelAlias: "inspection-video-preparation", PromptDigest: "digest", Mode: "VIDEO_SCOPE", SystemPrompt: "system", UserPrompt: "scope", JSONSchema: []byte(`{"type":"object"}`), Images: []NormalizedImage{image}})
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != CodeTransport {
		t.Fatalf("video frame was rejected before auxiliary gateway call: %v", err)
	}
	_, err = (HTTPGateway{BaseURL: "http://127.0.0.1:1"}).CompleteStructured(context.Background(), StructuredRequest{ModelAlias: "inspection-vision", PromptDigest: "digest", Mode: "CURRENT_ONLY", SystemPrompt: "system", UserPrompt: "condition", JSONSchema: []byte(`{"type":"object"}`), Images: []NormalizedImage{image}})
	if !errors.As(err, &typed) || typed.Code != CodeInvalidInput {
		t.Fatalf("video frame reached condition processing: %v", err)
	}
	request := StructuredRequest{ModelAlias: "inspection-video-preparation", PromptDigest: "digest", Mode: "VIDEO_SCOPE", SystemPrompt: "system", UserPrompt: "scope", JSONSchema: []byte(`{"type":"object"}`), Images: make([]NormalizedImage, 25)}
	_, err = (HTTPGateway{BaseURL: "http://127.0.0.1:1"}).CompleteStructured(context.Background(), request)
	if !errors.As(err, &typed) || typed.Code != CodeInvalidInput {
		t.Fatalf("oversized semantic batch reached gateway: %v", err)
	}
}

func TestVideoScopeGatewayPreservesKnownFrameObservationsUT080(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(body)
		for _, id := range []string{"F1", "F2"} {
			if !strings.Contains(string(encoded), "evidenceId="+id) {
				t.Fatalf("frame %s omitted from gateway request", id)
			}
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"observations\":[{\"frameId\":\"F1\",\"environmentCandidate\":\"kitchen\",\"componentLabel\":\"sink\",\"viewLabel\":\"wide\",\"visibility\":\"VISIBLE\",\"landmarks\":[]},{\"frameId\":\"F2\",\"environmentCandidate\":\"bathroom\",\"componentLabel\":\"basin\",\"viewLabel\":\"wide\",\"visibility\":\"VISIBLE\",\"landmarks\":[]}],\"unresolvedFrameIds\":[]}"}}]}`))
	}))
	defer server.Close()
	request := StructuredRequest{ModelAlias: "inspection-video-preparation", PromptDigest: "digest", Mode: "VIDEO_SCOPE", SystemPrompt: "system", UserPrompt: "scope", JSONSchema: []byte(`{"type":"object"}`), Images: []NormalizedImage{{EvidenceID: "F1", Source: "VIDEO_FRAME", Digest: "d1", DataURL: "data:image/jpeg;base64,AAAA"}, {EvidenceID: "F2", Source: "VIDEO_FRAME", Digest: "d2", DataURL: "data:image/jpeg;base64,AAAA"}}}
	result, err := (HTTPGateway{BaseURL: server.URL}).CompleteStructured(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	var scope struct {
		Observations []video.ScopeObservation `json:"observations"`
		Unresolved   []string                 `json:"unresolvedFrameIds"`
	}
	if err := json.Unmarshal(result.JSON, &scope); err != nil {
		t.Fatal(err)
	}
	if err := video.ValidateScope([]string{"F1", "F2"}, scope.Observations, scope.Unresolved); err != nil {
		t.Fatalf("gateway observations did not preserve valid frame IDs: %v", err)
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

func TestHTTPGatewayClassifiesTimeoutNetworkAndMissingUsageUT081(t *testing.T) {
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
