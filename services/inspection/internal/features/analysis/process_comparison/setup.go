// Package process_comparison persists one immutable structured-analysis run.
package process_comparison

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	analysis "inspection/services/inspection/internal/features/analysis/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

type Payload struct {
	JobID identity.ID `json:"jobId"`
}
type eventsPayload = Payload
type RequestBuilder func(context.Context, *gorm.DB, database.ComparisonJob, Payload) (llm.StructuredRequest, error)
type Dependencies struct {
	Gateway      llm.Gateway
	BuildRequest RequestBuilder
	Now          func() time.Time
}

// Setup returns an inbox-compatible consumer. BuildRequest is deliberately the
// only seam allowed to load authorized normalized derivatives; queue payloads
// never carry image bytes or storage credentials.
func Setup(deps Dependencies) (func(context.Context, *gorm.DB, []byte, identity.ID) error, error) {
	if deps.Gateway == nil || deps.BuildRequest == nil {
		return nil, fmt.Errorf("slice analysis/process_comparison: missing dependency")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return func(ctx context.Context, tx *gorm.DB, payload []byte, tenantID identity.ID) error {
		var event Payload
		if err := json.Unmarshal(payload, &event); err != nil || event.JobID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		var job database.ComparisonJob
		if err := tx.Where("tenant_id=? AND id=?", tenantID, event.JobID).First(&job).Error; err != nil {
			return err
		}
		if job.Status == "COMPLETED" || job.Status == "INCONCLUSIVE" {
			return nil
		}
		request, err := deps.BuildRequest(ctx, tx, job, event)
		if err == nil {
			_, err = validateRequest(request)
		}
		if err != nil {
			return retryOrFallback(ctx, tx, tenantID, job, deps.Now(), err)
		}
		started := deps.Now().UTC()
		result, err := deps.Gateway.CompleteStructured(ctx, request)
		if err != nil {
			return retryOrFallback(ctx, tx, tenantID, job, deps.Now(), err)
		}
		parsed, validationErr := analysis.ParseResult(result.JSON)
		if validationErr == nil {
			validationErr = validateEvidence(parsed, request)
		}
		if validationErr != nil {
			return retryOrFallback(ctx, tx, tenantID, job, deps.Now(), validationErr)
		}
		return persistAccepted(tx, tenantID, job, parsed, result, started, deps.Now().UTC())
	}, nil
}

func validateEvidence(result analysis.Result, request llm.StructuredRequest) error {
	known := make(map[string]struct{}, len(request.Images))
	for _, image := range request.Images {
		known[image.EvidenceID] = struct{}{}
	}
	if len(result.Findings) > 100 {
		return fmt.Errorf("analysis: finding limit exceeded")
	}
	for _, finding := range result.Findings {
		if len(finding.EvidenceIDs) > 50 {
			return fmt.Errorf("analysis: evidence limit exceeded")
		}
		for _, evidenceID := range finding.EvidenceIDs {
			if _, ok := known[evidenceID]; !ok {
				return fmt.Errorf("analysis: unknown evidence reference")
			}
		}
	}
	return nil
}

func retryOrFallback(ctx context.Context, tx *gorm.DB, tenantID identity.ID, job database.ComparisonJob, now time.Time, cause error) error {
	// Direct slice callers do not have broker metadata and retain the historical
	// deterministic fallback behavior. Rabbit consumers carry the attempt and
	// leave the job retryable until the fourth delivery.
	if messaging.HasAttempt(ctx) && messaging.Attempt(ctx) < analysis.MaxAttempts-1 {
		return cause
	}
	return persistFallback(tx, tenantID, job, now, cause)
}

func validateRequest(request llm.StructuredRequest) (llm.StructuredRequest, error) {
	if request.ModelAlias == "" || request.PromptVersion == "" || len(request.JSONSchema) == 0 || len(request.Images) == 0 {
		return llm.StructuredRequest{}, fmt.Errorf("analysis request lacks authorized structured input")
	}
	for _, image := range request.Images {
		if image.EvidenceID == "" || image.Digest == "" || len(image.DataURL) < len("data:image/") || image.DataURL[:len("data:image/")] != "data:image/" {
			return llm.StructuredRequest{}, fmt.Errorf("analysis request has unauthorized image")
		}
	}
	return request, nil
}

func persistAccepted(tx *gorm.DB, tenantID identity.ID, job database.ComparisonJob, result analysis.Result, provider llm.StructuredResult, started, now time.Time) error {
	run := database.AnalysisRun{ID: identity.NewID(), TenantID: tenantID, JobID: job.ID, Provider: provider.Provider, Model: provider.Model, GatewayRequestID: provider.GatewayRequestID, PromptVersion: job.PromptVersion, InputDigest: job.InputDigest, Output: provider.JSON, InputTokens: provider.InputTokens, OutputTokens: provider.OutputTokens, Cost: provider.Cost, LatencyMS: provider.Latency.Milliseconds(), ValidationOutcome: "ACCEPTED", CreatedAt: now}
	if run.LatencyMS == 0 {
		run.LatencyMS = now.Sub(started).Milliseconds()
	}
	if err := tx.Create(&run).Error; err != nil {
		return err
	}
	if err := tx.Create(&database.UsageRecord{ID: identity.NewID(), TenantID: tenantID, InspectionID: job.InspectionID, JobID: job.ID, Provider: provider.Provider, Model: provider.Model, PromptVersion: job.PromptVersion, InputTokens: provider.InputTokens, OutputTokens: provider.OutputTokens, Cost: provider.Cost, LatencyMS: run.LatencyMS, CreatedAt: now}).Error; err != nil {
		return err
	}
	var daily database.UsageDailySummary
	day := now.UTC().Truncate(24 * time.Hour)
	if err := tx.Where("tenant_id=? AND day=?", tenantID, day).First(&daily).Error; err == gorm.ErrRecordNotFound {
		daily = database.UsageDailySummary{ID: identity.NewID(), TenantID: tenantID, Day: day, Requests: 1, UpdatedAt: now}
		if provider.InputTokens != nil {
			daily.InputTokens = *provider.InputTokens
		}
		if provider.OutputTokens != nil {
			daily.OutputTokens = *provider.OutputTokens
		}
		if provider.Cost != nil {
			daily.Cost = *provider.Cost
		}
		if err := tx.Create(&daily).Error; err != nil {
			return err
		}
	} else if err == nil {
		updates := map[string]any{"requests": gorm.Expr("requests + 1"), "updated_at": now}
		if provider.InputTokens != nil {
			updates["input_tokens"] = gorm.Expr("input_tokens + ?", *provider.InputTokens)
		}
		if provider.OutputTokens != nil {
			updates["output_tokens"] = gorm.Expr("output_tokens + ?", *provider.OutputTokens)
		}
		if provider.Cost != nil {
			updates["cost"] = gorm.Expr("cost + ?", *provider.Cost)
		}
		if err := tx.Model(&database.UsageDailySummary{}).Where("tenant_id=? AND day=?", tenantID, day).Updates(updates).Error; err != nil {
			return err
		}
	} else {
		return err
	}
	for _, finding := range result.Findings {
		evidence, _ := json.Marshal(finding.EvidenceIDs)
		row := database.FindingRecord{ID: identity.NewID(), TenantID: tenantID, AnalysisRunID: run.ID, Category: finding.Category, Title: finding.Title, Description: finding.Description, Severity: finding.Severity, Quality: finding.Quality, RecommendedAction: finding.RecommendedAction, Confidence: finding.Confidence, Evidence: evidence, CreatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	if err := tx.Model(&database.ComparisonJob{}).Where("tenant_id=? AND id=?", tenantID, job.ID).Updates(map[string]any{"status": "COMPLETED", "attempts": job.Attempts + 1, "updated_at": now}).Error; err != nil {
		return err
	}
	return emitCompleted(tx, tenantID, job, now)
}

func persistFallback(tx *gorm.DB, tenantID identity.ID, job database.ComparisonJob, now time.Time, cause error) error {
	output, _ := json.Marshal(map[string]any{"inconclusive": true, "reason": "analysis unavailable"})
	run := database.AnalysisRun{ID: identity.NewID(), TenantID: tenantID, JobID: job.ID, Provider: "", Model: "", GatewayRequestID: "", PromptVersion: job.PromptVersion, InputDigest: job.InputDigest, Output: output, LatencyMS: 0, ValidationOutcome: "INCONCLUSIVE", CreatedAt: now}
	if err := tx.Create(&run).Error; err != nil {
		return err
	}
	if err := tx.Model(&database.ComparisonJob{}).Where("tenant_id=? AND id=?", tenantID, job.ID).Updates(map[string]any{"status": "INCONCLUSIVE", "attempts": job.Attempts + 1, "updated_at": now}).Error; err != nil {
		return err
	}
	return emitCompleted(tx, tenantID, job, now)
}

func emitCompleted(tx *gorm.DB, tenantID identity.ID, job database.ComparisonJob, now time.Time) error {
	eventID := identity.NewID()
	return messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "analysis.comparison_completed.v1", SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: job.ID, CorrelationID: "analysis-comparison-" + job.ID.String(), Payload: map[string]any{"inspectionId": job.InspectionID, "jobId": job.ID, "requirementKey": job.RequirementKey, "status": job.Status}})
}
