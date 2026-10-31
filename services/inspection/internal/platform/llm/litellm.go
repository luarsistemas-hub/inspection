package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
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

var decimalCostPattern = regexp.MustCompile(`^\+?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

func (g HTTPGateway) CompleteStructured(ctx context.Context, request StructuredRequest) (StructuredResult, error) {
	if g.BaseURL == "" || request.ModelAlias == "" || request.PromptDigest == "" || request.SystemPrompt == "" || request.UserPrompt == "" || len(request.JSONSchema) == 0 || len(request.Images) == 0 {
		return StructuredResult{}, NewError(CodeInvalidInput, 0, fmt.Errorf("invalid structured request"))
	}
	if isCappedImageMode(request.Mode) && len(request.Images) > 24 {
		return StructuredResult{}, NewError(CodeInvalidInput, 0, fmt.Errorf("video image request limit exceeded"))
	}
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	content := make([]map[string]any, 0, len(request.Images)*2+1)
	content = append(content, map[string]any{"type": "text", "text": request.UserPrompt})
	var videoEncodedBytes int64
	for _, image := range request.Images {
		videoSource := image.Source == "VIDEO_FRAME" && isCappedImageMode(request.Mode)
		inventorySource := isInventoryMode(request.Mode) && (image.Source == "ORIGIN" || image.Source == "CURRENT" || image.Source == "VIDEO_FRAME")
		if !strings.HasPrefix(image.DataURL, "data:image/") || image.Digest == "" || image.EvidenceID == "" || (image.Source != "CURRENT" && image.Source != "ORIGIN" && !videoSource) || (isInventoryMode(request.Mode) && !inventorySource) {
			return StructuredResult{}, NewError(CodeInvalidInput, 0, fmt.Errorf("unauthorized image"))
		}
		if isCappedImageMode(request.Mode) {
			_, encoded, ok := strings.Cut(image.DataURL, ",")
			if !ok {
				return StructuredResult{}, NewError(CodeInvalidInput, 0, fmt.Errorf("invalid video image data"))
			}
			decoded, decodeErr := base64.StdEncoding.DecodeString(encoded)
			if decodeErr != nil {
				return StructuredResult{}, NewError(CodeInvalidInput, 0, fmt.Errorf("invalid video image data"))
			}
			decodedBytes := int64(len(decoded))
			videoEncodedBytes += decodedBytes
			if videoEncodedBytes > 24<<20 {
				return StructuredResult{}, NewError(CodeInvalidInput, 0, fmt.Errorf("video encoded image limit exceeded"))
			}
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
	body, err := json.Marshal(map[string]any{"model": request.ModelAlias, "messages": []any{map[string]any{"role": "system", "content": request.SystemPrompt}, map[string]any{"role": "user", "content": content}}, "metadata": map[string]string{"comparison_mode": request.Mode}, "response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "inspection_analysis", "strict": true, "schema": json.RawMessage(request.JSONSchema)}}})
	if err != nil {
		return StructuredResult{}, NewError(CodeInvalidInput, 0, err)
	}
	if isCappedImageMode(request.Mode) && len(body) > 24<<20 {
		return StructuredResult{}, NewError(CodeInvalidInput, 0, fmt.Errorf("video request payload limit exceeded"))
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
		return StructuredResult{Latency: time.Since(started), RequestBodyBytes: int64(len(body))}, NewError(code, 0, err)
	}
	defer response.Body.Close()
	resultMetadata := StructuredResult{HTTPStatus: response.StatusCode, TransportDelivered: true, Latency: time.Since(started), RequestBodyBytes: int64(len(body))}
	resultMetadata.GatewayCallID = strings.TrimSpace(response.Header.Get("x-litellm-call-id"))
	resultMetadata.CostSource = "unknown"
	if rawCost := strings.TrimSpace(response.Header.Get("x-litellm-response-cost")); rawCost != "" {
		if decimal, ok := validDecimalCost(rawCost); ok {
			resultMetadata.ReportedCostDecimal = &decimal
			resultMetadata.CostSource = "header"
		}
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		code := CodeProviderHTTP
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			code = CodeAuthentication
		} else if response.StatusCode == http.StatusTooManyRequests {
			code = CodeRateLimit
		}
		if resultMetadata.ReportedCostDecimal == nil {
			resultMetadata.CostSource = "error_missing"
		}
		return resultMetadata, NewError(code, response.StatusCode, fmt.Errorf("provider response status %d", response.StatusCode))
	}
	var wire struct {
		ID, Model string
		Choices   []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        *int64 `json:"prompt_tokens"`
			CompletionTokens    *int64 `json:"completion_tokens"`
			PromptTokensDetails *struct {
				CachedTokens json.RawMessage `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
		Cost     *float64 `json:"cost"`
		Provider string   `json:"provider"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	if err := decoder.Decode(&wire); err != nil {
		return resultMetadata, NewError(CodeMalformedResponse, response.StatusCode, fmt.Errorf("malformed structured response"))
	}
	result := resultMetadata
	result.GatewayRequestID, result.Provider, result.Model = wire.ID, wire.Provider, wire.Model
	result.InputTokens, result.OutputTokens = wire.Usage.PromptTokens, wire.Usage.CompletionTokens
	result.Cost = wire.Cost
	if result.ReportedCostDecimal == nil && wire.Cost != nil {
		legacy := strconv.FormatFloat(*wire.Cost, 'f', -1, 64)
		if decimal, ok := validDecimalCost(legacy); ok {
			result.ReportedCostDecimal, result.CostSource = &decimal, "body"
		}
	}
	if result.ReportedCostDecimal == nil && result.CostSource == "" {
		result.CostSource = "unknown"
	}
	if wire.Usage.PromptTokensDetails != nil && len(wire.Usage.PromptTokensDetails.CachedTokens) > 0 && string(wire.Usage.PromptTokensDetails.CachedTokens) != "null" {
		var cached int64
		if decodeErr := json.Unmarshal(wire.Usage.PromptTokensDetails.CachedTokens, &cached); decodeErr != nil || cached < 0 || (wire.Usage.PromptTokens != nil && cached > *wire.Usage.PromptTokens) {
			result.InvalidCachedInputTokens = true
		} else {
			result.CachedInputTokens = &cached
		}
	}
	if len(wire.Choices) != 1 || wire.Choices[0].Message.Content == "" {
		return result, NewError(CodeMalformedResponse, response.StatusCode, fmt.Errorf("malformed structured response"))
	}
	result.JSON = []byte(wire.Choices[0].Message.Content)
	return result, nil
}

func validDecimalCost(raw string) (string, bool) {
	if !decimalCostPattern.MatchString(raw) {
		return "", false
	}
	mantissa, exponentText, hasExponent := strings.Cut(strings.ToLower(raw), "e")
	exponent := 0
	if hasExponent {
		parsed, err := strconv.Atoi(exponentText)
		if err != nil || parsed < -100 || parsed > 100 {
			return "", false
		}
		exponent = parsed
	}
	mantissa = strings.TrimPrefix(mantissa, "+")
	whole, fraction, hasFraction := strings.Cut(mantissa, ".")
	if !hasFraction {
		fraction = ""
	}
	digits := whole + fraction
	if digits == "" {
		return "", false
	}
	numerator, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return "", false
	}
	scale := len(fraction) - exponent
	denominator := big.NewInt(1)
	if scale > 0 {
		denominator.Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	} else if scale < 0 {
		numerator.Mul(numerator, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-scale)), nil))
	}
	decimal := new(big.Rat).SetFrac(numerator, denominator).FloatString(12)
	if len(strings.SplitN(decimal, ".", 2)[0]) > 8 {
		return "", false
	}
	integer, fractional, hasDecimal := strings.Cut(decimal, ".")
	if hasDecimal {
		fractional = strings.TrimRight(fractional, "0")
		decimal = integer
		if fractional != "" {
			decimal += "." + fractional
		}
	}
	if decimal == "" {
		decimal = "0"
	}
	return decimal, true
}

func isInventoryMode(mode string) bool {
	return mode == "INVENTORY_CATALOG" || mode == "INVENTORY_MATCH" || mode == "INVENTORY_COMPARE"
}

func isCappedImageMode(mode string) bool {
	return mode == "VIDEO_SCOPE" || mode == "VIDEO_MATCH" || isInventoryMode(mode)
}
