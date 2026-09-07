package auth

import "testing"

func TestRemoteVerifierRequiresIssuerDiscovery(t *testing.T) {
	if _, err := NewRemoteVerifier(t.Context(), "http://127.0.0.1:1/unavailable", "inspection-web", ""); err == nil {
		t.Fatal("NewRemoteVerifier() error = nil, want discovery failure")
	}
}
