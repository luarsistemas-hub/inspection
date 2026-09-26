// Package llm defines the provider-neutral structured-analysis boundary.
package llm

import (
	"context"
	"errors"
	"time"
)

// StructuredRequest may contain only normalized, already-authorized image data.
// Model is a logical alias; provider/model resolution remains adapter metadata.
type StructuredRequest struct {
	ModelAlias, PromptDigest, Mode string
	SystemPrompt, UserPrompt       string
	JSONSchema                     []byte
	MinimumConfidenceBPS           int
	Images                         []NormalizedImage
}

// NormalizedImage carries explicit lineage so comparative prompts cannot
// silently mix current evidence with the pinned origin snapshot. PairID and
// Position are populated for comparative evidence so the adapter can preserve
// the relationship between an origin image and its current counterpart.
type NormalizedImage struct {
	EvidenceID string
	Source     string
	PairID     string
	Position   string
	DataURL    string
	Digest     string
}

// StructuredResult records actual provider facts, never estimated usage.
type StructuredResult struct {
	JSON                              []byte
	GatewayRequestID, Provider, Model string
	CallID                            string
	InputTokens, OutputTokens         *int64
	CachedInputTokens                 *int64
	RequestBodyBytes                  int64
	InvalidCachedInputTokens          bool
	Cost                              *float64
	Latency                           time.Duration
	HTTPStatus                        int
	TransportDelivered                bool
}

type Gateway interface {
	CompleteStructured(context.Context, StructuredRequest) (StructuredResult, error)
}

// CallStart contains the safe correlation and request metadata persisted before
// a provider transport is attempted.
type CallStart struct {
	CallID, TenantID, EventID, CorrelationID string
	JobID, InspectionID, ExecutionID         string
	Attempt, ReplayGeneration                int
	Mode, ComparisonMode, ModelAlias         string
	PromptDigest                             string
	StartedAt                                time.Time
}

// CallFinish contains provider metadata available after a transport attempt.
// Nil token and cost values remain unknown rather than being estimated.
type CallFinish struct {
	CallID, Provider, Model, GatewayRequestID, TechnicalOutcome string
	InputTokens, OutputTokens                                   *int64
	CachedInputTokens                                           *int64
	ImageCount                                                  int
	RequestBodyBytes                                            int64
	Cost                                                        *float64
	TransportDelivered                                          bool
	HTTPStatus                                                  int
	Duration                                                    time.Duration
	FinishedAt                                                  time.Time
}

// CallLedger persists one operational row for each gateway invocation.
type CallLedger interface {
	Start(context.Context, CallStart) error
	Finish(context.Context, CallFinish) error
}

// NoopCallLedger disables durable call recording for tests or non-worker
// callers without changing the provider-neutral gateway contract.
type NoopCallLedger struct{}

func (NoopCallLedger) Start(context.Context, CallStart) error   { return nil }
func (NoopCallLedger) Finish(context.Context, CallFinish) error { return nil }

var errMissingGateway = errors.New("missing gateway")

type ErrorCode string

const (
	CodeInvalidInput      ErrorCode = "invalid_input"
	CodeTimeout           ErrorCode = "timeout"
	CodeCancelled         ErrorCode = "cancelled"
	CodeTransport         ErrorCode = "transport"
	CodeAuthentication    ErrorCode = "authentication"
	CodeRateLimit         ErrorCode = "rate_limit"
	CodeProviderHTTP      ErrorCode = "provider_http"
	CodeMalformedResponse ErrorCode = "malformed_response"
	CodeLedgerPersistence ErrorCode = "ledger_persistence"
)

type Error struct {
	Code       ErrorCode
	HTTPStatus int
	Err        error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return "llm: " + string(e.Code)
	}
	return "llm: " + string(e.Code) + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

func (e *Error) ErrorCode() string { return string(e.Code) }

func NewError(code ErrorCode, status int, err error) error {
	if err == nil && code == "" {
		return nil
	}
	return &Error{Code: code, HTTPStatus: status, Err: err}
}

func CodeOf(err error) string {
	if err == nil {
		return ""
	}
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		return coded.ErrorCode()
	}
	return "internal_error"
}
