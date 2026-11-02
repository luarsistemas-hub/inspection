package process_note

import (
	"context"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/usage/record_llm_call"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/observability"
)

// AttemptLedgerAdapter records each transcription transport through record_llm_call.
type AttemptLedgerAdapter struct {
	Ledger       llm.CallLedger
	Mode         string
	correlations sync.Map
}

type ledgerCorrelation struct {
	value   observability.Correlation
	created time.Time
}

// NewAttemptLedger adapts the existing durable record_llm_call slice.
func NewAttemptLedger(db *gorm.DB, mode string) (AttemptLedger, error) {
	ledger, err := record_llm_call.Setup(record_llm_call.Dependencies{DB: db})
	if err != nil {
		return nil, err
	}
	return &AttemptLedgerAdapter{Ledger: ledger, Mode: mode}, nil
}

// Start creates a durable call record and saves its structured correlation.
func (a *AttemptLedgerAdapter) Start(ctx context.Context, job Job) (string, error) {
	if a.Ledger == nil {
		return "", errors.New("missing call ledger")
	}
	callID, eventID, executionID := identity.NewID(), identity.NewID(), identity.NewID()
	mode := a.Mode
	if mode == "" {
		mode = "live"
	}
	if mode != "live" && mode != "mock" {
		return "", errors.New("invalid ledger mode")
	}
	ctx = observability.WithCorrelation(ctx, observability.Correlation{TenantID: job.TenantID.String(), JobID: job.JobID.String(), InspectionID: job.InspectionID.String(), EventID: eventID.String(), CorrelationID: job.NoteID.String(), ExecutionID: executionID.String(), CallID: callID.String(), Attempt: job.Attempt, ReplayGeneration: job.Generation})
	err := a.Ledger.Start(ctx, llm.CallStart{CallID: callID.String(), TenantID: job.TenantID.String(), EventID: eventID.String(), CorrelationID: job.NoteID.String(), JobID: job.JobID.String(), InspectionID: job.InspectionID.String(), ExecutionID: executionID.String(), Attempt: job.Attempt, ReplayGeneration: job.Generation, Mode: mode, ComparisonMode: "TRANSCRIPTION", ModelAlias: job.Profile.ModelAlias, PromptDigest: job.PromptDigest, AudioDurationMS: job.DurationMS, AudioTokenUnit: "MILLISECONDS"})
	if err == nil {
		a.correlations.Range(func(key, value any) bool {
			if time.Since(value.(ledgerCorrelation).created) > time.Hour {
				a.correlations.Delete(key)
			}
			return true
		})
		key := callID.String()
		a.correlations.Store(key, ledgerCorrelation{value: observability.CorrelationFromContext(ctx), created: time.Now()})
		time.AfterFunc(time.Hour, func() { a.correlations.Delete(key) })
	}
	return callID.String(), err
}

// Finish closes a call record with provider facts and audio duration metadata.
func (a *AttemptLedgerAdapter) Finish(ctx context.Context, callID string, result llm.StructuredResult, outcome string, durationMS int64) error {
	id, err := identity.ParseID(callID)
	if err != nil {
		return err
	}
	corr := observability.CorrelationFromContext(ctx)
	if value, ok := a.correlations.Load(callID); ok {
		corr = value.(ledgerCorrelation).value
	}
	corr.CallID = id.String()
	ctx = observability.WithCorrelation(ctx, corr)
	var audioDuration *int64
	if durationMS > 0 {
		audioDuration = &durationMS
	}
	err = a.Ledger.Finish(ctx, llm.CallFinish{CallID: id.String(), Provider: result.Provider, Model: result.Model, GatewayRequestID: result.GatewayRequestID, GatewayCallID: result.GatewayCallID, TechnicalOutcome: outcome, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens, CachedInputTokens: result.CachedInputTokens, AudioDurationMS: audioDuration, AudioTokenUnit: "MILLISECONDS", ReportedCostDecimal: result.ReportedCostDecimal, CostSource: result.CostSource, TransportDelivered: result.TransportDelivered, HTTPStatus: result.HTTPStatus, Duration: result.Latency, RequestBodyBytes: result.RequestBodyBytes})
	if err == nil {
		a.correlations.Delete(callID)
	}
	return err
}
