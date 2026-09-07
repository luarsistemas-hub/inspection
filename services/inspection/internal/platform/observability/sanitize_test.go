package observability

import "testing"

func TestSanitizerContractsUT054UT055(t *testing.T) {
	got := Sanitize(map[string]any{"correlation_id": "c", "code": "INTERNAL", "tenant_id": "tenant", "Authorization": "Bearer x", "otp": "123456", "provider_response": "secret", "image_bytes": "x", "email": "a@b"})
	if got["correlation_id"] != "c" || got["code"] != "INTERNAL" || got["tenant_hash"] == nil {
		t.Fatalf("safe fields missing: %#v", got)
	}
	for _, key := range []string{"Authorization", "otp", "provider_response", "image_bytes", "email", "tenant_id"} {
		if _, ok := got[key]; ok {
			t.Fatalf("field leaked: %s", key)
		}
	}
}
