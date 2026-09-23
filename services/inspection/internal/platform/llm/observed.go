package llm

import (
	"context"
	"time"

	"inspection/services/inspection/internal/platform/observability"

	"github.com/google/uuid"
)

// ObservedGateway decorates a provider gateway without changing the
// provider-neutral contract used by analysis slices.
type ObservedGateway struct {
	Inner   Gateway
	Metrics *observability.Metrics
	Logger  observability.LLMLogger
	Mode    string
}

func (g ObservedGateway) CompleteStructured(ctx context.Context, request StructuredRequest) (StructuredResult, error) {
	if g.Inner == nil {
		return StructuredResult{}, NewError(CodeTransport, 0, errMissingGateway)
	}
	if g.Logger == nil {
		g.Logger = observability.NoopLLMLogger{}
	}
	callID := uuid.NewString()
	ctx = observability.WithCallID(ctx, callID)
	started := time.Now()
	if g.Metrics != nil {
		g.Metrics.LLMCallStarted(g.Mode, request.ModelAlias)
	}
	startEvent := observability.EventFromContext(ctx, "llm_call_started", "info", "llm", "started", "")
	startEvent.CallID = callID
	startEvent.Mode = g.Mode
	startEvent.ComparisonMode = request.Mode
	startEvent.ModelAlias = request.ModelAlias
	startEvent.PromptDigest = request.PromptDigest
	startEvent.Images = len(request.Images)
	g.Logger.LogLLM(ctx, startEvent)

	result, err := g.Inner.CompleteStructured(ctx, request)
	duration := time.Since(started)
	outcome := "success"
	level := "info"
	code := ""
	if err != nil {
		code = CodeOf(err)
		outcome = code
		level = "warn"
	}
	if g.Metrics != nil {
		g.Metrics.LLMCallFinished(g.Mode, request.Mode, request.ModelAlias, outcome, duration, result.TransportDelivered, result.InputTokens, result.OutputTokens)
	}
	endEvent := observability.EventFromContext(ctx, "llm_call_finished", level, "llm", outcome, code)
	endEvent.CallID = callID
	endEvent.Mode = g.Mode
	endEvent.ComparisonMode = request.Mode
	endEvent.ModelAlias = request.ModelAlias
	endEvent.Provider = result.Provider
	endEvent.Model = result.Model
	endEvent.GatewayRequestID = result.GatewayRequestID
	endEvent.PromptDigest = request.PromptDigest
	endEvent.HTTPStatus = result.HTTPStatus
	endEvent.TransportDelivered = result.TransportDelivered
	endEvent.DurationMS = duration.Milliseconds()
	endEvent.InputTokens = result.InputTokens
	endEvent.OutputTokens = result.OutputTokens
	endEvent.Cost = result.Cost
	endEvent.Images = len(request.Images)
	g.Logger.LogLLM(ctx, endEvent)
	return result, err
}
