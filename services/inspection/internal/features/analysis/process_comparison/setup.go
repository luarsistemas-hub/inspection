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
	"inspection/services/inspection/internal/platform/observability"

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
	Metrics      *observability.Metrics
	Logger       observability.LLMLogger
	Mode         string
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
	if deps.Logger == nil {
		deps.Logger = observability.NoopLLMLogger{}
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
			if recorder := observability.OutcomeRecorderFromContext(ctx); recorder != nil {
				recorder.Set("duplicate")
			}
			return nil
		}
		ctx = observability.EnsureExecutionID(ctx, func() string { return identity.NewID().String() })
		correlation := observability.CorrelationFromContext(ctx)
		correlation.JobID = job.ID.String()
		correlation.InspectionID = job.InspectionID.String()
		correlation.TenantID = tenantID.String()
		ctx = observability.WithCorrelation(ctx, correlation)
		if recorder := observability.OutcomeRecorderFromContext(ctx); recorder != nil {
			recorder.SetCorrelation(correlation)
		}
		requestStarted := deps.Now()
		request, err := deps.BuildRequest(ctx, tx, job, event)
		requestOutcome := "success"
		if err != nil {
			requestOutcome = "error"
		}
		if deps.Metrics != nil {
			deps.Metrics.AnalysisStage("request", requestOutcome, time.Since(requestStarted))
		}
		validationStarted := deps.Now()
		if err == nil {
			_, err = validateRequest(request)
		}
		if deps.Metrics != nil {
			validationOutcome := "success"
			if err != nil {
				validationOutcome = "rejected"
			}
			deps.Metrics.AnalysisStage("validation", validationOutcome, time.Since(validationStarted))
		}
		if err != nil {
			if deps.Metrics != nil {
				deps.Metrics.AnalysisValidation(deps.Mode, request.Mode, "rejected", "request_invalid")
			}
			logAttempt(ctx, deps.Logger, request, llm.StructuredResult{}, "REQUEST_REJECTED", err)
			fallbackErr := retryOrFallback(ctx, tx, tenantID, job, deps.Now(), err)
			if fallbackErr == nil {
				if recorder := observability.OutcomeRecorderFromContext(ctx); recorder != nil {
					recorder.Set("inconclusive")
				}
			}
			return fallbackErr
		}
		started := deps.Now().UTC()
		result, err := deps.Gateway.CompleteStructured(ctx, request)
		if err != nil {
			if deps.Metrics != nil {
				deps.Metrics.AnalysisStage("llm", "error", time.Since(started))
			}
			logAttempt(ctx, deps.Logger, request, result, "PROVIDER_ERROR", err)
			fallbackErr := retryOrFallback(ctx, tx, tenantID, job, deps.Now(), err)
			if fallbackErr == nil {
				if recorder := observability.OutcomeRecorderFromContext(ctx); recorder != nil {
					recorder.Set("inconclusive")
				}
			}
			return fallbackErr
		}
		if deps.Metrics != nil {
			deps.Metrics.AnalysisStage("llm", "success", time.Since(started))
		}
		validationStarted = deps.Now()
		parsed, validationErr := analysis.ParseResult(result.JSON)
		if validationErr == nil {
			validationErr = validateEvidence(parsed, request)
		}
		if validationErr != nil {
			if deps.Metrics != nil {
				deps.Metrics.AnalysisStage("validation", "rejected", time.Since(validationStarted))
				deps.Metrics.AnalysisValidation(deps.Mode, request.Mode, "rejected", "response_invalid")
			}
			logAttempt(ctx, deps.Logger, request, result, "RESPONSE_REJECTED", validationErr)
			fallbackErr := retryOrFallback(ctx, tx, tenantID, job, deps.Now(), validationErr)
			if fallbackErr == nil {
				if recorder := observability.OutcomeRecorderFromContext(ctx); recorder != nil {
					recorder.Set("inconclusive")
				}
			}
			return fallbackErr
		}
		validationOutcome := "accepted"
		status := "COMPLETED"
		if isInsufficientEvidence(parsed) {
			status = "INCONCLUSIVE"
			validationOutcome = "inconclusive"
		}
		if deps.Metrics != nil {
			deps.Metrics.AnalysisStage("validation", validationOutcome, time.Since(validationStarted))
			deps.Metrics.AnalysisValidation(deps.Mode, request.Mode, validationOutcome, "accepted")
		}
		logAttempt(ctx, deps.Logger, request, result, "ACCEPTED", nil)
		persistStarted := deps.Now()
		err = persistAccepted(tx, tenantID, job, parsed, result, started, deps.Now().UTC(), status)
		persistOutcome := "success"
		if err != nil {
			persistOutcome = "error"
		}
		if deps.Metrics != nil {
			deps.Metrics.AnalysisStage("persist", persistOutcome, time.Since(persistStarted))
		}
		if err != nil {
			logAttempt(ctx, deps.Logger, request, result, "PERSISTENCE_REJECTED", err)
			return err
		}
		if recorder := observability.OutcomeRecorderFromContext(ctx); recorder != nil {
			if status == "INCONCLUSIVE" {
				recorder.Set("inconclusive")
			} else {
				recorder.Set("completed")
			}
		}
		return nil
	}, nil
}

func logAttempt(ctx context.Context, logger observability.LLMLogger, request llm.StructuredRequest, result llm.StructuredResult, outcome string, err error) {
	level := "info"
	if err != nil && outcome == "PERSISTENCE_REJECTED" {
		level = "error"
	} else if err != nil {
		level = "warn"
	}
	event := observability.EventFromContext(ctx, "analysis_attempt", level, "analysis", outcome, llm.CodeOf(err))
	event.ComparisonMode = request.Mode
	event.ModelAlias = request.ModelAlias
	event.Provider = result.Provider
	event.Model = result.Model
	event.GatewayRequestID = result.GatewayRequestID
	event.PromptDigest = request.PromptDigest
	event.HTTPStatus = result.HTTPStatus
	event.TransportDelivered = result.TransportDelivered
	event.DurationMS = result.Latency.Milliseconds()
	event.InputTokens = result.InputTokens
	event.OutputTokens = result.OutputTokens
	event.Cost = result.Cost
	event.Images = len(request.Images)
	logger.LogLLM(ctx, event)
}

func validateEvidence(result analysis.Result, request llm.StructuredRequest) error {
	known := make(map[string]struct{}, len(request.Images))
	byID := make(map[string]llm.NormalizedImage, len(request.Images))
	comparative := false
	for _, image := range request.Images {
		known[image.EvidenceID] = struct{}{}
		byID[image.EvidenceID] = image
		comparative = comparative || image.Source == "ORIGIN"
	}
	if len(result.Findings) > 100 {
		return fmt.Errorf("analysis: finding limit exceeded")
	}
	for _, finding := range result.Findings {
		if finding.Category != "EVIDENCE_QUALITY" && int(finding.Confidence*10000) < request.MinimumConfidenceBPS {
			return fmt.Errorf("analysis: finding below minimum confidence")
		}
		if len(finding.EvidenceIDs) > 50 {
			return fmt.Errorf("analysis: evidence limit exceeded")
		}
		seen := make(map[string]struct{}, len(finding.EvidenceIDs))
		pairIDs := make(map[string]struct{})
		hasCurrent, hasOrigin := false, false
		for _, evidenceID := range finding.EvidenceIDs {
			if _, ok := known[evidenceID]; !ok {
				return fmt.Errorf("analysis: unknown evidence reference")
			}
			if _, duplicate := seen[evidenceID]; duplicate {
				return fmt.Errorf("analysis: duplicate evidence reference")
			}
			seen[evidenceID] = struct{}{}
			image := byID[evidenceID]
			hasCurrent = hasCurrent || image.Source == "CURRENT"
			hasOrigin = hasOrigin || image.Source == "ORIGIN"
			if image.PairID != "" {
				pairIDs[image.PairID] = struct{}{}
			}
		}
		if finding.Category != "EVIDENCE_QUALITY" && !hasCurrent {
			return fmt.Errorf("analysis: finding requires current evidence")
		}
		if comparative && finding.ChangeType == "CURRENT_CONDITION" {
			return fmt.Errorf("analysis: current condition cannot be used with origin evidence")
		}
		if finding.ChangeType == "CURRENT_CONDITION" && len(pairIDs) > 0 {
			return fmt.Errorf("analysis: current condition cannot use paired evidence")
		}
		if comparative && finding.Category != "EVIDENCE_QUALITY" && finding.ChangeType != "NOT_APPLICABLE" && (len(pairIDs) == 0 || !hasOrigin) {
			return fmt.Errorf("analysis: comparative finding requires paired origin evidence")
		}
		if finding.ChangeType != "CURRENT_CONDITION" && finding.ChangeType != "NOT_APPLICABLE" && len(pairIDs) > 0 && !hasOrigin {
			return fmt.Errorf("analysis: comparative finding requires origin evidence")
		}
		if len(pairIDs) > 1 {
			return fmt.Errorf("analysis: finding mixes evidence pairs")
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
	if request.ModelAlias == "" || request.PromptDigest == "" || request.SystemPrompt == "" || request.UserPrompt == "" || len(request.JSONSchema) == 0 || len(request.Images) == 0 || request.MinimumConfidenceBPS < 0 || request.MinimumConfidenceBPS > 10000 {
		return llm.StructuredRequest{}, fmt.Errorf("analysis request lacks authorized structured input")
	}
	pairedSources := make(map[string]map[string]bool)
	for _, image := range request.Images {
		if image.EvidenceID == "" || image.Digest == "" || (image.Source != "CURRENT" && image.Source != "ORIGIN") || len(image.DataURL) < len("data:image/") || image.DataURL[:len("data:image/")] != "data:image/" {
			return llm.StructuredRequest{}, fmt.Errorf("analysis request has unauthorized image")
		}
		if (image.PairID == "") != (image.Position == "") {
			return llm.StructuredRequest{}, fmt.Errorf("analysis request has incomplete evidence pairing")
		}
		if image.PairID != "" {
			if pairedSources[image.PairID] == nil {
				pairedSources[image.PairID] = make(map[string]bool)
			}
			pairedSources[image.PairID][image.Source] = true
		}
	}
	for pairID, sources := range pairedSources {
		if !sources["ORIGIN"] || !sources["CURRENT"] {
			return llm.StructuredRequest{}, fmt.Errorf("analysis request has incomplete pair %s", pairID)
		}
	}
	return request, nil
}

func isInsufficientEvidence(result analysis.Result) bool {
	return result.CoverageStatus != "COMPLETE" || result.ComparisonStatus == "INCONCLUSIVE"
}

func persistAccepted(tx *gorm.DB, tenantID identity.ID, job database.ComparisonJob, result analysis.Result, provider llm.StructuredResult, started, now time.Time, status string) error {
	outcome := "ACCEPTED"
	if status == "INCONCLUSIVE" {
		outcome = "INCONCLUSIVE"
	}
	run := database.AnalysisRun{ID: identity.NewID(), TenantID: tenantID, JobID: job.ID, PromptSnapshotID: job.PromptSnapshotID, Provider: provider.Provider, Model: provider.Model, GatewayRequestID: provider.GatewayRequestID, PromptDigest: job.PromptDigest, InputDigest: job.InputDigest, Output: provider.JSON, InputTokens: provider.InputTokens, OutputTokens: provider.OutputTokens, Cost: provider.Cost, LatencyMS: provider.Latency.Milliseconds(), ValidationOutcome: outcome, CreatedAt: now}
	if run.LatencyMS == 0 {
		run.LatencyMS = now.Sub(started).Milliseconds()
	}
	if err := tx.Create(&run).Error; err != nil {
		return err
	}
	if err := tx.Create(&database.UsageRecord{ID: identity.NewID(), TenantID: tenantID, InspectionID: job.InspectionID, JobID: job.ID, PromptSnapshotID: job.PromptSnapshotID, Provider: provider.Provider, Model: provider.Model, PromptDigest: job.PromptDigest, InputTokens: provider.InputTokens, OutputTokens: provider.OutputTokens, Cost: provider.Cost, LatencyMS: run.LatencyMS, CreatedAt: now}).Error; err != nil {
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
		row := database.FindingRecord{ID: identity.NewID(), TenantID: tenantID, AnalysisRunID: run.ID, Category: finding.Category, ChangeType: finding.ChangeType, Title: finding.Title, Description: finding.Description, Severity: finding.Severity, Quality: finding.Quality, RecommendedAction: finding.RecommendedAction, Confidence: finding.Confidence, Evidence: evidence, CreatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	if err := tx.Model(&database.ComparisonJob{}).Where("tenant_id=? AND id=?", tenantID, job.ID).Updates(map[string]any{"status": status, "attempts": job.Attempts + 1, "updated_at": now}).Error; err != nil {
		return err
	}
	job.Status = status
	return emitCompleted(tx, tenantID, job, now)
}

func persistFallback(tx *gorm.DB, tenantID identity.ID, job database.ComparisonJob, now time.Time, cause error) error {
	output, _ := json.Marshal(map[string]any{"coverageStatus": "INSUFFICIENT", "comparisonStatus": "INCONCLUSIVE", "findings": []any{}, "reason": "analysis unavailable"})
	run := database.AnalysisRun{ID: identity.NewID(), TenantID: tenantID, JobID: job.ID, PromptSnapshotID: job.PromptSnapshotID, Provider: "", Model: "", GatewayRequestID: "", PromptDigest: job.PromptDigest, InputDigest: job.InputDigest, Output: output, LatencyMS: 0, ValidationOutcome: "INCONCLUSIVE", CreatedAt: now}
	if err := tx.Create(&run).Error; err != nil {
		return err
	}
	if err := tx.Model(&database.ComparisonJob{}).Where("tenant_id=? AND id=?", tenantID, job.ID).Updates(map[string]any{"status": "INCONCLUSIVE", "attempts": job.Attempts + 1, "updated_at": now}).Error; err != nil {
		return err
	}
	job.Status = "INCONCLUSIVE"
	return emitCompleted(tx, tenantID, job, now)
}

func emitCompleted(tx *gorm.DB, tenantID identity.ID, job database.ComparisonJob, now time.Time) error {
	eventID := identity.NewID()
	return messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "analysis.comparison_completed.v1", SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: job.ID, CorrelationID: "analysis-comparison-" + job.ID.String(), Payload: map[string]any{"inspectionId": job.InspectionID, "jobId": job.ID, "requirementKey": job.RequirementKey, "status": job.Status}})
}
