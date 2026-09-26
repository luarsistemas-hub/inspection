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

// NewMetrics returns an empty collector.
func NewMetrics() *Metrics {
	return &Metrics{
		counters:   make(map[string]map[string]float64),
		gauges:     make(map[string]map[string]float64),
		hist:       make(map[string]histogram),
		histograms: make(map[string]map[string]labeledHistogram),
		metricMeta: map[string]metricMetadata{
			"inspection_llm_calls_total":                 {typeName: "counter", help: "LLM gateway calls delivered to the configured transport."},
			"inspection_llm_call_duration_seconds":       {typeName: "histogram", help: "Duration of LLM gateway calls in seconds."},
			"inspection_llm_inflight":                    {typeName: "gauge", help: "LLM gateway calls currently in flight."},
			"inspection_llm_tokens_total":                {typeName: "counter", help: "Provider reported LLM tokens."},
			"inspection_llm_cached_input_tokens_total":   {typeName: "counter", help: "Provider reported input tokens served from cache."},
			"inspection_llm_cache_hit_calls_total":       {typeName: "counter", help: "LLM calls with at least one provider reported cached input token."},
			"inspection_llm_cache_usage_missing_total":   {typeName: "counter", help: "Delivered LLM calls without cached input token metadata."},
			"inspection_llm_images_total":                {typeName: "counter", help: "Images sent to the LLM gateway."},
			"inspection_llm_request_body_bytes_total":    {typeName: "counter", help: "Serialized request body bytes sent to the LLM gateway."},
			"inspection_llm_usage_missing_total":         {typeName: "counter", help: "LLM calls without provider usage metadata."},
			"inspection_llm_ledger_writes_total":         {typeName: "counter", help: "Durable LLM call ledger write outcomes."},
			"inspection_analysis_validation_total":       {typeName: "counter", help: "Structured analysis validation outcomes."},
			"inspection_analysis_stage_duration_seconds": {typeName: "histogram", help: "Analysis stage duration in seconds."},
			"inspection_analysis_processing_total":       {typeName: "counter", help: "Analysis processing outcomes after the transaction boundary."},
			"inspection_analysis_delivery_actions_total": {typeName: "counter", help: "RabbitMQ retry and dead letter delivery actions."},
		},
	}
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
			for _, bound := range append(append([]float64{}, llmDurationBuckets...), 0) {
				if bound == 0 {
					continue
				}
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
