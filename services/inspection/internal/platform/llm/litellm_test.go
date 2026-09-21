package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
		if string(body.Messages[0].Content) != `"system"` || !strings.Contains(string(body.Messages[1].Content), "evidenceId=e1; source=CURRENT") || !strings.Contains(string(body.Messages[1].Content), `"detail":"high"`) {
			t.Fatalf("unexpected prompt=%s", body.Messages[1].Content)
		}
		_, _ = w.Write([]byte(`{"id":"req-1","model":"provider/model","provider":"provider","choices":[{"message":{"content":"{\\\"noRelevantChange\\\":true,\\\"findings\\\":[]}"}}],"usage":{"prompt_tokens":3,"completion_tokens":2},"cost":0.01}`))
	}))
	defer server.Close()
	result, err := (HTTPGateway{BaseURL: server.URL, APIKey: "gateway-secret"}).CompleteStructured(context.Background(), StructuredRequest{ModelAlias: "inspection-vision", PromptDigest: "digest", SystemPrompt: "system", UserPrompt: "context", JSONSchema: []byte(`{"type":"object"}`), Images: []NormalizedImage{{EvidenceID: "e1", Source: "CURRENT", Digest: "digest", DataURL: "data:image/png;base64,AAAA"}}})
	if err != nil || result.Model != "provider/model" || result.InputTokens == nil {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestHTTPGatewayRejectsMalformedProviderResponseAndUnauthorizedImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"choices":[]}`)) }))
	defer server.Close()
	_, err := (HTTPGateway{BaseURL: server.URL}).CompleteStructured(context.Background(), StructuredRequest{ModelAlias: "alias", PromptDigest: "d", SystemPrompt: "s", UserPrompt: "u", JSONSchema: []byte(`{"type":"object"}`), Images: []NormalizedImage{{EvidenceID: "e1", Source: "CURRENT", Digest: "d1", DataURL: "data:image/png;base64,AAAA"}}})
	if err == nil {
		t.Fatal("malformed provider response accepted")
	}
	_, err = (HTTPGateway{BaseURL: server.URL}).CompleteStructured(context.Background(), StructuredRequest{ModelAlias: "alias", PromptDigest: "d", SystemPrompt: "s", UserPrompt: "u", JSONSchema: []byte(`{"type":"object"}`), Images: []NormalizedImage{{EvidenceID: "e1", Source: "CURRENT", Digest: "d1", DataURL: "https://private/object"}}})
	if err == nil {
		t.Fatal("unauthorized image URL accepted")
	}
}
