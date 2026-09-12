package observability

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Metrics is a small dependency-free Prometheus collector for operational
// notification signals. Labels are constrained to provider/channel values so
// a destination, tenant, or provider response can never become a label.
type Metrics struct {
	mu       sync.RWMutex
	counters map[string]map[string]float64
	gauges   map[string]map[string]float64
	hist     map[string]histogram
}

type histogram struct {
	Count float64
	Sum   float64
}

// NewMetrics returns an empty collector.
func NewMetrics() *Metrics {
	return &Metrics{counters: make(map[string]map[string]float64), gauges: make(map[string]map[string]float64), hist: make(map[string]histogram)}
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
