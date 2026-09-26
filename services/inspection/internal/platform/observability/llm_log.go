package observability

import (
	"context"
	"io"
	"log/slog"
)

// LLMEvent is the allow-listed, bounded payload emitted for LLM operations.
// It intentionally has no prompt, image, response, URL or provider error text.
type LLMEvent struct {
	Event, Stage, Level, Outcome, Code  string
	Service, Environment, Mode          string
	EventID, CorrelationID, CausationID string
	ExecutionID, CallID, JobID          string
	InspectionID, TenantHash            string
	TransactionPhase                    string
	Attempt, ReplayGeneration           int
	ComparisonMode, ModelAlias          string
	Provider, Model, GatewayRequestID   string
	PromptDigest, InputDigest           string
	HTTPStatus                          int
	TransportDelivered                  bool
	DurationMS                          int64
	InputTokens, OutputTokens           *int64
	CachedInputTokens                   *int64
	RequestBodyBytes                    int64
	Cost                                *float64
	Images, Findings                    int
}

type LLMLogger interface {
	LogLLM(context.Context, LLMEvent)
}

type NoopLLMLogger struct{}

func (NoopLLMLogger) LogLLM(context.Context, LLMEvent) {}

type slogLLMLogger struct {
	logger                     *slog.Logger
	service, environment, mode string
}

func NewJSONLLMLogger(w io.Writer, service, environment, mode string) LLMLogger {
	if w == nil {
		return NoopLLMLogger{}
	}
	return slogLLMLogger{logger: slog.New(slog.NewJSONHandler(w, nil)), service: service, environment: environment, mode: mode}
}

func (l slogLLMLogger) LogLLM(ctx context.Context, event LLMEvent) {
	if event.Service == "" {
		event.Service = l.service
	}
	if event.Environment == "" {
		event.Environment = l.environment
	}
	if event.Mode == "" {
		event.Mode = l.mode
	}
	event.Mode = safeLLMMode(event.Mode)
	event.Code = safeCode(event.Code)
	if event.Level == "" {
		event.Level = "info"
	}
	attrs := []any{
		"event", event.Event,
		"service", event.Service,
		"environment", event.Environment,
		"mode", event.Mode,
		"stage", event.Stage,
		"outcome", event.Outcome,
		"code", event.Code,
		"eventId", event.EventID,
		"correlationId", event.CorrelationID,
		"causationId", event.CausationID,
		"executionId", event.ExecutionID,
		"callId", event.CallID,
		"jobId", event.JobID,
		"inspectionId", event.InspectionID,
		"tenantHash", event.TenantHash,
		"transactionPhase", event.TransactionPhase,
		"attempt", event.Attempt,
		"replayGeneration", event.ReplayGeneration,
		"comparisonMode", event.ComparisonMode,
		"modelAlias", event.ModelAlias,
		"provider", event.Provider,
		"model", event.Model,
		"gatewayRequestId", event.GatewayRequestID,
		"promptDigest", event.PromptDigest,
		"inputDigest", event.InputDigest,
		"httpStatus", event.HTTPStatus,
		"transportDelivered", event.TransportDelivered,
		"durationMs", event.DurationMS,
		"images", event.Images,
		"requestBodyBytes", event.RequestBodyBytes,
		"findings", event.Findings,
	}
	if event.InputTokens != nil {
		attrs = append(attrs, "inputTokens", *event.InputTokens)
	}
	if event.OutputTokens != nil {
		attrs = append(attrs, "outputTokens", *event.OutputTokens)
	}
	if event.CachedInputTokens != nil {
		attrs = append(attrs, "cachedInputTokens", *event.CachedInputTokens)
	}
	if event.Cost != nil {
		attrs = append(attrs, "cost", *event.Cost)
	}
	level := slog.LevelInfo
	if event.Level == "warn" {
		level = slog.LevelWarn
	} else if event.Level == "error" {
		level = slog.LevelError
	}
	l.logger.Log(ctx, level, "llm_observation", attrs...)
}

func EventFromContext(ctx context.Context, event, level, stage, outcome, code string) LLMEvent {
	correlation := CorrelationFromContext(ctx)
	return LLMEvent{
		Event: event, Level: level, Stage: stage, Outcome: outcome, Code: code,
		EventID: correlation.EventID, CorrelationID: correlation.CorrelationID, CausationID: correlation.CausationID,
		ExecutionID: correlation.ExecutionID, CallID: correlation.CallID, JobID: correlation.JobID,
		InspectionID: correlation.InspectionID, TenantHash: HashIdentifier(correlation.TenantID),
		Attempt: correlation.Attempt, ReplayGeneration: correlation.ReplayGeneration,
	}
}
