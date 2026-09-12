package observability

import (
	"strings"
	"testing"
	"time"
)

func TestIT047MetricsExposeDeterministicNotificationSignals(t *testing.T) {
	m := NewMetrics()
	m.Request("SMS", "twilio")
	m.Attempt("SMS", "twilio")
	m.Failure("SMS", "twilio", "provider_rejected")
	m.Retry("SMS", "twilio")
	m.ExpiredLease("SMS", "twilio")
	m.Unknown("SMS", "twilio")
	m.Callback("twilio", true)
	m.QueueDepth("notification-delivery-v2", "SMS", "twilio", 3)
	m.ObserveQueueLatency(2 * time.Second)
	m.ObserveProcessingLatency(3 * time.Second)
	m.ObserveProviderLatency(4 * time.Second)

	output := m.Prometheus()
	for _, name := range []string{
		"inspection_notification_queue_depth",
		"inspection_notification_queue_latency_seconds_sum 2",
		"inspection_notification_processing_latency_seconds_sum 3",
		"inspection_notification_provider_latency_seconds_sum 4",
		"inspection_notification_attempts_total",
		"inspection_notification_failures_total",
		"inspection_notification_retries_total",
		"inspection_notification_expired_leases_total",
		"inspection_notification_callbacks_total",
		"inspection_notification_unknown_total",
	} {
		if !strings.Contains(output, name) {
			t.Fatalf("metric %q missing from:\n%s", name, output)
		}
	}
	if strings.Contains(output, "provider_rejected") == false {
		t.Fatal("normalized failure code is missing")
	}
	if !strings.Contains(output, `channel="sms"`) || !strings.Contains(output, `provider="twilio"`) {
		t.Fatalf("safe labels missing: %s", output)
	}
}

func TestMetricsNeverPromoteUntrustedLabels(t *testing.T) {
	m := NewMetrics()
	m.Failure("+5511999999999", "https://token.example", "body=secret")
	output := m.Prometheus()
	if strings.Contains(output, "+5511") || strings.Contains(output, "token.example") || strings.Contains(output, "secret") {
		t.Fatalf("unsafe metric label: %s", output)
	}
}
