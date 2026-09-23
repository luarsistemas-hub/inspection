package llm

import (
	"context"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/observability"
)

// ObservedGateway decorates a provider gateway without changing the
// provider-neutral contract used by analysis slices.
type ObservedGateway struct {
	Inner   Gateway
	Metrics *observability.Metrics
	Logger  observability.LLMLogger
	Mode    string
	Ledger  CallLedger
}

func (g ObservedGateway) CompleteStructured(ctx context.Context, request StructuredRequest) (StructuredResult, error) {
	if g.Inner == nil {
		return StructuredResult{}, NewError(CodeTransport, 0, errMissingGateway)
	}
	if g.Logger == nil {
		g.Logger = observability.NoopLLMLogger{}
	}
	callID := identity.NewID().String()
	ctx = observability.WithCallID(ctx, callID)
	started := time.Now()
	correlation := observability.CorrelationFromContext(ctx)
	if g.Ledger != nil {
		if err := g.Ledger.Start(ctx, CallStart{
			CallID: callID, TenantID: correlation.TenantID, EventID: correlation.EventID,
			CorrelationID: correlation.CorrelationID, JobID: correlation.JobID, InspectionID: correlation.InspectionID,
			ExecutionID: correlation.ExecutionID, Attempt: correlation.Attempt, ReplayGeneration: correlation.ReplayGeneration,
			Mode: g.Mode, ComparisonMode: request.Mode, ModelAlias: request.ModelAlias, PromptDigest: request.PromptDigest,
			StartedAt: started,
		}); err != nil {
			if g.Metrics != nil {
				g.Metrics.LLMLedgerWrite("start", "error")
			}
			event := observability.EventFromContext(ctx, "llm_ledger_start_failed", "error", "ledger", "error", CodeOf(err))
			event.CallID = callID
			event.Mode = g.Mode
			event.ComparisonMode = request.Mode
			event.ModelAlias = request.ModelAlias
			g.Logger.LogLLM(ctx, event)
			return StructuredResult{CallID: callID}, NewError(CodeLedgerPersistence, 0, err)
		}
		if g.Metrics != nil {
			g.Metrics.LLMLedgerWrite("start", "success")
		}
	}
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
	result.CallID = callID
	duration := time.Since(started)
	outcome := "success"
	level := "info"
	code := ""
	if err != nil {
		code = CodeOf(err)
		outcome = code
		level = "warn"
	}
	if g.Ledger != nil {
		finishContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		finishErr := g.Ledger.Finish(finishContext, CallFinish{
			CallID: callID, Provider: result.Provider, Model: result.Model, GatewayRequestID: result.GatewayRequestID,
			TechnicalOutcome: outcome, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens, Cost: result.Cost,
			TransportDelivered: result.TransportDelivered, HTTPStatus: result.HTTPStatus, Duration: duration, FinishedAt: time.Now().UTC(),
		})
		cancel()
		if finishErr != nil {
			if g.Metrics != nil {
				g.Metrics.LLMLedgerWrite("finish", "error")
			}
			finishEvent := observability.EventFromContext(ctx, "llm_ledger_finish_failed", "error", "ledger", "error", CodeOf(finishErr))
			finishEvent.CallID = callID
			finishEvent.Mode = g.Mode
			finishEvent.ComparisonMode = request.Mode
			finishEvent.ModelAlias = request.ModelAlias
			g.Logger.LogLLM(ctx, finishEvent)
		} else if g.Metrics != nil {
			g.Metrics.LLMLedgerWrite("finish", "success")
		}
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
