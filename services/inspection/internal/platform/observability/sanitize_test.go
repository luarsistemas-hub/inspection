package observability

import (
	"bytes"
	"strings"
	"testing"
)

func TestSanitizerContractsUT054UT055(t *testing.T) {
	got := Sanitize(map[string]any{"correlation_id": "c", "notification_id": "n", "code": "INTERNAL", "tenant_id": "tenant", "Authorization": "Bearer x", "otp": "123456", "provider_response": "secret", "image_bytes": "x", "email": "a@b", "recipient": "+5511", "capture_url": "https://x/capture/token", "ciphertext": "encrypted"})
	if got["correlation_id"] != "c" || got["code"] != "INTERNAL" || got["tenant_hash"] == nil {
		t.Fatalf("safe fields missing: %#v", got)
	}
	for _, key := range []string{"Authorization", "otp", "provider_response", "image_bytes", "email", "recipient", "capture_url", "ciphertext", "tenant_id"} {
		if _, ok := got[key]; ok {
			t.Fatalf("field leaked: %s", key)
		}
	}
}

func TestIT048StructuredLogRedactsSensitiveNotificationFields(t *testing.T) {
	var output bytes.Buffer
	if err := Log(&output, "notification_attempt", map[string]any{"correlation_id": "corr-1", "notification_id": "notification-1", "code": "provider_rejected", "recipient": "+5511999999999", "capture_token": "opaque", "rendered_body": "sensitive"}); err != nil {
		t.Fatal(err)
	}
	value := output.String()
	if !strings.Contains(value, "corr-1") || !strings.Contains(value, "notification-1") || !strings.Contains(value, "provider_rejected") {
		t.Fatalf("safe evidence missing: %s", value)
	}
	for _, secret := range []string{"+5511", "opaque", "sensitive"} {
		if strings.Contains(value, secret) {
			t.Fatalf("log leaked %q: %s", secret, value)
		}
	}
}
