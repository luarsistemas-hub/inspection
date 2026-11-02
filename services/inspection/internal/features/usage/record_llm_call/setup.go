// Package record_llm_call persists one operational row for each LLM attempt.
package record_llm_call

import (
	"context"
	"fmt"
	"math"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/observability"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// Dependencies are the database dependencies of the ledger slice.
type Dependencies struct {
	DB *gorm.DB
}

// Ledger exposes standard attempt recording and one-time late cost reconciliation.
type Ledger interface {
	llm.CallLedger
	ReportLateCost(context.Context, LateCost) error
}

// LateCost carries provider cost metadata that arrived after the call was finalized.
type LateCost struct {
	TenantID, CallID string
	Cost             float64
	Source           string
	ReportedAt       time.Time
}

// Setup returns a durable LLM call ledger. PostgreSQL writes run under the
// same forced-RLS tenant boundary used by the rest of the service.
func Setup(deps Dependencies) (Ledger, error) {
	if deps.DB == nil {
		return nil, fmt.Errorf("slice usage/record_llm_call: missing database")
	}
	return ledger{db: deps.DB}, nil
}

// ReportLateCost fills unknown cost provenance once without changing attempt outcome.
func (l ledger) ReportLateCost(ctx context.Context, report LateCost) error {
	tenantID, err := parseID("tenant", report.TenantID)
	if err != nil {
		return llm.NewError(llm.CodeLedgerPersistence, 0, err)
	}
	callID, err := parseID("call", report.CallID)
	if err != nil || report.Cost < 0 || math.IsNaN(report.Cost) || math.IsInf(report.Cost, 0) || (report.Source != "header" && report.Source != "body") {
		return llm.NewError(llm.CodeLedgerPersistence, 0, fmt.Errorf("invalid late cost report"))
	}
	updatedAt := report.ReportedAt.UTC()
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	err = l.withTenant(ctx, tenantID, func(tx *gorm.DB) error {
		result := tx.Model(&database.LLMCallRecord{}).
			Where("tenant_id=? AND call_id=? AND state=? AND reported_cost IS NULL AND cost_source=?", tenantID, callID, "FINISHED", "unknown").
			Updates(map[string]any{"reported_cost": report.Cost, "cost_source": report.Source, "updated_at": updatedAt})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("late cost already reported or call not eligible")
		}
		return nil
	})
	if err != nil {
		return llm.NewError(llm.CodeLedgerPersistence, 0, err)
	}
	return nil
}

type ledger struct{ db *gorm.DB }

func (l ledger) Start(ctx context.Context, start llm.CallStart) error {
	tenantID, err := parseID("tenant", start.TenantID)
	if err != nil {
		return llm.NewError(llm.CodeLedgerPersistence, 0, err)
	}
	callID, err := parseID("call", start.CallID)
	if err != nil {
		return llm.NewError(llm.CodeLedgerPersistence, 0, err)
	}
	eventID, err := parseID("event", start.EventID)
	if err != nil {
		return llm.NewError(llm.CodeLedgerPersistence, 0, err)
	}
	jobID, err := parseID("job", start.JobID)
	if err != nil {
		return llm.NewError(llm.CodeLedgerPersistence, 0, err)
	}
	var inspectionID *identity.ID
	if start.InspectionID != "" {
		parsed, parseErr := parseID("inspection", start.InspectionID)
		if parseErr != nil {
			return llm.NewError(llm.CodeLedgerPersistence, 0, parseErr)
		}
		inspectionID = &parsed
	} else if start.ComparisonMode != "VIDEO_SCOPE" {
		return llm.NewError(llm.CodeLedgerPersistence, 0, fmt.Errorf("missing inspection identifier"))
	}
	executionID, err := parseID("execution", start.ExecutionID)
	if err != nil {
		return llm.NewError(llm.CodeLedgerPersistence, 0, err)
	}
	if start.Mode != "mock" && start.Mode != "live" {
		return llm.NewError(llm.CodeLedgerPersistence, 0, fmt.Errorf("invalid LLM mode"))
	}
	startedAt := start.StartedAt.UTC()
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	row := database.LLMCallRecord{
		CallID: callID, TenantID: tenantID, InspectionID: inspectionID, JobID: jobID, EventID: eventID, ExecutionID: executionID,
		CorrelationID: start.CorrelationID, Attempt: start.Attempt, ReplayGeneration: start.ReplayGeneration,
		Mode: start.Mode, ComparisonMode: start.ComparisonMode, ModelAlias: start.ModelAlias, PromptDigest: start.PromptDigest, AudioDurationMS: optionalAudioDurationMS(start.AudioDurationMS),
		AudioTokenUnit: optionalTextPointer(start.AudioTokenUnit),
		State:          "STARTED", TechnicalOutcome: "pending", StartedAt: startedAt, UpdatedAt: startedAt,
	}
	err = l.withTenant(ctx, tenantID, func(tx *gorm.DB) error { return tx.Create(&row).Error })
	if err != nil {
		return llm.NewError(llm.CodeLedgerPersistence, 0, err)
	}
	return nil
}

func (l ledger) Finish(ctx context.Context, finish llm.CallFinish) error {
	callID, err := parseID("call", finish.CallID)
	if err != nil {
		return llm.NewError(llm.CodeLedgerPersistence, 0, err)
	}
	correlation := observability.CorrelationFromContext(ctx)
	tenantID, err := parseID("tenant", correlation.TenantID)
	if err != nil {
		return llm.NewError(llm.CodeLedgerPersistence, 0, err)
	}
	finishedAt := finish.FinishedAt.UTC()
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}
	transportDelivered := finish.TransportDelivered
	httpStatus, durationMS := optionalInt(finish.HTTPStatus), optionalInt64(finish.Duration.Milliseconds())
	updates := map[string]any{
		"provider":            finish.Provider,
		"model":               finish.Model,
		"gateway_request_id":  finish.GatewayRequestID,
		"gateway_call_id":     finish.GatewayCallID,
		"cost_source":         nullableText(finish.CostSource),
		"state":               "FINISHED",
		"technical_outcome":   finish.TechnicalOutcome,
		"transport_delivered": &transportDelivered,
		"http_status":         httpStatus,
		"input_tokens":        finish.InputTokens,
		"output_tokens":       finish.OutputTokens,
		"cached_input_tokens": finish.CachedInputTokens,
		"audio_duration_ms":   finish.AudioDurationMS,
		"audio_input_tokens":  finish.AudioInputTokens,
		"text_input_tokens":   finish.TextInputTokens,
		"reasoning_tokens":    finish.ReasoningTokens,
		"audio_token_unit":    nullableText(finish.AudioTokenUnit),
		"image_count":         optionalInt(finish.ImageCount),
		"request_body_bytes":  optionalInt64(finish.RequestBodyBytes),
		"reported_cost":       finish.ReportedCostDecimal,
		"duration_ms":         durationMS,
		"finished_at":         finishedAt,
		"updated_at":          finishedAt,
	}
	err = l.withTenant(ctx, tenantID, func(tx *gorm.DB) error {
		result := tx.Model(&database.LLMCallRecord{}).
			Where("tenant_id=? AND call_id=? AND state=?", tenantID, callID, "STARTED").
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("LLM call is missing or already finished")
		}
		return nil
	})
	if err != nil {
		return llm.NewError(llm.CodeLedgerPersistence, 0, err)
	}
	return nil
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (l ledger) withTenant(ctx context.Context, tenantID identity.ID, fn func(*gorm.DB) error) error {
	if l.db.Dialector.Name() == "postgres" {
		return (tenanttx.Runner{DB: l.db}).Within(ctx, tenantID, fn)
	}
	return fn(l.db.WithContext(ctx).Where("tenant_id=?", tenantID))
}

func parseID(name, value string) (identity.ID, error) {
	if value == "" {
		return identity.ID{}, fmt.Errorf("missing %s identifier", name)
	}
	id, err := identity.ParseID(value)
	if err != nil {
		return identity.ID{}, fmt.Errorf("invalid %s identifier", name)
	}
	return id, nil
}

func optionalInt(value int) *int {
	if value <= 0 {
		return nil
	}
	return &value
}

func optionalInt64(value int64) *int64 {
	if value < 0 {
		return nil
	}
	return &value
}

// optionalAudioDurationMS stores only positive source audio durations; zero means unknown.
func optionalAudioDurationMS(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}

func optionalTextPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
