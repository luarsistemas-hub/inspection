package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"time"
)

var errUnknownTranscriptionProfile = errors.New("unknown transcription profile")

// HTTPTranscriptionGateway sends audio-only requests to LiteLLM's OpenAI-compatible API.
type HTTPTranscriptionGateway struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

// ConfiguredTimeout returns the effective request timeout for the HTTP client.
func (g HTTPTranscriptionGateway) ConfiguredTimeout() time.Duration {
	if g.Client != nil && g.Client.Timeout > 0 {
		return g.Client.Timeout
	}
	return DefaultTranscriptionTimeout
}

// Transcribe returns literal speech without summarizing, inferring, or cataloging it.
func (g HTTPTranscriptionGateway) Transcribe(ctx context.Context, request TranscriptionRequest) (TranscriptionResult, error) {
	if g.BaseURL == "" || request.Profile.Key == "" || request.Audio == nil || request.PromptDigest == "" ||
		request.Profile.MaxOutputTokens <= 0 || request.Profile.Version <= 0 || request.Profile.ModelAlias == "" ||
		request.AudioDuration <= 0 || request.AudioDuration > time.Minute || !supportedTranscriptionMIME(request.MIME) {
		return TranscriptionResult{}, NewError(CodeInvalidInput, 0, fmt.Errorf("invalid transcription request"))
	}
	profile, err := ResolveTranscriptionProfile(request.Profile.Key)
	if err != nil || profile != request.Profile {
		return TranscriptionResult{}, NewError(CodeInvalidInput, 0, errUnknownTranscriptionProfile)
	}
	audio, err := io.ReadAll(io.LimitReader(request.Audio, (5<<20)+1))
	if err != nil || len(audio) == 0 || len(audio) > 5<<20 {
		return TranscriptionResult{}, NewError(CodeInvalidInput, 0, fmt.Errorf("invalid audio payload"))
	}
	format := map[string]string{"audio/webm": "webm", "audio/ogg": "ogg", "audio/mp4": "mp4", "audio/mpeg": "mp3"}[strings.ToLower(strings.TrimSpace(strings.Split(request.MIME, ";")[0]))]
	schema := json.RawMessage("{\"type\":\"object\",\"additionalProperties\":false,\"properties\":{\"transcript\":{\"type\":\"string\"},\"noSpeech\":{\"type\":\"boolean\"}},\"required\":[\"transcript\",\"noSpeech\"]}")
	systemPrompt := "Transcreva literalmente a fala audível em português. Não resuma, não infira itens e não acrescente palavras. Se não houver fala inteligível, retorne transcript vazio e noSpeech true."
	userContent := []any{
		map[string]any{"type": "text", "text": "Transcreva o áudio literalmente."},
		map[string]any{"type": "input_audio", "input_audio": map[string]string{"data": base64.StdEncoding.EncodeToString(audio), "format": format}},
	}
	body, err := json.Marshal(map[string]any{
		"model": request.Profile.ModelAlias,
		"messages": []any{
			map[string]any{"role": "system", "content": systemPrompt},
			map[string]any{"role": "user", "content": userContent},
		},
		"max_tokens":      request.Profile.MaxOutputTokens,
		"metadata":        map[string]string{"comparison_mode": "TRANSCRIPTION", "prompt_digest": request.PromptDigest},
		"response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "literal_transcription", "strict": true, "schema": schema}},
	})
	if err != nil {
		return TranscriptionResult{}, NewError(CodeInvalidInput, 0, err)
	}
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: DefaultTranscriptionTimeout}
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(g.BaseURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return TranscriptionResult{}, NewError(CodeInvalidInput, 0, err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if g.APIKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+g.APIKey)
	}
	var wroteRequest atomic.Bool
	httpRequest = httpRequest.WithContext(httptrace.WithClientTrace(httpRequest.Context(), &httptrace.ClientTrace{
		WroteRequest: func(info httptrace.WroteRequestInfo) { wroteRequest.Store(info.Err == nil) },
	}))
	started := time.Now()
	response, err := client.Do(httpRequest)
	if err != nil {
		code := CodeTransport
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = CodeTimeout
		} else if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			code = CodeCancelled
		}
		return TranscriptionResult{Usage: StructuredResult{TransportDelivered: wroteRequest.Load(), RequestBodyBytes: int64(len(body)), Latency: time.Since(started)}}, NewError(code, 0, err)
	}
	defer response.Body.Close()
	usage := StructuredResult{HTTPStatus: response.StatusCode, TransportDelivered: true, RequestBodyBytes: int64(len(body)), Latency: time.Since(started), CostSource: "unknown"}
	usage.GatewayCallID = strings.TrimSpace(response.Header.Get("x-litellm-call-id"))
	if raw := strings.TrimSpace(response.Header.Get("x-litellm-response-cost")); raw != "" {
		if value, ok := validDecimalCost(raw); ok {
			usage.ReportedCostDecimal, usage.CostSource = &value, "header"
		}
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		if usage.ReportedCostDecimal == nil {
			usage.CostSource = "error_missing"
		}
		code := CodeProviderHTTP
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			code = CodeAuthentication
		} else if response.StatusCode == http.StatusTooManyRequests {
			code = CodeRateLimit
		}
		return TranscriptionResult{Usage: usage}, NewError(code, response.StatusCode, fmt.Errorf("provider response status %d", response.StatusCode))
	}
	var wire struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil {
		code := CodeTransport
		cause := readErr
		var networkErr net.Error
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.As(readErr, &networkErr) && networkErr.Timeout() {
			code = CodeTimeout
		} else if errors.Is(ctx.Err(), context.Canceled) {
			code = CodeCancelled
		}
		return TranscriptionResult{Usage: usage}, NewError(code, response.StatusCode, cause)
	}
	if err := json.NewDecoder(bytes.NewReader(responseBody)).Decode(&wire); err != nil {
		return TranscriptionResult{Usage: usage}, NewError(CodeMalformedResponse, response.StatusCode, fmt.Errorf("malformed transcription response"))
	}
	if len(wire.Choices) != 1 {
		return TranscriptionResult{Usage: usage}, NewError(CodeMalformedResponse, response.StatusCode, fmt.Errorf("malformed transcription response"))
	}
	var payload struct {
		Transcript string `json:"transcript"`
		NoSpeech   bool   `json:"noSpeech"`
	}
	payloadDecoder := json.NewDecoder(strings.NewReader(wire.Choices[0].Message.Content))
	payloadDecoder.DisallowUnknownFields()
	if err := payloadDecoder.Decode(&payload); err != nil || (payload.NoSpeech && payload.Transcript != "") || (!payload.NoSpeech && strings.TrimSpace(payload.Transcript) == "") {
		return TranscriptionResult{Usage: usage}, NewError(CodeMalformedResponse, response.StatusCode, fmt.Errorf("malformed transcription result"))
	}
	usage.GatewayRequestID, usage.Model, usage.Provider = wire.ID, wire.Model, "litellm"
	usage.InputTokens, usage.OutputTokens = &wire.Usage.PromptTokens, &wire.Usage.CompletionTokens
	usage.JSON = []byte(wire.Choices[0].Message.Content)
	return TranscriptionResult{Transcript: payload.Transcript, NoSpeech: payload.NoSpeech, Usage: usage}, nil
}

func supportedTranscriptionMIME(mime string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0])) {
	case "audio/webm", "audio/ogg", "audio/mp4", "audio/mpeg":
		return true
	default:
		return false
	}
}
