package observability

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"inspection/services/inspection/internal/contracts/events"
)

// TransactionObservation is emitted only after the inbox transaction has
// returned. A successful observation therefore means the transaction committed.
type TransactionObservation struct {
	ExecutionID string
	Outcome     string
	Phase       string
	Processed   bool
	Duration    time.Duration
	Err         error
}

type ConsumerObserver interface {
	AfterTransaction(context.Context, events.RawEnvelope, TransactionObservation)
}

type DeliveryObservation struct {
	ExecutionID, Queue, CorrelationID string
	Action, Result, Reason            string
	Attempt                           int
	Duration                          time.Duration
	Err                               error
}

type DeliveryObserver interface {
	AfterDelivery(context.Context, DeliveryObservation)
}

// WorkerObserver connects the generic messaging lifecycle to bounded analysis
// metrics and structured logs. It deliberately ignores non-analysis events.
type WorkerObserver struct {
	Metrics *Metrics
	Logger  LLMLogger
}

func NewWorkerObserver(metrics *Metrics, logger LLMLogger) WorkerObserver {
	if logger == nil {
		logger = NoopLLMLogger{}
	}
	return WorkerObserver{Metrics: metrics, Logger: logger}
}

func (o WorkerObserver) AfterTransaction(ctx context.Context, envelope events.RawEnvelope, observation TransactionObservation) {
	if envelope.Type != "analysis.comparison_requested.v1" || !hasJobID(envelope.Payload) {
		return
	}
	if o.Metrics != nil {
		outcome := observation.Outcome
		if outcome == "" {
			outcome = "error"
		}
		o.Metrics.AnalysisProcessing(outcome)
	}
	level := "info"
	if observation.Outcome == "error" {
		level = "error"
	}
	eventName := "analysis_processing"
	if observation.Phase == "rollback" {
		eventName = "analysis_transaction_rollback"
	} else if observation.Phase == "commit_error" {
		eventName = "analysis_transaction_commit_error"
	}
	event := EventFromContext(ctx, eventName, level, "processing", observation.Outcome, errorCode(observation.Err))
	event.EventID = envelope.ID.String()
	event.CorrelationID = envelope.CorrelationID
	event.CausationID = envelope.CausationID
	event.TransactionPhase = observation.Phase
	event.DurationMS = observation.Duration.Milliseconds()
	o.Logger.LogLLM(ctx, event)
}

func (o WorkerObserver) AfterDelivery(ctx context.Context, observation DeliveryObservation) {
	if observation.Queue != "analysis-comparison-requested" {
		return
	}
	if observation.Action != "retry" && observation.Action != "dlq" {
		return
	}
	if o.Metrics != nil {
		o.Metrics.AnalysisDelivery(observation.Action, observation.Result)
	}
	level := "warn"
	if observation.Result == "publish_failed" || observation.Action == "dlq" {
		level = "error"
	}
	event := EventFromContext(ctx, "analysis_delivery", level, "delivery", observation.Result, errorCode(observation.Err))
	event.EventID = ""
	event.CorrelationID = observation.CorrelationID
	event.Attempt = observation.Attempt
	event.DurationMS = observation.Duration.Milliseconds()
	o.Logger.LogLLM(ctx, event)
}

func hasJobID(payload []byte) bool {
	var value struct {
		JobID string `json:"jobId"`
	}
	return json.Unmarshal(payload, &value) == nil && value.JobID != ""
}

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		return coded.ErrorCode()
	}
	return "internal_error"
}
