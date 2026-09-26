// Package record_llm_call persists one operational row for each LLM attempt.
package record_llm_call

import (
	"context"
	"fmt"
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

// Setup returns a durable LLM call ledger. PostgreSQL writes run under the
// same forced-RLS tenant boundary used by the rest of the service.
func Setup(deps Dependencies) (llm.CallLedger, error) {
	if deps.DB == nil {
		return nil, fmt.Errorf("slice usage/record_llm_call: missing database")
	}
	return ledger{db: deps.DB}, nil
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
	inspectionID, err := parseID("inspection", start.InspectionID)
	if err != nil {
		return llm.NewError(llm.CodeLedgerPersistence, 0, err)
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
		Mode: start.Mode, ComparisonMode: start.ComparisonMode, ModelAlias: start.ModelAlias, PromptDigest: start.PromptDigest,
		State: "STARTED", TechnicalOutcome: "pending", StartedAt: startedAt, UpdatedAt: startedAt,
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
		"state":               "FINISHED",
		"technical_outcome":   finish.TechnicalOutcome,
		"transport_delivered": &transportDelivered,
		"http_status":         httpStatus,
		"input_tokens":        finish.InputTokens,
		"output_tokens":       finish.OutputTokens,
		"cached_input_tokens": finish.CachedInputTokens,
		"image_count":         optionalInt(finish.ImageCount),
		"request_body_bytes":  optionalInt64(finish.RequestBodyBytes),
		"reported_cost":       finish.Cost,
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
