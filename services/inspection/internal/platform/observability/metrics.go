package observability

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"inspection/libs/identity"
)

// LogDeliveryTransition records only identifiers and normalized operational
// fields; destinations, tokens and rendered content are intentionally absent.
func LogDeliveryTransition(ctx context.Context, tenantID, deliveryID, inspectionID identity.ID, from, to, code, provider string, attempt int) {
	slog.InfoContext(ctx, "notification_delivery_transition", "tenantId", tenantID.String(), "deliveryId", deliveryID.String(), "inspectionId", inspectionID.String(), "from", safeCode(from), "to", safeCode(to), "failureCode", safeCode(code), "provider", safeValue(provider), "attempt", attempt)
}

// LogResponsibleEmailCorrected records a redacted correction audit signal.
func LogResponsibleEmailCorrected(ctx context.Context, tenantID, inspectionID, invitationID identity.ID, source string, version int64) {
	slog.InfoContext(ctx, "responsible_email_corrected", "tenantId", tenantID.String(), "inspectionId", inspectionID.String(), "invitationId", invitationID.String(), "source", safeCode(source), "responsibilityVersion", version)
}

// LogResponsibleAccessConfirmed records the first successful responsible OTP access.
func LogResponsibleAccessConfirmed(ctx context.Context, tenantID, inspectionID, responsibilityID identity.ID, version int64) {
	slog.InfoContext(ctx, "responsible_access_confirmed", "tenantId", tenantID.String(), "inspectionId", inspectionID.String(), "responsibilityId", responsibilityID.String(), "responsibilityVersion", version)
}

// LogOwnerDeliveryAlertRequested records an alert request without its email.
func LogOwnerDeliveryAlertRequested(ctx context.Context, tenantID, eventID, inspectionID identity.ID, state string) {
	slog.InfoContext(ctx, "owner_delivery_alert_requested", "tenantId", tenantID.String(), "eventId", eventID.String(), "inspectionId", inspectionID.String(), "state", safeCode(state))
}

// Metrics is a small dependency-free Prometheus collector for operational
// notification signals. Labels are constrained to provider/channel values so
// a destination, tenant, or provider response can never become a label.
type Metrics struct {
	mu         sync.RWMutex
	counters   map[string]map[string]float64
	gauges     map[string]map[string]float64
	hist       map[string]histogram
	histograms map[string]map[string]labeledHistogram
	metricMeta map[string]metricMetadata
}

type histogram struct {
	Count float64
	Sum   float64
}

type labeledHistogram struct {
	Count   float64
	Sum     float64
	Buckets map[float64]float64
}

type metricMetadata struct {
	typeName string
	help     string
}

var llmDurationBuckets = []float64{0.1, 0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300}
var httpRequestDurationBuckets = []float64{0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// NewMetrics returns an empty collector.
func NewMetrics() *Metrics {
	return &Metrics{
		counters:   make(map[string]map[string]float64),
		gauges:     make(map[string]map[string]float64),
		hist:       make(map[string]histogram),
		histograms: make(map[string]map[string]labeledHistogram),
		metricMeta: map[string]metricMetadata{
			"inspection_http_request_duration_seconds":          {typeName: "histogram", help: "HTTP request duration by bounded route and response status class."},
			"inspection_llm_calls_total":                        {typeName: "counter", help: "LLM gateway calls delivered to the configured transport."},
			"inspection_llm_call_duration_seconds":              {typeName: "histogram", help: "Duration of LLM gateway calls in seconds."},
			"inspection_llm_inflight":                           {typeName: "gauge", help: "LLM gateway calls currently in flight."},
			"inspection_llm_tokens_total":                       {typeName: "counter", help: "Provider reported LLM tokens."},
			"inspection_llm_cached_input_tokens_total":          {typeName: "counter", help: "Provider reported input tokens served from cache."},
			"inspection_llm_cache_hit_calls_total":              {typeName: "counter", help: "LLM calls with at least one provider reported cached input token."},
			"inspection_llm_cache_usage_missing_total":          {typeName: "counter", help: "Delivered LLM calls without cached input token metadata."},
			"inspection_llm_images_total":                       {typeName: "counter", help: "Images sent to the LLM gateway."},
			"inspection_llm_request_body_bytes_total":           {typeName: "counter", help: "Serialized request body bytes sent to the LLM gateway."},
			"inspection_llm_usage_missing_total":                {typeName: "counter", help: "LLM calls without provider usage metadata."},
			"inspection_llm_ledger_writes_total":                {typeName: "counter", help: "Durable LLM call ledger write outcomes."},
			"inspection_analysis_validation_total":              {typeName: "counter", help: "Structured analysis validation outcomes."},
			"inspection_analysis_stage_duration_seconds":        {typeName: "histogram", help: "Analysis stage duration in seconds."},
			"inspection_analysis_processing_total":              {typeName: "counter", help: "Analysis processing outcomes after the transaction boundary."},
			"inspection_analysis_delivery_actions_total":        {typeName: "counter", help: "RabbitMQ retry and dead letter delivery actions."},
			"inspection_report_pdf_jobs_total":                  {typeName: "counter", help: "Report PDF job outcomes."},
			"inspection_report_pdf_active":                      {typeName: "gauge", help: "Report PDF jobs currently processing in this worker."},
			"inspection_report_pdf_pending":                     {typeName: "gauge", help: "Report PDF jobs waiting for processing or retry."},
			"inspection_report_pdf_stage_duration_seconds":      {typeName: "histogram", help: "Report PDF processing time by stage."},
			"inspection_report_pdf_pages":                       {typeName: "histogram", help: "Pages in completed report PDFs."},
			"inspection_report_pdf_bytes":                       {typeName: "histogram", help: "Bytes in completed report PDFs."},
			"inspection_report_pdf_expired_leases_total":        {typeName: "counter", help: "Report PDF reservations recovered after lease expiry."},
			"inspection_video_preparations_total":               {typeName: "counter", help: "Video preparation attempts by stage and terminal outcome."},
			"inspection_video_preparation_attempts_total":       {typeName: "counter", help: "Video preparation attempts by stage."},
			"inspection_video_decoded_seconds_total":            {typeName: "counter", help: "Video duration decoded in seconds."},
			"inspection_video_selected_frames_total":            {typeName: "counter", help: "Frames selected and retained from video sources."},
			"inspection_video_selected_bytes_total":             {typeName: "counter", help: "Bytes selected and retained for video evidence."},
			"inspection_video_lease_losses_total":               {typeName: "counter", help: "Video preparation and plan leases lost before completion."},
			"inspection_video_gateway_requests_total":           {typeName: "counter", help: "Video gateway calls by bounded purpose and outcome."},
			"inspection_video_gateway_tokens_total":             {typeName: "counter", help: "Provider-reported video gateway tokens by bounded purpose and direction."},
			"inspection_video_expiration_lag_seconds":           {typeName: "gauge", help: "Current overdue source expiration lag in seconds."},
			"inspection_video_partial_deletions_total":          {typeName: "counter", help: "Video source deletions requiring retry after partial failure."},
			"inspection_video_scope_discoveries_total":          {typeName: "counter", help: "Discovered video scope components and environments by result."},
			"inspection_video_plan_items_total":                 {typeName: "counter", help: "Sealed video plan items by bounded outcome."},
			"inspection_video_unprocessed_candidates_total":     {typeName: "counter", help: "Video plan candidates left unprocessed at seal."},
			"inspection_video_provider_config_failures_total":   {typeName: "counter", help: "Terminal video provider configuration failures."},
			"inspection_video_work_heartbeat_timestamp_seconds": {typeName: "gauge", help: "Unix timestamp of the last video work heartbeat."},
			"inspection_video_queue_depth":                      {typeName: "gauge", help: "Runnable video work by stage and status."},
			"inspection_video_oldest_runnable_age_seconds":      {typeName: "gauge", help: "Age of the oldest runnable video job by stage."},
		},
	}
}

// ObserveHTTPRequest records latency with bounded route and status labels.
func (m *Metrics) ObserveHTTPRequest(route, statusClass string, duration time.Duration) {
	if route != "graphql" && route != "media" && route != "health" {
		route = "other"
	}
	if statusClass != "2xx" && statusClass != "3xx" && statusClass != "4xx" && statusClass != "5xx" {
		statusClass = "5xx"
	}
	m.observeHistogram("inspection_http_request_duration_seconds", map[string]string{"route": route, "status_class": statusClass}, duration.Seconds(), httpRequestDurationBuckets)
}

// VideoPreparation records bounded preparation stages and outcomes.
func (m *Metrics) VideoPreparation(stage, outcome string) {
	m.counter("inspection_video_preparations_total", map[string]string{"stage": safeVideoStage(stage), "status": safeVideoStatus(outcome)}, 1)
}

// VideoPreparationAttempt records a worker attempt without exposing job identifiers.
func (m *Metrics) VideoPreparationAttempt(stage string) {
	m.counter("inspection_video_preparation_attempts_total", map[string]string{"stage": safeVideoStage(stage)}, 1)
}

// VideoSelected records bounded resource totals from a completed source.
func (m *Metrics) VideoSelected(decodedSeconds float64, frames int, bytes int64) {
	if decodedSeconds >= 0 {
		m.counter("inspection_video_decoded_seconds_total", nil, decodedSeconds)
	}
	if frames >= 0 {
		m.counter("inspection_video_selected_frames_total", nil, float64(frames))
	}
	if bytes >= 0 {
		m.counter("inspection_video_selected_bytes_total", nil, float64(bytes))
	}
}

// VideoLeaseLost records a fenced preparation or plan attempt.
func (m *Metrics) VideoLeaseLost(stage string) {
	m.counter("inspection_video_lease_losses_total", map[string]string{"stage": safeVideoStage(stage)}, 1)
}

// VideoGateway records bounded provider usage for a video purpose.
func (m *Metrics) VideoGateway(purpose, outcome string, inputTokens, outputTokens *int64) {
	purpose = safeVideoPurpose(purpose)
	m.counter("inspection_video_gateway_requests_total", map[string]string{"purpose": purpose, "status": safeVideoStatus(outcome)}, 1)
	if inputTokens != nil {
		m.counter("inspection_video_gateway_tokens_total", map[string]string{"purpose": purpose, "direction": "input"}, float64(*inputTokens))
	}
	if outputTokens != nil {
		m.counter("inspection_video_gateway_tokens_total", map[string]string{"purpose": purpose, "direction": "output"}, float64(*outputTokens))
	}
}

// VideoQueue publishes bounded database aggregate observations.
func (m *Metrics) VideoQueue(stage, status string, depth int64, oldestAge time.Duration) {
	labels := map[string]string{"stage": safeVideoStage(stage), "status": safeVideoStatus(status)}
	m.gauge("inspection_video_queue_depth", labels, float64(max(depth, 0)))
	m.gauge("inspection_video_oldest_runnable_age_seconds", labels, max(oldestAge.Seconds(), 0))
}

// VideoExpirationLag records the highest observed overdue source delay.
func (m *Metrics) VideoExpirationLag(seconds float64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gauges["inspection_video_expiration_lag_seconds"] == nil {
		m.gauges["inspection_video_expiration_lag_seconds"] = make(map[string]float64)
	}
	key := labelKey(nil)
	m.gauges["inspection_video_expiration_lag_seconds"][key] = max(m.gauges["inspection_video_expiration_lag_seconds"][key], max(seconds, 0))
}

// ResetVideoExpirationLag starts a fresh scheduler-wide sweep observation.
func (m *Metrics) ResetVideoExpirationLag() {
	m.gauge("inspection_video_expiration_lag_seconds", nil, 0)
}

// VideoPartialDeletion records an incomplete object deletion that must be retried.
func (m *Metrics) VideoPartialDeletion() {
	m.counter("inspection_video_partial_deletions_total", nil, 1)
}

// VideoScope records discovered components or environments when a preparation is sealed.
func (m *Metrics) VideoScope(resource, result string, count int) {
	if resource != "components" && resource != "environments" {
		resource = "other"
	}
	if result != "success" && result != "empty" && result != "limited" {
		result = "other"
	}
	m.counter("inspection_video_scope_discoveries_total", map[string]string{"stage": "scope", "resource": resource, "result": result}, float64(max(count, 0)))
}

// VideoPlanSeal records bounded outcomes and technically unprocessed candidates.
func (m *Metrics) VideoPlanSeal(outcomes map[string]int) {
	for outcome, count := range outcomes {
		if count <= 0 {
			continue
		}
		switch outcome {
		case "MATCHED", "CURRENT_ONLY", "INCONCLUSIVE", "TECHNICAL_FAILURE":
		default:
			outcome = "other"
		}
		m.counter("inspection_video_plan_items_total", map[string]string{"outcome": strings.ToLower(outcome)}, float64(count))
		if outcome == "TECHNICAL_FAILURE" {
			m.counter("inspection_video_unprocessed_candidates_total", nil, float64(count))
		}
	}
}

// VideoProviderConfigFailure records terminal authentication/configuration errors only.
func (m *Metrics) VideoProviderConfigFailure(purpose string) {
	m.counter("inspection_video_provider_config_failures_total", map[string]string{"purpose": safeVideoPurpose(purpose)}, 1)
}

// VideoWorkHeartbeat records the time at which the worker last started work.
func (m *Metrics) VideoWorkHeartbeat(at time.Time) {
	if !at.IsZero() {
		m.gauge("inspection_video_work_heartbeat_timestamp_seconds", nil, float64(at.Unix()))
	}
}

func safeVideoStage(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "download", "probe", "screening", "decode", "scope", "select", "persist", "plan", "expire", "delete", "received", "extracting", "discovering", "ready":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "other"
	}
}

func safeVideoStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "queued", "running", "ready", "failed", "retry", "matched", "inconclusive", "unprocessed", "success", "error":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "other"
	}
}

func safeVideoPurpose(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "scope", "matching", "verification":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "other"
	}
}

// ReportPDFAcquire adjusts the in-process renderer concurrency gauge.
func (m *Metrics) ReportPDFAcquire(delta int) {
	m.gaugeDelta("inspection_report_pdf_active", nil, float64(delta))
}

// ReportPDFQueue records database-wide work still awaiting completion.
func (m *Metrics) ReportPDFQueue(pending int64) {
	m.gauge("inspection_report_pdf_pending", nil, float64(pending))
}

// ReportPDFStage records bounded internal stage names only.
func (m *Metrics) ReportPDFStage(stage string, duration time.Duration) {
	allowed := map[string]bool{"images": true, "render": true, "upload": true, "commit": true}
	if !allowed[stage] {
		stage = "other"
	}
	m.observeHistogram("inspection_report_pdf_stage_duration_seconds", map[string]string{"stage": stage}, duration.Seconds(), []float64{.1, .5, 1, 2, 5, 10, 30, 60, 120})
}

// ReportPDFOutcome records retry and terminal outcomes without tenant labels.
func (m *Metrics) ReportPDFOutcome(audience, outcome string, pages int, byteCount int64) {
	if audience != "PDF" && audience != "PDF_CUSTOMER" {
		audience = "other"
	}
	if outcome != "READY" && outcome != "RETRY" && outcome != "FAILED" && outcome != "CANCELED" {
		outcome = "other"
	}
	labels := map[string]string{"audience": strings.ToLower(audience), "outcome": strings.ToLower(outcome)}
	m.counter("inspection_report_pdf_jobs_total", labels, 1)
	if outcome == "READY" {
		m.observeHistogram("inspection_report_pdf_pages", map[string]string{"audience": strings.ToLower(audience)}, float64(pages), []float64{1, 2, 5, 10, 20, 50, 100, 200})
		m.observeHistogram("inspection_report_pdf_bytes", map[string]string{"audience": strings.ToLower(audience)}, float64(byteCount), []float64{1 << 20, 5 << 20, 10 << 20, 20 << 20, 40 << 20, 64 << 20})
	}
}

// ReportPDFLeaseExpired records a recovered reservation.
func (m *Metrics) ReportPDFLeaseExpired() {
	m.counter("inspection_report_pdf_expired_leases_total", nil, 1)
}

func (m *Metrics) counter(name string, labels map[string]string, value float64) {
	if m == nil || value == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counters[name] == nil {
		m.counters[name] = make(map[string]float64)
	}
	m.counters[name][labelKey(labels)] += value
}

func (m *Metrics) gauge(name string, labels map[string]string, value float64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gauges[name] == nil {
		m.gauges[name] = make(map[string]float64)
	}
	m.gauges[name][labelKey(labels)] = value
}

func (m *Metrics) gaugeDelta(name string, labels map[string]string, delta float64) {
	if m == nil || delta == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gauges[name] == nil {
		m.gauges[name] = make(map[string]float64)
	}
	key := labelKey(labels)
	m.gauges[name][key] += delta
	if m.gauges[name][key] < 0 {
		m.gauges[name][key] = 0
	}
}

func (m *Metrics) observe(name string, seconds float64) {
	if m == nil || seconds < 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	value := m.hist[name]
	value.Count++
	value.Sum += seconds
	m.hist[name] = value
}

func (m *Metrics) observeHistogram(name string, labels map[string]string, seconds float64, buckets []float64) {
	if m == nil || seconds < 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.histograms[name] == nil {
		m.histograms[name] = make(map[string]labeledHistogram)
	}
	key := labelKey(labels)
	value := m.histograms[name][key]
	if value.Buckets == nil {
		value.Buckets = make(map[float64]float64, len(buckets))
	}
	value.Count++
	value.Sum += seconds
	for _, bound := range buckets {
		if _, ok := value.Buckets[bound]; !ok {
			value.Buckets[bound] = 0
		}
		if seconds <= bound {
			value.Buckets[bound]++
		}
	}
	m.histograms[name][key] = value
}

// LLMCallStarted records a transport invocation that has begun.
func (m *Metrics) LLMCallStarted(mode, modelAlias string) {
	m.gaugeDelta("inspection_llm_inflight", llmLabels(mode, "", modelAlias), 1)
}

// LLMLedgerWrite records whether durable call metadata was written.
func (m *Metrics) LLMLedgerWrite(phase, result string) {
	phase = strings.ToLower(strings.TrimSpace(phase))
	if phase != "start" && phase != "finish" {
		phase = "unknown"
	}
	result = strings.ToLower(strings.TrimSpace(result))
	if result != "success" && result != "error" {
		result = "unknown"
	}
	m.counter("inspection_llm_ledger_writes_total", map[string]string{"phase": phase, "result": result}, 1)
}

// LLMCallFinished records a transport invocation, including calls that return
// an error or a response later rejected by the analysis validator.
func (m *Metrics) LLMCallFinished(mode, comparisonMode, modelAlias, outcome string, duration time.Duration, transportDelivered bool, inputTokens, outputTokens, cachedInputTokens *int64, imageCount int, requestBodyBytes int64) {
	m.gaugeDelta("inspection_llm_inflight", llmLabels(mode, "", modelAlias), -1)
	labels := map[string]string{"mode": safeLLMMode(mode), "model_alias": safeModelAlias(modelAlias)}
	if imageCount >= 0 {
		m.counter("inspection_llm_images_total", labels, float64(imageCount))
	}
	if requestBodyBytes >= 0 {
		m.counter("inspection_llm_request_body_bytes_total", labels, float64(requestBodyBytes))
	}
	if !transportDelivered {
		return
	}
	labels = llmLabels(mode, comparisonMode, modelAlias)
	labels["outcome"] = safeLLMOutcome(outcome)
	m.counter("inspection_llm_calls_total", labels, 1)
	m.observeHistogram("inspection_llm_call_duration_seconds", labels, duration.Seconds(), llmDurationBuckets)
	if inputTokens != nil {
		m.counter("inspection_llm_tokens_total", map[string]string{"mode": safeLLMMode(mode), "model_alias": safeModelAlias(modelAlias), "direction": "input"}, float64(*inputTokens))
	}
	if outputTokens != nil {
		m.counter("inspection_llm_tokens_total", map[string]string{"mode": safeLLMMode(mode), "model_alias": safeModelAlias(modelAlias), "direction": "output"}, float64(*outputTokens))
	}
	if cachedInputTokens != nil {
		m.counter("inspection_llm_cached_input_tokens_total", map[string]string{"mode": safeLLMMode(mode), "model_alias": safeModelAlias(modelAlias)}, float64(*cachedInputTokens))
		if *cachedInputTokens > 0 {
			m.counter("inspection_llm_cache_hit_calls_total", map[string]string{"mode": safeLLMMode(mode), "model_alias": safeModelAlias(modelAlias)}, 1)
		}
	} else {
		m.counter("inspection_llm_cache_usage_missing_total", map[string]string{"mode": safeLLMMode(mode), "model_alias": safeModelAlias(modelAlias)}, 1)
	}
	missing := ""
	if inputTokens == nil {
		missing = "input"
	}
	if outputTokens == nil {
		if missing != "" {
			missing = "both"
		} else {
			missing = "output"
		}
	}
	if missing != "" {
		m.counter("inspection_llm_usage_missing_total", map[string]string{"mode": safeLLMMode(mode), "model_alias": safeModelAlias(modelAlias), "direction": missing}, 1)
	}
}

// AnalysisValidation records the post-provider structured-result decision.
func (m *Metrics) AnalysisValidation(mode, comparisonMode, outcome, code string) {
	labels := map[string]string{"mode": safeLLMMode(mode), "comparison_mode": safeComparisonMode(comparisonMode), "outcome": safeValidationOutcome(outcome), "code": safeValidationCode(code)}
	m.counter("inspection_analysis_validation_total", labels, 1)
}

// AnalysisStage records the time spent preparing, calling, validating or
// persisting an analysis result.
func (m *Metrics) AnalysisStage(stage, outcome string, duration time.Duration) {
	m.observeHistogram("inspection_analysis_stage_duration_seconds", map[string]string{"stage": safeStage(stage), "outcome": safeStageOutcome(outcome)}, duration.Seconds(), llmDurationBuckets)
}

// AnalysisProcessing is emitted after the inbox transaction has returned.
func (m *Metrics) AnalysisProcessing(outcome string) {
	m.counter("inspection_analysis_processing_total", map[string]string{"outcome": safeProcessingOutcome(outcome)}, 1)
}

// AnalysisDelivery records retry/DLQ publication attempts and their result.
func (m *Metrics) AnalysisDelivery(action, result string) {
	m.counter("inspection_analysis_delivery_actions_total", map[string]string{"action": safeDeliveryAction(action), "result": safeDeliveryResult(result)}, 1)
}

// Request records a durable request boundary.
func (m *Metrics) Request(channel, provider string) {
	m.counter("inspection_notification_requests_total", safeLabels(channel, provider), 1)
}

// Dispatch records a reference-only event dispatched to the broker.
func (m *Metrics) Dispatch(channel, provider string) {
	m.counter("inspection_notification_dispatches_total", safeLabels(channel, provider), 1)
}

// Attempt records an actual provider invocation, distinct from broker redelivery.
func (m *Metrics) Attempt(channel, provider string) {
	m.counter("inspection_notification_attempts_total", safeLabels(channel, provider), 1)
}

// Failure records a normalized, safe failure code.
func (m *Metrics) Failure(channel, provider, code string) {
	labels := safeLabels(channel, provider)
	labels["code"] = safeCode(code)
	m.counter("inspection_notification_failures_total", labels, 1)
}

// Retry records a scheduled retry.
func (m *Metrics) Retry(channel, provider string) {
	m.counter("inspection_notification_retries_total", safeLabels(channel, provider), 1)
}

// ExpiredLease records reconciliation of a stale reservation.
func (m *Metrics) ExpiredLease(channel, provider string) {
	m.counter("inspection_notification_expired_leases_total", safeLabels(channel, provider), 1)
}

// Callback records whether a provider callback correlated to durable work.
func (m *Metrics) Callback(provider string, correlated bool) {
	labels := map[string]string{"provider": safeValue(provider), "correlated": strconv.FormatBool(correlated)}
	m.counter("inspection_notification_callbacks_total", labels, 1)
}

// Unknown records a provider outcome that cannot safely be retried.
func (m *Metrics) Unknown(channel, provider string) {
	m.counter("inspection_notification_unknown_total", safeLabels(channel, provider), 1)
}

// ResponsibleEmailCorrection records a correction or explicit reissue.
func (m *Metrics) ResponsibleEmailCorrection(source string) {
	m.counter("inspection_responsible_email_corrections_total", map[string]string{"source": safeCode(source)}, 1)
}

// DeliveryAlert records a terminal delivery alert request.
func (m *Metrics) DeliveryAlert(state string) {
	m.counter("inspection_notification_delivery_alerts_total", map[string]string{"state": safeCode(state)}, 1)
}

// QueueDepth updates the currently observed queue depth.
func (m *Metrics) QueueDepth(queue, channel, provider string, depth int) {
	labels := safeLabels(channel, provider)
	labels["queue"] = safeQueue(queue)
	m.gauge("inspection_notification_queue_depth", labels, float64(depth))
}

// ObserveQueueLatency records time between durable creation and reservation.
func (m *Metrics) ObserveQueueLatency(value time.Duration) {
	m.observe("inspection_notification_queue_latency_seconds", value.Seconds())
}

// ObserveProcessingLatency records time spent processing one reservation.
func (m *Metrics) ObserveProcessingLatency(value time.Duration) {
	m.observe("inspection_notification_processing_latency_seconds", value.Seconds())
}

// ObserveProviderLatency records time spent in provider I/O.
func (m *Metrics) ObserveProviderLatency(value time.Duration) {
	m.observe("inspection_notification_provider_latency_seconds", value.Seconds())
}

// Prometheus renders a deterministic exposition snapshot.
func (m *Metrics) Prometheus() string {
	if m == nil {
		return "inspection_up 1\n"
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var b strings.Builder
	b.WriteString("inspection_up 1\n")
	for _, name := range sortedMetadataKeys(m.metricMeta) {
		metadata := m.metricMeta[name]
		b.WriteString("# HELP " + name + " " + metadata.help + "\n")
		b.WriteString("# TYPE " + name + " " + metadata.typeName + "\n")
	}
	for _, name := range sortedKeys(m.counters) {
		for _, labels := range sortedLabelKeys(m.counters[name]) {
			writeSample(&b, name, labels, m.counters[name][labels])
		}
	}
	for _, name := range sortedKeys(m.gauges) {
		for _, labels := range sortedLabelKeys(m.gauges[name]) {
			writeSample(&b, name, labels, m.gauges[name][labels])
		}
	}
	for _, name := range sortedHistogramKeys(m.hist) {
		value := m.hist[name]
		writeSample(&b, name+"_count", "", value.Count)
		writeSample(&b, name+"_sum", "", value.Sum)
	}
	for _, name := range sortedLabeledHistogramKeys(m.histograms) {
		for _, labels := range sortedLabeledHistogramLabelKeys(m.histograms[name]) {
			value := m.histograms[name][labels]
			bounds := make([]float64, 0, len(value.Buckets))
			for bound := range value.Buckets {
				bounds = append(bounds, bound)
			}
			sort.Float64s(bounds)
			for _, bound := range bounds {
				writeSample(&b, name+"_bucket", withEncodedLabel(labels, "le", strconv.FormatFloat(bound, 'f', -1, 64)), value.Buckets[bound])
			}
			writeSample(&b, name+"_bucket", withEncodedLabel(labels, "le", "+Inf"), value.Count)
			writeSample(&b, name+"_count", labels, value.Count)
			writeSample(&b, name+"_sum", labels, value.Sum)
		}
	}
	return b.String()
}

func safeLabels(channel, provider string) map[string]string {
	return map[string]string{"channel": safeValue(channel), "provider": safeValue(provider)}
}

func safeValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "email", "sms", "whatsapp":
		return value
	case "smtp", "twilio", "meta":
		return value
	default:
		return "unknown"
	}
}

func safeCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return "unknown"
		}
	}
	if len(value) > 64 {
		return "unknown"
	}
	return value
}

func safeQueue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 96 {
		return "unknown"
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' && r != '.' {
			return "unknown"
		}
	}
	return value
}

func safeLLMMode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "mock" || value == "live" {
		return value
	}
	return "unknown"
}

func safeModelAlias(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "inspection-vision" {
		return value
	}
	return "unknown"
}

func safeComparisonMode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "current_only" || value == "compare_origin_current" || value == "" {
		if value == "" {
			return "unknown"
		}
		return value
	}
	return "unknown"
}

func safeStage(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "request", "llm", "validation", "persist":
		return value
	default:
		return "unknown"
	}
}

func safeLLMOutcome(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "success", "invalid_input", "timeout", "cancelled", "transport", "authentication", "rate_limit", "provider_http", "malformed_response":
		return value
	default:
		return "unknown"
	}
}

func safeValidationOutcome(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "accepted", "rejected", "inconclusive":
		return value
	default:
		return "unknown"
	}
}

func safeValidationCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "accepted", "request_invalid", "response_invalid":
		return value
	default:
		return "unknown"
	}
}

func safeStageOutcome(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "success", "error", "accepted", "rejected", "inconclusive":
		return value
	default:
		return "unknown"
	}
}

func safeProcessingOutcome(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "completed", "inconclusive", "duplicate", "error":
		return value
	default:
		return "unknown"
	}
}

func safeDeliveryAction(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "retry" || value == "dlq" {
		return value
	}
	return "unknown"
}

func safeDeliveryResult(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "published" || value == "publish_failed" {
		return value
	}
	return "unknown"
}

func llmLabels(mode, comparisonMode, modelAlias string) map[string]string {
	return map[string]string{"mode": safeLLMMode(mode), "comparison_mode": safeComparisonMode(comparisonMode), "model_alias": safeModelAlias(modelAlias)}
}

func labelKey(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(labels[key])
		b.WriteByte('\x00')
	}
	return b.String()
}

func withEncodedLabel(encoded, key, value string) string {
	labels := make(map[string]string)
	parts := strings.Split(strings.TrimSuffix(encoded, "\x00"), "\x00")
	for _, part := range parts {
		name, current, ok := strings.Cut(part, "=")
		if ok {
			labels[name] = current
		}
	}
	labels[key] = value
	return labelKey(labels)
}

func writeSample(b *strings.Builder, name, encodedLabels string, value float64) {
	if encodedLabels != "" {
		parts := strings.Split(strings.TrimSuffix(encodedLabels, "\x00"), "\x00")
		labels := make([]string, 0, len(parts))
		for _, part := range parts {
			key, val, ok := strings.Cut(part, "=")
			if ok {
				labels = append(labels, fmt.Sprintf("%s=\"%s\"", key, strings.ReplaceAll(val, "\"", "\\\"")))
			}
		}
		b.WriteString(name + "{" + strings.Join(labels, ",") + "} " + strconv.FormatFloat(value, 'f', -1, 64) + "\n")
		return
	}
	b.WriteString(name + " " + strconv.FormatFloat(value, 'f', -1, 64) + "\n")
}

func sortedKeys(values map[string]map[string]float64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedLabelKeys(values map[string]float64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedHistogramKeys(values map[string]histogram) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedMetadataKeys(values map[string]metricMetadata) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedLabeledHistogramKeys(values map[string]map[string]labeledHistogram) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedLabeledHistogramLabelKeys(values map[string]labeledHistogram) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
