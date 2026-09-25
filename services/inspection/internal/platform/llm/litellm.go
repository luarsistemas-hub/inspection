package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	APIKey  string
	Client  *http.Client
}

func (g HTTPGateway) CompleteStructured(ctx context.Context, request StructuredRequest) (StructuredResult, error) {
	if g.BaseURL == "" || request.ModelAlias == "" || request.PromptDigest == "" || request.SystemPrompt == "" || request.UserPrompt == "" || len(request.JSONSchema) == 0 || len(request.Images) == 0 {
		return StructuredResult{}, NewError(CodeInvalidInput, 0, fmt.Errorf("invalid structured request"))
	}
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	content := make([]map[string]any, 0, len(request.Images)*2+1)
	content = append(content, map[string]any{"type": "text", "text": request.UserPrompt})
	for _, image := range request.Images {
		if !strings.HasPrefix(image.DataURL, "data:image/") || image.Digest == "" || image.EvidenceID == "" || (image.Source != "CURRENT" && image.Source != "ORIGIN") {
			return StructuredResult{}, NewError(CodeInvalidInput, 0, fmt.Errorf("unauthorized image"))
		}
		label := "evidenceId=" + image.EvidenceID + "; role=" + image.Source
		if image.PairID != "" {
			if image.Position == "" {
				return StructuredResult{}, NewError(CodeInvalidInput, 0, fmt.Errorf("paired image has no position"))
			}
			label += "; pairId=" + image.PairID + "; position=" + image.Position
		}
		content = append(content, map[string]any{"type": "text", "text": label})
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": image.DataURL, "detail": "high"}})
	}
	body, err := json.Marshal(map[string]any{"model": request.ModelAlias, "messages": []any{map[string]any{"role": "system", "content": request.SystemPrompt}, map[string]any{"role": "user", "content": content}}, "response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "inspection_analysis", "strict": true, "schema": json.RawMessage(request.JSONSchema)}}})
	if err != nil {
		return StructuredResult{}, NewError(CodeInvalidInput, 0, err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(g.BaseURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return StructuredResult{}, NewError(CodeInvalidInput, 0, err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if g.APIKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+g.APIKey)
	}
	started := time.Now()
	response, err := client.Do(httpRequest)
	if err != nil {
		code := CodeTransport
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = CodeTimeout
		} else if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			code = CodeCancelled
		}
		return StructuredResult{Latency: time.Since(started)}, NewError(code, 0, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		code := CodeProviderHTTP
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			code = CodeAuthentication
		} else if response.StatusCode == http.StatusTooManyRequests {
			code = CodeRateLimit
		}
		return StructuredResult{HTTPStatus: response.StatusCode, TransportDelivered: true, Latency: time.Since(started)}, NewError(code, response.StatusCode, fmt.Errorf("provider response status %d", response.StatusCode))
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
	if err := decoder.Decode(&wire); err != nil {
		return StructuredResult{HTTPStatus: response.StatusCode, TransportDelivered: true, Latency: time.Since(started)}, NewError(CodeMalformedResponse, response.StatusCode, fmt.Errorf("malformed structured response"))
	}
	result := StructuredResult{GatewayRequestID: wire.ID, Provider: wire.Provider, Model: wire.Model, InputTokens: wire.Usage.PromptTokens, OutputTokens: wire.Usage.CompletionTokens, Cost: wire.Cost, HTTPStatus: response.StatusCode, TransportDelivered: true, Latency: time.Since(started)}
	if len(wire.Choices) != 1 || wire.Choices[0].Message.Content == "" {
		return result, NewError(CodeMalformedResponse, response.StatusCode, fmt.Errorf("malformed structured response"))
	}
	result.JSON = []byte(wire.Choices[0].Message.Content)
	return result, nil
}
