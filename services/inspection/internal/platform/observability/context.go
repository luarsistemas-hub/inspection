package observability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync/atomic"
)

type correlationContextKey struct{}
type outcomeRecorderContextKey struct{}

// Correlation carries bounded identifiers across the worker processing path.
// TenantID is kept only in memory; loggers should emit TenantHash instead.
type Correlation struct {
	EventID, CorrelationID, CausationID string
	TenantID                            string
	JobID, InspectionID                 string
	ExecutionID, CallID                 string
	Attempt, ReplayGeneration           int
}

func WithCorrelation(ctx context.Context, correlation Correlation) context.Context {
	return context.WithValue(ctx, correlationContextKey{}, correlation)
}

func CorrelationFromContext(ctx context.Context) Correlation {
	if ctx == nil {
		return Correlation{}
	}
	correlation, _ := ctx.Value(correlationContextKey{}).(Correlation)
	return correlation
}

func WithExecutionID(ctx context.Context, executionID string) context.Context {
	correlation := CorrelationFromContext(ctx)
	correlation.ExecutionID = executionID
	return WithCorrelation(ctx, correlation)
}

func WithCallID(ctx context.Context, callID string) context.Context {
	correlation := CorrelationFromContext(ctx)
	correlation.CallID = callID
	return WithCorrelation(ctx, correlation)
}

// EnsureExecutionID preserves a delivery execution ID when one already exists
// and creates one for direct callers that do not pass through RabbitMQ.
func EnsureExecutionID(ctx context.Context, newID func() string) context.Context {
	if CorrelationFromContext(ctx).ExecutionID != "" {
		return ctx
	}
	if newID == nil {
		return ctx
	}
	return WithExecutionID(ctx, newID())
}

func HashIdentifier(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

// OutcomeRecorder lets a transaction handler describe a business outcome to a
// consumer observer without emitting a completion signal before commit.
type OutcomeRecorder struct {
	value       atomic.Value
	correlation atomic.Value
}

func NewOutcomeRecorder() *OutcomeRecorder {
	recorder := &OutcomeRecorder{}
	recorder.value.Store("")
	recorder.correlation.Store(Correlation{})
	return recorder
}

func WithOutcomeRecorder(ctx context.Context, recorder *OutcomeRecorder) context.Context {
	return context.WithValue(ctx, outcomeRecorderContextKey{}, recorder)
}

func OutcomeRecorderFromContext(ctx context.Context) *OutcomeRecorder {
	if ctx == nil {
		return nil
	}
	recorder, _ := ctx.Value(outcomeRecorderContextKey{}).(*OutcomeRecorder)
	return recorder
}

func (r *OutcomeRecorder) Set(value string) {
	if r != nil && value != "" {
		r.value.Store(value)
	}
}

func (r *OutcomeRecorder) Get() string {
	if r == nil {
		return ""
	}
	value, _ := r.value.Load().(string)
	return value
}

func (r *OutcomeRecorder) SetCorrelation(correlation Correlation) {
	if r != nil {
		r.correlation.Store(correlation)
	}
}

func (r *OutcomeRecorder) Correlation() Correlation {
	if r == nil {
		return Correlation{}
	}
	correlation, _ := r.correlation.Load().(Correlation)
	return correlation
}
