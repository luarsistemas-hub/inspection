package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPGatewayUsesAliasSchemaAndAuthorizedImages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"req-1","model":"provider/model","provider":"provider","choices":[{"message":{"content":"{\\\"noRelevantChange\\\":true,\\\"findings\\\":[]}"}}],"usage":{"prompt_tokens":3,"completion_tokens":2},"cost":0.01}`))
	}))
	defer server.Close()
	result, err := (HTTPGateway{BaseURL: server.URL}).CompleteStructured(context.Background(), StructuredRequest{ModelAlias: "inspection-vision", PromptVersion: "analysis-v1", JSONSchema: []byte(`{"type":"object"}`), Images: []NormalizedImage{{EvidenceID: "e1", Digest: "digest", DataURL: "data:image/png;base64,AAAA"}}})
	if err != nil || result.Model != "provider/model" || result.InputTokens == nil {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestHTTPGatewayRejectsMalformedProviderResponseAndUnauthorizedImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"choices":[]}`)) }))
	defer server.Close()
	_, err := (HTTPGateway{BaseURL: server.URL}).CompleteStructured(context.Background(), StructuredRequest{ModelAlias: "alias", PromptVersion: "v1", JSONSchema: []byte(`{"type":"object"}`), Images: []NormalizedImage{{EvidenceID: "e1", Digest: "d1", DataURL: "data:image/png;base64,AAAA"}}})
	if err == nil {
		t.Fatal("malformed provider response accepted")
	}
	_, err = (HTTPGateway{BaseURL: server.URL}).CompleteStructured(context.Background(), StructuredRequest{ModelAlias: "alias", PromptVersion: "v1", JSONSchema: []byte(`{"type":"object"}`), Images: []NormalizedImage{{EvidenceID: "e1", Digest: "d1", DataURL: "https://private/object"}}})
	if err == nil {
		t.Fatal("unauthorized image URL accepted")
	}
}
