package observability

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
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

func TestUnit_Issue015_CounterpartyLogsUseIdentifiersAndResponseOnly(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	tenantID, deliveryID, invitationID, versionID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	LogCounterpartyDeliveryTransition(context.Background(), tenantID, deliveryID, invitationID, "QUEUED", "FAILED", "provider_timeout", "smtp", 2)
	LogCounterpartyResponded(context.Background(), tenantID, invitationID, versionID, "CONTESTED")
	log := output.String()
	for _, value := range []string{tenantID.String(), deliveryID.String(), invitationID.String(), versionID.String(), "counterparty_responded", "provider_timeout", "contested"} {
		if !strings.Contains(log, value) {
			t.Errorf("issue-015 log does not include %q: %s", value, log)
		}
	}
	for _, forbidden := range []string{"statement", "token", "otp", "secret text"} {
		if strings.Contains(strings.ToLower(log), forbidden) {
			t.Errorf("issue-015 log contains forbidden field %q: %s", forbidden, log)
		}
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

func TestObserveHTTPRequestUsesBoundedLabelsAndLatencyBuckets(t *testing.T) {
	m := NewMetrics()
	m.ObserveHTTPRequest("/graphql/tenant-secret", "200", 250*time.Millisecond)
	output := m.Prometheus()
	for _, sample := range []string{
		`inspection_http_request_duration_seconds_bucket{le="0.25",route="other",status_class="5xx"} 1`,
		`inspection_http_request_duration_seconds_bucket{le="0.5",route="other",status_class="5xx"} 1`,
		`inspection_http_request_duration_seconds_bucket{le="0.1",route="other",status_class="5xx"} 0`,
		`inspection_http_request_duration_seconds_bucket{le="+Inf",route="other",status_class="5xx"} 1`,
	} {
		if !strings.Contains(output, sample) {
			t.Fatalf("missing sample %q in:\n%s", sample, output)
		}
	}
	if strings.Contains(output, "tenant-secret") || strings.Contains(output, "/graphql") {
		t.Fatalf("unbounded label value leaked: %s", output)
	}
}
