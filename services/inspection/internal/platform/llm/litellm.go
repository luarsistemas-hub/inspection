package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPGateway is the narrow LiteLLM OpenAI-compatible adapter. Product code
// depends only on Gateway and therefore never sees provider request types.
type HTTPGateway struct {
	BaseURL string
	Client  *http.Client
}

func (g HTTPGateway) CompleteStructured(ctx context.Context, request StructuredRequest) (StructuredResult, error) {
	if g.BaseURL == "" || request.ModelAlias == "" || request.PromptVersion == "" || len(request.JSONSchema) == 0 || len(request.Images) == 0 {
		return StructuredResult{}, fmt.Errorf("litellm: invalid structured request")
	}
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	content := make([]map[string]any, 0, len(request.Images)+1)
	content = append(content, map[string]any{"type": "text", "text": "Prompt version: " + request.PromptVersion})
	for _, image := range request.Images {
		if !strings.HasPrefix(image.DataURL, "data:image/") || image.Digest == "" || image.EvidenceID == "" {
			return StructuredResult{}, fmt.Errorf("litellm: unauthorized image")
		}
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]string{"url": image.DataURL}})
	}
	body, _ := json.Marshal(map[string]any{"model": request.ModelAlias, "messages": []any{map[string]any{"role": "user", "content": content}}, "response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "inspection_analysis", "strict": true, "schema": json.RawMessage(request.JSONSchema)}}})
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(g.BaseURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return StructuredResult{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	started := time.Now()
	response, err := client.Do(httpRequest)
	if err != nil {
		return StructuredResult{}, fmt.Errorf("litellm: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return StructuredResult{}, fmt.Errorf("litellm: response status %d", response.StatusCode)
	}
	var wire struct {
		ID, Model string
		Choices   []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     *int64 `json:"prompt_tokens"`
			CompletionTokens *int64 `json:"completion_tokens"`
		} `json:"usage"`
		Cost     *float64 `json:"cost"`
		Provider string   `json:"provider"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	if err := decoder.Decode(&wire); err != nil || len(wire.Choices) != 1 || wire.Choices[0].Message.Content == "" {
		return StructuredResult{}, fmt.Errorf("litellm: malformed structured response")
	}
	return StructuredResult{JSON: []byte(wire.Choices[0].Message.Content), GatewayRequestID: wire.ID, Provider: wire.Provider, Model: wire.Model, InputTokens: wire.Usage.PromptTokens, OutputTokens: wire.Usage.CompletionTokens, Cost: wire.Cost, Latency: time.Since(started)}, nil
}
